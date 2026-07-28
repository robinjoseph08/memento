package search

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRequestSupportsExplicitDateSemantics(t *testing.T) {
	year := 2026
	month, date, start, end := "2026-07", "2026-07-27", "2026-07-20", "2026-07-29"
	for _, test := range []struct {
		name   string
		filter DateFilter
		start  string
		end    string
	}{
		{name: "year", filter: DateFilter{Kind: "year", Year: &year}, start: "2026-01-01", end: "2026-12-31"},
		{name: "month", filter: DateFilter{Kind: "month", Month: &month}, start: "2026-07-01", end: "2026-07-31"},
		{name: "date", filter: DateFilter{Kind: "date", Date: &date}, start: date, end: date},
		{name: "range", filter: DateFilter{Kind: "range", StartDate: &start, EndDate: &end}, start: start, end: end},
	} {
		t.Run(test.name, func(t *testing.T) {
			terms, bounds, err := parseRequest(Request{Date: &test.filter})
			require.NoError(t, err)
			assert.Empty(t, terms)
			require.NotNil(t, bounds)
			assert.Equal(t, test.start, bounds.start.Format(time.DateOnly))
			assert.Equal(t, test.end, bounds.end.Format(time.DateOnly))
		})
	}
}

func TestParseRequestRejectsIncompleteAmbiguousAndReversedFilters(t *testing.T) {
	year := 2026
	month := "2026-07"
	start, end := "2026-07-29", "2026-07-20"
	for _, request := range []Request{
		{},
		{Date: &DateFilter{Kind: "year"}},
		{Date: &DateFilter{Kind: "month", Year: &year}},
		{Date: &DateFilter{Kind: "year", Year: &year, Month: &month}},
		{Date: &DateFilter{Kind: "range", StartDate: &start, EndDate: &end}},
	} {
		_, _, err := parseRequest(request)
		assert.ErrorIs(t, err, ErrInvalidRequest)
	}
}

func TestDateFilterRejectsInvalidVariantsAtTheJSONBoundary(t *testing.T) {
	for _, body := range []string{
		`{"date":{"kind":"year"}}`,
		`{"date":{"kind":"year","year":2026,"month":"2026-07"}}`,
		`{"date":{"kind":"range","start_date":"2026-07-29","end_date":"2026-07-20"}}`,
	} {
		var request Request
		err := json.Unmarshal([]byte(body), &request)
		assert.ErrorIs(t, err, ErrInvalidRequest)
	}
}

func TestTokenizeKeepsUnicodeWordsForDatabaseUnaccentNormalization(t *testing.T) {
	assert.Equal(t, []string{"café", "são", "2026"}, tokenize(" CAFÉ, São / 2026 "))
}
