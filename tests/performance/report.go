// Package performance provides the target-scale performance evidence harness.
package performance

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"
)

var (
	errInvalidSamples = errors.New("invalid performance samples")
	errInvalidReport  = errors.New("invalid performance report")
)

// Baseline identifies one normative target from the product specification.
type Baseline struct {
	Name        string        `json:"name"`
	Target      time.Duration `json:"target"`
	Description string        `json:"description"`
}

// Baselines is the complete target matrix from docs/product-architecture-spec.md.
var Baselines = []Baseline{
	{Name: "liveness", Target: 50 * time.Millisecond, Description: "Liveness response"},
	{Name: "readiness", Target: 500 * time.Millisecond, Description: "Readiness response with healthy dependencies"},
	{Name: "session_authorization", Target: 50 * time.Millisecond, Description: "Session validation plus simple authorization"},
	{Name: "recipient_list", Target: 300 * time.Millisecond, Description: "Recipient timeline or Event page, up to 100 items"},
	{Name: "curator_list", Target: 300 * time.Millisecond, Description: "Curator work queue or People list"},
	{Name: "search", Target: 500 * time.Millisecond, Description: "Authorized search first page"},
	{Name: "mutation", Target: 300 * time.Millisecond, Description: "Comment, Favorite, preference, or seen-state mutation"},
	{Name: "audience_proposal", Target: time.Second, Description: "Audience proposal recalculation for 50 Recipients and 500 Moment items"},
	{Name: "publication", Target: 3 * time.Second, Description: "Atomic Publication with 5,000 placements and 50 Recipients"},
	{Name: "job_start", Target: 60 * time.Second, Description: "Eligible job start after available_at"},
	{Name: "notification_dispatch", Target: 2 * time.Minute, Description: "Notification dispatch start after coalescing closes"},
	{Name: "reconciliation", Target: 30 * time.Minute, Description: "Full 100,000-item reconciliation"},
	{Name: "proxy_overhead", Target: 150 * time.Millisecond, Description: "Media proxy first-byte application overhead"},
	{Name: "stream_buffer", Target: time.Duration(1 << 20), Description: "Application buffer bytes per active Media stream"},
}

// FixtureShape records the data cardinalities required to reproduce a run.
type FixtureShape struct {
	MediaItems             int    `json:"media_items"`
	Recipients             int    `json:"recipients"`
	Events                 int    `json:"events"`
	LargestEventPlacements int    `json:"largest_event_placements"`
	ReusedMediaItems       int    `json:"reused_media_items"`
	AudienceEntries        int    `json:"audience_entries"`
	PublicationRecipients  int    `json:"publication_recipients"`
	OverlappingRecipients  int    `json:"overlapping_recipients"`
	ProposalMomentItems    int    `json:"proposal_moment_items"`
	AttendanceRows         int    `json:"attendance_rows"`
	Comments               int    `json:"comments"`
	Favorites              int    `json:"favorites"`
	SearchDocuments        int    `json:"search_documents"`
	DeliveryActivity       int    `json:"delivery_activity"`
	Checksum               string `json:"checksum"`
}

// Environment records the execution environment without secrets.
type Environment struct {
	GitRevision       string `json:"git_revision"`
	GitDirty          bool   `json:"git_dirty"`
	OS                string `json:"os"`
	Architecture      string `json:"architecture"`
	CPU               string `json:"cpu"`
	LogicalCPUs       int    `json:"logical_cpus"`
	MemoryBytes       uint64 `json:"memory_bytes"`
	GoVersion         string `json:"go_version"`
	PostgreSQLVersion string `json:"postgresql_version"`
	DatabaseSizeBytes int64  `json:"database_size_bytes"`
	DatabasePoolSize  int    `json:"database_pool_size"`
}

// Metric is one measured baseline in one explicit scenario.
type Metric struct {
	Name                 string        `json:"name"`
	Description          string        `json:"description"`
	Target               time.Duration `json:"target"`
	Unit                 string        `json:"unit"`
	Samples              []int64       `json:"samples"`
	P95                  int64         `json:"p95"`
	Passed               bool          `json:"passed"`
	CacheState           string        `json:"cache_state"`
	Scenario             string        `json:"scenario"`
	CompetingWork        string        `json:"competing_work"`
	Concurrency          int           `json:"concurrency"`
	ImmichLatency        time.Duration `json:"immich_latency"`
	ImmichBlocked        time.Duration `json:"immich_blocked"`
	MaximumUpstreamReads int           `json:"maximum_upstream_reads,omitempty"`
	Notes                []string      `json:"notes,omitempty"`
}

// PlanEvidence stores PostgreSQL's machine-readable execution plan and buffers.
type PlanEvidence struct {
	Name       string          `json:"name"`
	CacheState string          `json:"cache_state"`
	SQLRole    string          `json:"sql_role"`
	Plan       json.RawMessage `json:"plan"`
}

// Report is the complete reproducible evidence artifact.
type Report struct {
	SchemaVersion int            `json:"schema_version"`
	Qualifying    bool           `json:"qualifying"`
	GeneratedAt   time.Time      `json:"generated_at"`
	CacheState    string         `json:"cache_state"`
	Fixture       FixtureShape   `json:"fixture"`
	Environment   Environment    `json:"environment"`
	Metrics       []Metric       `json:"metrics"`
	Comparisons   []Metric       `json:"comparisons"`
	Plans         []PlanEvidence `json:"plans"`
}

// NearestRankP95 returns the nearest-rank 95th percentile without mutating samples.
func NearestRankP95(samples []int64) (int64, error) {
	if len(samples) == 0 {
		return 0, fmt.Errorf("%w: p95 requires at least one sample", errInvalidSamples)
	}
	ordered := append([]int64(nil), samples...)
	for _, sample := range ordered {
		if sample < 0 {
			return 0, fmt.Errorf("%w: p95 samples cannot be negative", errInvalidSamples)
		}
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	rank := int(math.Ceil(0.95*float64(len(ordered)))) - 1
	return ordered[rank], nil
}

// NewDurationMetric constructs a latency metric using nanosecond samples.
func NewDurationMetric(baseline Baseline, samples []time.Duration, cacheState, scenario, competingWork string, concurrency int, immichLatency time.Duration) (Metric, error) {
	values := make([]int64, len(samples))
	for index, sample := range samples {
		values[index] = sample.Nanoseconds()
	}
	p95, err := NearestRankP95(values)
	if err != nil {
		return Metric{}, err
	}
	return Metric{Name: baseline.Name, Description: baseline.Description, Target: baseline.Target, Unit: "nanoseconds", Samples: values, P95: p95, Passed: time.Duration(p95) < baseline.Target, CacheState: cacheState, Scenario: scenario, CompetingWork: competingWork, Concurrency: concurrency, ImmichLatency: immichLatency}, nil
}

// NewByteMetric constructs the stream-buffer target, whose duration field is used as bytes.
func NewByteMetric(baseline Baseline, samples []int64, cacheState, scenario string, concurrency int) (Metric, error) {
	p95, err := NearestRankP95(samples)
	if err != nil {
		return Metric{}, err
	}
	target := int64(baseline.Target)
	return Metric{Name: baseline.Name, Description: baseline.Description, Target: baseline.Target, Unit: "bytes", Samples: append([]int64(nil), samples...), P95: p95, Passed: p95 <= target, CacheState: cacheState, Scenario: scenario, CompetingWork: "none", Concurrency: concurrency}, nil
}

// BaselineByName returns one required target.
func BaselineByName(name string) (Baseline, bool) {
	for _, baseline := range Baselines {
		if baseline.Name == name {
			return baseline, true
		}
	}
	return Baseline{}, false
}

// Validate rejects partial or ambiguous evidence.
func (r Report) Validate() error {
	if r.SchemaVersion != 1 {
		return fmt.Errorf("%w: unsupported schema version %d", errInvalidReport, r.SchemaVersion)
	}
	if r.CacheState == "" || r.Fixture.Checksum == "" || r.Environment.PostgreSQLVersion == "" {
		return fmt.Errorf("%w: missing cache, fixture checksum, or PostgreSQL evidence", errInvalidReport)
	}
	if r.Fixture.MediaItems != 100000 || r.Fixture.Recipients != 50 || r.Fixture.Events == 0 || r.Fixture.LargestEventPlacements != 5000 || r.Fixture.ReusedMediaItems == 0 || r.Fixture.AudienceEntries == 0 || r.Fixture.PublicationRecipients != 50 || r.Fixture.OverlappingRecipients == 0 || r.Fixture.ProposalMomentItems != 500 || r.Fixture.AttendanceRows != 50 || r.Fixture.Comments == 0 || r.Fixture.Favorites == 0 || r.Fixture.SearchDocuments == 0 || r.Fixture.DeliveryActivity == 0 {
		return fmt.Errorf("%w: fixture does not match the target-scale shape: %+v", errInvalidReport, r.Fixture)
	}
	seen := make(map[string]bool, len(r.Metrics))
	for _, metric := range r.Metrics {
		if _, required := BaselineByName(metric.Name); !required {
			return fmt.Errorf("%w: unknown metric %q", errInvalidReport, metric.Name)
		}
		if seen[metric.Name] {
			return fmt.Errorf("%w: duplicate steady-state metric %q", errInvalidReport, metric.Name)
		}
		seen[metric.Name] = true
		if metric.CacheState == "" || metric.Scenario == "" || metric.Concurrency < 1 || len(metric.Samples) == 0 || metric.Unit == "" {
			return fmt.Errorf("%w: metric %q lacks scenario evidence", errInvalidReport, metric.Name)
		}
		baseline, _ := BaselineByName(metric.Name)
		observed, err := NearestRankP95(metric.Samples)
		if err != nil || observed != metric.P95 || metric.Target != baseline.Target {
			return fmt.Errorf("%w: metric %q has stale percentile or target", errInvalidReport, metric.Name)
		}
		expectedPass := time.Duration(observed) < baseline.Target
		if metric.Unit == "bytes" {
			expectedPass = observed <= int64(baseline.Target)
		}
		if metric.Passed != expectedPass {
			return fmt.Errorf("%w: metric %q has stale pass state", errInvalidReport, metric.Name)
		}
	}
	for _, baseline := range Baselines {
		if !seen[baseline.Name] {
			return fmt.Errorf("%w: missing baseline metric %q", errInvalidReport, baseline.Name)
		}
	}
	if r.Qualifying {
		for _, metric := range r.Metrics {
			minimum := 20
			if metric.Name == "liveness" {
				minimum = 100
			}
			if metric.Name == "reconciliation" {
				minimum = 1
			}
			if metric.Name == "stream_buffer" {
				minimum = 32
			}
			if len(metric.Samples) < minimum {
				return fmt.Errorf("%w: qualifying metric %q requires at least %d samples", errInvalidReport, metric.Name, minimum)
			}
		}
	}
	competitors := map[string]bool{}
	for _, comparison := range r.Comparisons {
		if comparison.CacheState == "" || comparison.Scenario == "" || comparison.CompetingWork == "" || comparison.CompetingWork == "none" || comparison.Concurrency < 1 || len(comparison.Samples) == 0 {
			return fmt.Errorf("%w: comparison %q lacks competing-work evidence", errInvalidReport, comparison.Name)
		}
		observed, err := NearestRankP95(comparison.Samples)
		baseline, known := BaselineByName(comparison.Name)
		if err != nil || !known || observed != comparison.P95 || comparison.Target != baseline.Target || comparison.Passed != (time.Duration(observed) < baseline.Target) {
			return fmt.Errorf("%w: comparison %q has stale evidence", errInvalidReport, comparison.CompetingWork)
		}
		competitors[comparison.CompetingWork] = true
		if r.Qualifying && len(comparison.Samples) < 20 {
			return fmt.Errorf("%w: qualifying comparison %q requires at least 20 samples", errInvalidReport, comparison.CompetingWork)
		}
	}
	if !competitors["reconciliation"] || !competitors["publication"] || !competitors["notification dispatch"] {
		return fmt.Errorf("%w: reconciliation, publication, and notification dispatch competing-work evidence is required", errInvalidReport)
	}
	requiredPlans := map[string]bool{"authorization": false, "media_authorization": false, "gallery": false, "chronology": false, "search": false, "curator": false}
	for _, plan := range r.Plans {
		if plan.Name == "" || plan.CacheState == "" || plan.SQLRole == "" || !json.Valid(plan.Plan) {
			return fmt.Errorf("%w: invalid PostgreSQL plan evidence %q", errInvalidReport, plan.Name)
		}
		seen, required := requiredPlans[plan.Name]
		if !required || seen {
			return fmt.Errorf("%w: unknown or duplicate PostgreSQL plan %q", errInvalidReport, plan.Name)
		}
		requiredPlans[plan.Name] = true
	}
	for name, present := range requiredPlans {
		if !present {
			return fmt.Errorf("%w: missing PostgreSQL plan %q", errInvalidReport, name)
		}
	}
	return nil
}

// Write emits matching JSON and Markdown artifacts.
func (r Report) Write(jsonPath, markdownPath string) error {
	if err := r.Validate(); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(jsonPath, append(encoded, '\n'), 0o600); err != nil {
		return err
	}
	return os.WriteFile(markdownPath, []byte(r.Markdown()), 0o600)
}

// Markdown renders a compact human-readable report.
func (r Report) Markdown() string {
	var output strings.Builder
	fmt.Fprintf(&output, "# Target-scale performance report\n\n- Generated: `%s`\n- Qualifying: `%t`\n- Git revision: `%s` (dirty: `%t`)\n- Cache state: `%s`\n- PostgreSQL: `%s`\n\n", r.GeneratedAt.UTC().Format(time.RFC3339), r.Qualifying, r.Environment.GitRevision, r.Environment.GitDirty, r.CacheState, r.Environment.PostgreSQLVersion)
	fmt.Fprintf(&output, "## Fixture\n\n100,000 Media items, %d Recipients, %d Events, largest Event %d placements, %d reused Media items, %d Audience entries, %d Publication Recipients, %d overlapping Recipients, a %d-item proposal Moment with %d Attendance rows, %d Comments, %d Favorites, %d search documents, and %d delivery activity rows. Fixture checksum: `%s`.\n\n", r.Fixture.Recipients, r.Fixture.Events, r.Fixture.LargestEventPlacements, r.Fixture.ReusedMediaItems, r.Fixture.AudienceEntries, r.Fixture.PublicationRecipients, r.Fixture.OverlappingRecipients, r.Fixture.ProposalMomentItems, r.Fixture.AttendanceRows, r.Fixture.Comments, r.Fixture.Favorites, r.Fixture.SearchDocuments, r.Fixture.DeliveryActivity, r.Fixture.Checksum)
	output.WriteString("## Baselines\n\n| Operation | p95 | Target | Result | Scenario | Concurrency | Immich latency |\n| --- | ---: | ---: | --- | --- | ---: | ---: |\n")
	for _, metric := range r.Metrics {
		result := "PASS"
		if !metric.Passed {
			result = "FAIL"
		}
		p95, target := time.Duration(metric.P95).String(), metric.Target.String()
		if metric.Unit == "bytes" {
			p95, target = fmt.Sprintf("%d B", metric.P95), fmt.Sprintf("%d B", int64(metric.Target))
		}
		fmt.Fprintf(&output, "| %s | %s | %s | %s | %s%s | %d | %s |\n", metric.Description, p95, target, result, metric.Scenario, competingSuffix(metric.CompetingWork), metric.Concurrency, metric.ImmichLatency)
	}
	output.WriteString("\n## Competing work\n\n| Operation | p95 | Competitor | Concurrency |\n| --- | ---: | --- | ---: |\n")
	for _, metric := range r.Comparisons {
		fmt.Fprintf(&output, "| %s | %s | %s | %d |\n", metric.Description, time.Duration(metric.P95), metric.CompetingWork, metric.Concurrency)
	}
	output.WriteString("\n## PostgreSQL plans and buffers\n\n")
	for _, plan := range r.Plans {
		fmt.Fprintf(&output, "- `%s`: %s cache, role `%s`; full `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)` is in the JSON artifact.\n", plan.Name, plan.CacheState, plan.SQLRole)
	}
	fmt.Fprintf(&output, "\n## Environment\n\n- OS/architecture: `%s/%s`\n- CPU: `%s` (%d logical CPUs)\n- Memory: %d bytes\n- Go: `%s`\n- Database size: %d bytes\n- Database pool: %d connections\n", r.Environment.OS, r.Environment.Architecture, r.Environment.CPU, r.Environment.LogicalCPUs, r.Environment.MemoryBytes, r.Environment.GoVersion, r.Environment.DatabaseSizeBytes, r.Environment.DatabasePoolSize)
	return output.String()
}

func competingSuffix(value string) string {
	if value == "" || value == "none" {
		return ""
	}
	return " with " + value
}
