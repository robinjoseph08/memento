package binder

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBindJSON(t *testing.T) {
	t.Parallel()

	var payload struct {
		Name string `json:"name" mod:"trim" validate:"required"`
		Page int    `json:"page" default:"1" validate:"min=1"`
	}
	err := bind(t, http.MethodPost, "/", `{"name":"  Example  "}`, echo.MIMEApplicationJSON, &payload, nil)
	require.NoError(t, err)
	assert.Equal(t, "Example", payload.Name)
	assert.Equal(t, 1, payload.Page)
}

func TestBindJSONRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	var payload struct {
		Name string `json:"name"`
	}
	err := bind(t, http.MethodPost, "/", `{"name":"Example","extra":true}`, echo.MIMEApplicationJSON, &payload, nil)
	assertAPIError(t, err, "unknown_parameter")
}

func TestBindJSONCanAllowUnknownFields(t *testing.T) {
	t.Parallel()

	var payload struct {
		Name string `json:"name"`
	}
	err := bind(t, http.MethodPost, "/", `{"name":"Example","extra":true}`, echo.MIMEApplicationJSON, &payload, map[string]any{
		disallowUnknownFieldsKey: false,
	})
	require.NoError(t, err)
	assert.Equal(t, "Example", payload.Name)
}

func TestBindTypeErrorHasFriendlyField(t *testing.T) {
	t.Parallel()
	b, err := New()
	require.NoError(t, err)
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"count":"many"}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	var input struct {
		Count int `json:"count"`
	}
	err = b.Bind(e.NewContext(req, httptest.NewRecorder()), &input)
	var fieldErr *errcodes.FieldError
	require.ErrorAs(t, err, &fieldErr)
	assert.Equal(t, "Enter a whole number.", fieldErr.Fields["count"])
	assert.Equal(t, "Check the highlighted fields.", fieldErr.Error())
}

func TestBindJSONRejectsWrongType(t *testing.T) {
	t.Parallel()

	var payload struct {
		Count int `json:"count"`
	}
	err := bind(t, http.MethodPost, "/", `{"count":"many"}`, echo.MIMEApplicationJSON, &payload, nil)
	assertAPIError(t, err, "validation_type_error")
}

func TestBindJSONRejectsMalformedAndTrailingPayloads(t *testing.T) {
	t.Parallel()

	for name, body := range map[string]string{
		"malformed": `{"name":`,
		"trailing":  `{"name":"one"}{"name":"two"}`,
	} {
		name := name
		body := body
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var payload struct {
				Name string `json:"name"`
			}
			err := bind(t, http.MethodPost, "/", body, echo.MIMEApplicationJSON, &payload, nil)
			assertAPIError(t, err, "malformed_payload")
		})
	}
}

func TestBindEmptyBody(t *testing.T) {
	t.Parallel()

	var payload struct{}
	err := bind(t, http.MethodPost, "/", "", "", &payload, nil)
	assertAPIError(t, err, "empty_request_body")

	err = bind(t, http.MethodPost, "/", "", "", &payload, map[string]any{disallowEmptyBodyKey: false})
	require.NoError(t, err)
}

func TestBindChunkedJSONBody(t *testing.T) {
	t.Parallel()

	binder, err := New()
	require.NoError(t, err)
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"chunked"}`))
	req.ContentLength = -1
	req.TransferEncoding = []string{"chunked"}
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	ctx := e.NewContext(req, httptest.NewRecorder())

	var payload struct {
		Name string `json:"name"`
	}
	require.NoError(t, binder.Bind(ctx, &payload))
	assert.Equal(t, "chunked", payload.Name)
}

func TestBindQuery(t *testing.T) {
	t.Parallel()

	var payload struct {
		Page int `query:"page" validate:"min=1"`
	}
	err := bind(t, http.MethodGet, "/?page=3", "", "", &payload, nil)
	require.NoError(t, err)
	assert.Equal(t, 3, payload.Page)

	err = bind(t, http.MethodGet, "/?page=invalid", "", "", &payload, nil)
	assertAPIError(t, err, "validation_type_error")
}

func TestBindForm(t *testing.T) {
	t.Parallel()

	form := url.Values{"name": {"Example"}, "count": {"2"}}
	var payload struct {
		Name  string `form:"name"`
		Count int    `form:"count"`
	}
	err := bind(t, http.MethodPost, "/", form.Encode(), echo.MIMEApplicationForm, &payload, nil)
	require.NoError(t, err)
	assert.Equal(t, "Example", payload.Name)
	assert.Equal(t, 2, payload.Count)
}

func TestBindMultipartForm(t *testing.T) {
	t.Parallel()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("name", "Example"))
	file, err := writer.CreateFormFile("attachment", "note.txt")
	require.NoError(t, err)
	_, err = file.Write([]byte("remember this"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	var payload struct {
		Name      string                           `form:"name"`
		FormFiles map[string]*multipart.FileHeader `form:"-"`
	}
	err = bind(t, http.MethodPost, "/", body.String(), writer.FormDataContentType(), &payload, nil)
	require.NoError(t, err)
	assert.Equal(t, "Example", payload.Name)
	require.Contains(t, payload.FormFiles, "attachment")
	assert.Equal(t, "note.txt", payload.FormFiles["attachment"].Filename)
}

func TestBindAllFieldErrors(t *testing.T) {
	t.Parallel()
	b, err := New()
	require.NoError(t, err)
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"email":"bad","display_name":""}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	c := e.NewContext(req, httptest.NewRecorder())
	var body struct {
		Email string `json:"email" validate:"required,email"`
		Name  string `json:"display_name" validate:"required"`
	}
	err = b.Bind(c, &body)
	var fields *errcodes.FieldError
	require.ErrorAs(t, err, &fields)
	assert.Equal(t, map[string]string{
		"email":        "Enter a valid email address.",
		"display_name": "Enter a value.",
	}, fields.Fields)
	var apiErr *errcodes.Error
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, "Check the highlighted fields.", apiErr.Message)
}

func TestBindReadableLimits(t *testing.T) {
	t.Parallel()

	var payload struct {
		DisplayName string   `json:"display_name" validate:"max=100"`
		ShortName   string   `json:"short_name" validate:"min=2"`
		Page        int      `json:"page" validate:"min=1"`
		Count       int      `json:"count" validate:"max=10"`
		Tags        []string `json:"tags" validate:"min=1"`
		Choices     []string `json:"choices" validate:"max=2"`
	}
	body := `{"display_name":"` + strings.Repeat("é", 101) + `","short_name":"a","page":0,"count":11,"choices":["a","b","c"]}`
	err := bind(t, http.MethodPost, "/", body, echo.MIMEApplicationJSON, &payload, nil)
	var fields *errcodes.FieldError
	require.ErrorAs(t, err, &fields)
	assert.Equal(t, map[string]string{
		"display_name": "Use 100 characters or fewer.",
		"short_name":   "Use at least 2 characters.",
		"page":         "Enter 1 or more.",
		"count":        "Enter 10 or less.",
		"tags":         "Choose at least 1 item.",
		"choices":      "Choose 2 items or fewer.",
	}, fields.Fields)
}

func TestBindReadableFormatsAndComparisons(t *testing.T) {
	t.Parallel()

	var payload struct {
		Date     string `json:"capture_date" validate:"date"`
		URL      string `json:"server_url" validate:"url"`
		After    int    `json:"after" validate:"gt=1"`
		AtLeast  int    `json:"at_least" validate:"gte=2"`
		Before   int    `json:"before" validate:"lt=3"`
		AtMost   int    `json:"at_most" validate:"lte=4"`
		Code     string `json:"code" validate:"len=5"`
		End      int    `json:"end" validate:"gtfield=After"`
		Kind     string `json:"kind" validate:"oneof=internal_one internal_two"`
		NotEqual string `json:"not_equal" validate:"ne=internal_value"`
	}
	err := bind(t, http.MethodPost, "/", `{"capture_date":"2026-02-31","server_url":"no","after":1,"at_least":1,"before":3,"at_most":5,"not_equal":"internal_value"}`, echo.MIMEApplicationJSON, &payload, nil)
	var fields *errcodes.FieldError
	require.ErrorAs(t, err, &fields)
	assert.Equal(t, map[string]string{
		"capture_date": "Enter a valid date in YYYY-MM-DD format.",
		"server_url":   "Enter a valid web address.",
		"after":        "Enter a number greater than 1.",
		"at_least":     "Enter 2 or more.",
		"before":       "Enter a number less than 3.",
		"at_most":      "Enter 4 or less.",
		"code":         "Check this value.",
		"end":          "Check this value.",
		"kind":         "Check this value.",
		"not_equal":    "Check this value.",
	}, fields.Fields)
}

func TestBindValidation(t *testing.T) {
	t.Parallel()

	var payload struct {
		Date string `json:"date" validate:"required,date"`
		URL  string `json:"url" validate:"required,url"`
	}
	err := bind(t, http.MethodPost, "/", `{"date":"2026-02-31","url":"https://example.com"}`, echo.MIMEApplicationJSON, &payload, nil)
	assertAPIError(t, err, "validation_error")

	err = bind(t, http.MethodPost, "/", `{"date":"2026-10-10","url":"not-a-url"}`, echo.MIMEApplicationJSON, &payload, nil)
	assertAPIError(t, err, "validation_error")
}

func TestBindRejectsUnsupportedMediaType(t *testing.T) {
	t.Parallel()

	var payload struct{}
	err := bind(t, http.MethodPost, "/", "payload", echo.MIMETextPlain, &payload, nil)
	assertAPIError(t, err, "unsupported_media_type")
}

func bind(t *testing.T, method, target, body, contentType string, payload any, values map[string]any) error {
	t.Helper()

	requestBinder, err := New()
	require.NoError(t, err)
	e := echo.New()
	var requestBody *strings.Reader
	if body == "" {
		requestBody = strings.NewReader("")
	} else {
		requestBody = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, requestBody)
	if body == "" {
		req.Body = http.NoBody
		req.ContentLength = 0
	}
	if contentType != "" {
		req.Header.Set(echo.HeaderContentType, contentType)
	}
	ctx := e.NewContext(req, httptest.NewRecorder())
	for key, value := range values {
		ctx.Set(key, value)
	}
	return requestBinder.Bind(ctx, payload)
}

func assertAPIError(t *testing.T, err error, code string) {
	t.Helper()
	var apiErr *errcodes.Error
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, code, apiErr.Code)
}
