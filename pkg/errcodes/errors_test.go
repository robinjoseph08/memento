package errcodes

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestErrorConstructors(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err      error
		httpCode int
		code     string
		message  string
	}{
		"not found":              {NotFound("Page"), http.StatusNotFound, "not_found", "Page not found."},
		"unsupported media type": {UnsupportedMediaType(), http.StatusUnsupportedMediaType, "unsupported_media_type", "Unsupported Media Type"},
		"unknown parameter":      {UnknownParameter("extra"), http.StatusUnprocessableEntity, "unknown_parameter", `Unknown Parameter "extra"`},
		"validation type":        {ValidationTypeError("wrong type"), http.StatusUnprocessableEntity, "validation_type_error", "wrong type"},
		"validation":             {ValidationError("invalid"), http.StatusUnprocessableEntity, "validation_error", "invalid"},
		"malformed":              {MalformedPayload(), http.StatusBadRequest, "malformed_payload", "Malformed Payload"},
		"empty body":             {EmptyRequestBody(), http.StatusBadRequest, "empty_request_body", "Request body can't be empty."},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var apiErr *Error
			require.ErrorAs(t, test.err, &apiErr)
			assert.Equal(t, test.httpCode, apiErr.HTTPCode)
			assert.Equal(t, test.code, apiErr.Code)
			assert.Equal(t, test.message, apiErr.Message)
			assert.Equal(t, test.message, apiErr.Error())
			assert.ErrorIs(t, test.err, &Error{test.httpCode, test.message, test.code})
		})
	}
}
