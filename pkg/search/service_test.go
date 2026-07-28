package search

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/robinjoseph08/memento/pkg/setup"
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

func TestDateFilterRejectsInvalidVariantsAfterJSONDecoding(t *testing.T) {
	for _, body := range []string{
		`{"date":{"kind":"year"}}`,
		`{"date":{"kind":"year","year":2026,"month":"2026-07"}}`,
		`{"date":{"kind":"range","start_date":"2026-07-29","end_date":"2026-07-20"}}`,
	} {
		var request Request
		require.NoError(t, json.Unmarshal([]byte(body), &request))
		_, _, err := parseRequest(request)
		assert.ErrorIs(t, err, ErrInvalidRequest)
	}
}

func TestTokenizeKeepsDiacriticNormalizedUniqueUnicodeWordsAndDiscardsMarkOnlyTerms(t *testing.T) {
	assert.Equal(t, []string{"café", "são", "2026"}, tokenize(" CAFÉ, cafe café São sao / 2026 "))
	assert.Equal(t, []string{"café"}, tokenize("café cafe cafe\u0301"))
	assert.Empty(t, tokenize("\u0301 \u0308"))

	_, _, err := parseRequest(Request{Query: "\u0301"})
	require.ErrorIs(t, err, ErrInvalidRequest)

	date := "2026-07-28"
	terms, _, err := parseRequest(Request{Query: "\u0301", Date: &DateFilter{Kind: "date", Date: &date}})
	require.NoError(t, err)
	assert.Empty(t, terms)
}

func TestParseRequestBoundsUniqueTermsWithoutPenalizingDuplicates(t *testing.T) {
	allowed := strings.Join([]string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l"}, " ")
	terms, _, err := parseRequest(Request{Query: allowed})
	require.NoError(t, err)
	assert.Len(t, terms, maxSearchTerms)

	terms, _, err = parseRequest(Request{Query: strings.Repeat("a ", 100)})
	require.NoError(t, err)
	assert.Equal(t, []string{"a"}, terms)

	terms, _, err = parseRequest(Request{Query: strings.Repeat("café cafe ", 20)})
	require.NoError(t, err)
	assert.Equal(t, []string{"café"}, terms)

	_, _, err = parseRequest(Request{Query: allowed + " m"})
	require.ErrorIs(t, err, ErrTooManyTerms)
	assert.ErrorIs(t, err, ErrInvalidRequest)
}

func TestLongTermDocumentMatchingUsesAnIndexableConservativeTypoPrefilter(t *testing.T) {
	query, args := matchingCTE(setup.SessionActor{}, []string{"reunoin"}, nil)

	assert.Contains(t, query, "memento_normalize_search_text(?) OPERATOR(public.<<%) authorized.normalized_search_text")
	assert.Contains(t, query, "strict_word_similarity(memento_normalize_search_text(?), authorized.normalized_search_text) >= 0.3")
	assert.Len(t, args, 8)
}
