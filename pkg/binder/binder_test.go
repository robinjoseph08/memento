package binder

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type bindPayload struct {
	Name  string `json:"name" form:"name" query:"name" mod:"trim" validate:"required,max=9"`
	Count int    `json:"count" form:"count" query:"count" default:"2" validate:"min=1"`
}

func newContext(method, payload, contentType string) echo.Context {
	e := echo.New()
	request := httptest.NewRequestWithContext(context.Background(), method, "/", strings.NewReader(payload))
	if contentType != "" {
		request.Header.Set(echo.HeaderContentType, contentType)
	}
	return e.NewContext(request, httptest.NewRecorder())
}

func requireErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	var codedError *errcodes.Error
	require.ErrorAs(t, err, &codedError)
	assert.Equal(t, code, codedError.Code)
}

func TestBindJSONNormalizesDefaultsAndValidates(t *testing.T) {
	binder, err := New()
	require.NoError(t, err)
	payload := bindPayload{}

	err = binder.Bind(&payload, newContext(http.MethodPost, `{"name":"  family  "}`, echo.MIMEApplicationJSON))

	require.NoError(t, err)
	assert.Equal(t, "family", payload.Name)
	assert.Equal(t, 2, payload.Count)
}

func TestBindParsesExactJSONMediaTypeWithParameters(t *testing.T) {
	binder, err := New()
	require.NoError(t, err)
	payload := bindPayload{}
	require.NoError(t, binder.Bind(&payload, newContext(http.MethodPost, `{"name":"family"}`, "application/json; charset=utf-8")))
	assert.Equal(t, "family", payload.Name)

	bindErr := binder.Bind(&bindPayload{}, newContext(http.MethodPost, `{"name":"family"}`, "application/jsonp"))
	requireErrorCode(t, bindErr, "unsupported_media_type")
}

func TestBindRejectsUnsafeJSONShapes(t *testing.T) {
	binder, err := New()
	require.NoError(t, err)
	tests := []struct {
		name string
		body string
		code string
	}{
		{"unknown field", `{"name":"family","private":"value"}`, "unknown_parameter"},
		{"wrong type", `{"name":123}`, "validation_type_error"},
		{"malformed", `{"name":`, "malformed_payload"},
		{"multiple documents", `{"name":"family"}{"name":"other"}`, "malformed_payload"},
		{"null", `null`, "malformed_payload"},
		{"whitespace", "  \n\t", "empty_request_body"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bindErr := binder.Bind(&bindPayload{}, newContext(http.MethodPost, test.body, echo.MIMEApplicationJSON))
			requireErrorCode(t, bindErr, test.code)
		})
	}
}

func TestBindCanAllowUnknownFieldsAndEmptyBodies(t *testing.T) {
	binder, err := New()
	require.NoError(t, err)
	jsonContext := newContext(http.MethodPost, `{"name":"family","future":"value"}`, echo.MIMEApplicationJSON)
	jsonContext.Set("disallow_unknown_fields", false)
	payload := bindPayload{}
	require.NoError(t, binder.Bind(&payload, jsonContext))
	assert.Equal(t, "family", payload.Name)

	emptyContext := newContext(http.MethodPost, "", "")
	emptyContext.Set("disallow_empty_body", false)
	require.NoError(t, binder.Bind(&struct{}{}, emptyContext))
}

func TestBindRejectsUnsupportedAndEmptyMutationBodies(t *testing.T) {
	binder, err := New()
	require.NoError(t, err)

	requireErrorCode(t, binder.Bind(&bindPayload{}, newContext(http.MethodPost, "value", echo.MIMETextPlain)), "unsupported_media_type")
	requireErrorCode(t, binder.Bind(&bindPayload{}, newContext(http.MethodPost, "", "")), "empty_request_body")

	unknownLengthEmpty := newContext(http.MethodPost, "", echo.MIMEApplicationJSON)
	unknownLengthEmpty.Request().Body = io.NopCloser(strings.NewReader(""))
	unknownLengthEmpty.Request().ContentLength = -1
	requireErrorCode(t, binder.Bind(&bindPayload{}, unknownLengthEmpty), "empty_request_body")
}

func TestBindQueryAndFormParameters(t *testing.T) {
	binder, err := New()
	require.NoError(t, err)

	queryContext := newContext(http.MethodGet, "", "")
	queryContext.SetRequest(queryContext.Request().WithContext(context.Background()))
	queryContext.Request().URL.RawQuery = "name=%20family%20&count=3"
	queryPayload := bindPayload{}
	require.NoError(t, binder.Bind(&queryPayload, queryContext))
	assert.Equal(t, bindPayload{Name: "family", Count: 3}, queryPayload)

	form := url.Values{"name": {" household "}, "count": {"4"}}
	formPayload := bindPayload{}
	require.NoError(t, binder.Bind(&formPayload, newContext(http.MethodPost, form.Encode(), echo.MIMEApplicationForm)))
	assert.Equal(t, bindPayload{Name: "household", Count: 4}, formPayload)
}

func TestBindUsesDeterministicParameterErrors(t *testing.T) {
	binder, err := New()
	require.NoError(t, err)
	for range 20 {
		requestContext := newContext(http.MethodGet, "", "")
		requestContext.Request().URL.RawQuery = "count=wrong&a_unknown=value"
		bindErr := binder.Bind(&bindPayload{}, requestContext)
		var codedError *errcodes.Error
		require.ErrorAs(t, bindErr, &codedError)
		assert.Equal(t, "unknown_parameter", codedError.Code)
		assert.Equal(t, `Unknown parameter "a_unknown".`, codedError.Message)
	}
}

func TestBindMultipartFiles(t *testing.T) {
	binder, err := New()
	require.NoError(t, err)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("name", "family"))
	file, err := writer.CreateFormFile("photo", "photo.jpg")
	require.NoError(t, err)
	_, err = file.Write([]byte("photo"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	type multipartPayload struct {
		Name      string                           `form:"name" validate:"required"`
		FormFiles map[string]*multipart.FileHeader `json:"-"`
	}
	payload := multipartPayload{}
	require.NoError(t, binder.Bind(&payload, newContext(http.MethodPost, body.String(), writer.FormDataContentType())))
	assert.Equal(t, "family", payload.Name)
	require.Contains(t, payload.FormFiles, "photo")
	assert.Equal(t, "photo.jpg", payload.FormFiles["photo"].Filename)
}

func TestBindRejectsInvalidMultipartTargets(t *testing.T) {
	binder, err := New()
	require.NoError(t, err)
	multipartContext := func() echo.Context {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		file, createErr := writer.CreateFormFile("photo", "photo.jpg")
		require.NoError(t, createErr)
		_, writeErr := file.Write([]byte("photo"))
		require.NoError(t, writeErr)
		require.NoError(t, writer.Close())
		return newContext(http.MethodPost, body.String(), writer.FormDataContentType())
	}

	tests := []struct {
		name   string
		target any
	}{
		{"nil target", (*struct{})(nil)},
		{"non-map field", &struct{ FormFiles string }{}},
		{"wrong map type", &struct{ FormFiles map[string]string }{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Error(t, binder.Bind(test.target, multipartContext()))
		})
	}
}

func TestBindAppliesNestedModifiersWithDive(t *testing.T) {
	binder, err := New()
	require.NoError(t, err)
	type item struct {
		Value string `json:"value" mod:"trim"`
	}
	type payload struct {
		Items []item `json:"items" mod:"dive"`
	}
	result := payload{}

	require.NoError(t, binder.Bind(&result, newContext(http.MethodPost, `{"items":[{"value":"  photo  "}]}`, echo.MIMEApplicationJSON)))
	require.Len(t, result.Items, 1)
	assert.Equal(t, "photo", result.Items[0].Value)
}

func TestBindFormatsValidationErrors(t *testing.T) {
	binder, err := New()
	require.NoError(t, err)
	type payload struct {
		Email string `json:"email" validate:"required,email"`
		Date  string `json:"date" validate:"date"`
		URL   string `json:"url" validate:"url"`
	}
	tests := []struct {
		name    string
		body    string
		message string
	}{
		{"required", `{"date":"","url":""}`, `"email" is required`},
		{"email", `{"email":"wrong","date":"","url":""}`, `"email" is not a valid email`},
		{"date", `{"email":"a@example.com","date":"2026-02-31","url":""}`, `"date" should be in the format YYYY-MM-DD`},
		{"url", `{"email":"a@example.com","date":"","url":"relative"}`, `"url" is not a valid URL`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bindErr := binder.Bind(&payload{}, newContext(http.MethodPost, test.body, echo.MIMEApplicationJSON))
			var codedError *errcodes.Error
			require.ErrorAs(t, bindErr, &codedError)
			assert.Equal(t, "validation_error", codedError.Code)
			assert.Equal(t, test.message, codedError.Message)
		})
	}
}
