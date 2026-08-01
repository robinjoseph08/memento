package performance

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNearestRankP95UsesIndependentSortedSamples(t *testing.T) {
	samples := []int64{100, 1, 10, 20, 30, 40, 50, 60, 70, 80, 90, 2, 3, 4, 5, 6, 7, 8, 9, 11}
	p95, err := NearestRankP95(samples)
	require.NoError(t, err)
	assert.Equal(t, int64(90), p95)
	assert.Equal(t, int64(100), samples[0], "measurement ordering remains available in the artifact")
}

func TestNearestRankP95RejectsMissingAndNegativeSamples(t *testing.T) {
	_, err := NearestRankP95(nil)
	require.Error(t, err)
	_, err = NearestRankP95([]int64{1, -1})
	require.Error(t, err)
}

func TestReportValidationRequiresEveryBaselineAndScaleEvidence(t *testing.T) {
	report := completeReport(t)
	require.NoError(t, report.Validate())

	report.Metrics = report.Metrics[:len(report.Metrics)-1]
	require.ErrorContains(t, report.Validate(), `missing baseline metric "stream_buffer"`)
}

func TestQualifyingReportEnforcesSampleMinimums(t *testing.T) {
	report := completeReport(t)
	report.Qualifying = true
	for index := range report.Metrics {
		minimum := 20
		switch report.Metrics[index].Name {
		case "liveness":
			minimum = 100
		case "reconciliation":
			minimum = 1
		case "stream_buffer":
			minimum = 32
		}
		report.Metrics[index].Samples = repeatedSample(report.Metrics[index].P95, minimum)
	}
	for index := range report.Comparisons {
		report.Comparisons[index].Samples = repeatedSample(report.Comparisons[index].P95, 20)
	}
	require.NoError(t, report.Validate())

	for index := range report.Metrics {
		if report.Metrics[index].Name == "publication" {
			report.Metrics[index].Samples = report.Metrics[index].Samples[:19]
		}
	}
	require.ErrorContains(t, report.Validate(), `qualifying metric "publication" requires at least 20 samples`)
}

func repeatedSample(value int64, count int) []int64 {
	result := make([]int64, count)
	for index := range result {
		result[index] = value
	}
	return result
}

func TestReportMarkdownRecordsRequiredContext(t *testing.T) {
	markdown := completeReport(t).Markdown()
	assert.Contains(t, markdown, "100,000 Media items")
	assert.Contains(t, markdown, "Immich latency")
	assert.Contains(t, markdown, "PostgreSQL plans and buffers")
	assert.Contains(t, markdown, "steady with reconciliation")
}

func completeReport(t *testing.T) Report {
	t.Helper()
	metrics := make([]Metric, 0, len(Baselines))
	for _, baseline := range Baselines {
		var metric Metric
		var err error
		if baseline.Name == "stream_buffer" {
			metric, err = NewByteMetric(baseline, []int64{32 << 10}, "warm", "steady", 8)
		} else {
			metric, err = NewDurationMetric(baseline, []time.Duration{time.Microsecond}, "warm", "steady", "reconciliation", 1, 5*time.Millisecond)
		}
		require.NoError(t, err)
		metrics = append(metrics, metric)
	}
	plan, err := json.Marshal([]any{map[string]any{"Plan": map[string]any{"Node Type": "Index Scan"}}})
	require.NoError(t, err)
	planNames := []string{"authorization", "media_authorization", "gallery", "chronology", "search", "curator"}
	plans := make([]PlanEvidence, len(planNames))
	for index := range plans {
		plans[index] = PlanEvidence{Name: planNames[index], CacheState: "warm", SQLRole: "memento_app", Plan: plan}
	}
	return Report{
		SchemaVersion: 1,
		Qualifying:    false,
		GeneratedAt:   time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
		CacheState:    "warm",
		Fixture:       FixtureShape{MediaItems: 100000, Recipients: 50, Events: 21, LargestEventPlacements: 5000, ReusedMediaItems: 1000, AudienceEntries: 250, PublicationRecipients: 50, OverlappingRecipients: 25, ProposalMomentItems: 500, AttendanceRows: 50, Comments: 1000, Favorites: 1000, SearchDocuments: 100000, DeliveryActivity: 50, Checksum: "fixture-v1"},
		Environment:   Environment{GitRevision: "abc", OS: "linux", Architecture: "amd64", CPU: "test", LogicalCPUs: 4, GoVersion: "go1.26", PostgreSQLVersion: "PostgreSQL 17", DatabasePoolSize: 16},
		Metrics:       metrics,
		Comparisons: []Metric{
			{Name: "recipient_list", Description: "Recipient Event list", Target: 300 * time.Millisecond, Unit: "nanoseconds", Samples: []int64{1}, P95: 1, Passed: true, CacheState: "warm", Scenario: "competing", CompetingWork: "reconciliation", Concurrency: 2},
			{Name: "recipient_list", Description: "Recipient Event list", Target: 300 * time.Millisecond, Unit: "nanoseconds", Samples: []int64{1}, P95: 1, Passed: true, CacheState: "warm", Scenario: "competing", CompetingWork: "publication", Concurrency: 2},
			{Name: "search", Description: "Authorized search", Target: 500 * time.Millisecond, Unit: "nanoseconds", Samples: []int64{1}, P95: 1, Passed: true, CacheState: "warm", Scenario: "competing", CompetingWork: "notification dispatch", Concurrency: 2},
		},
		Plans: plans,
	}
}
