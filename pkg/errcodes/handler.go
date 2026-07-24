package errcodes

import (
	"context"
	"errors"
	"net/http"

	"github.com/iancoleman/strcase"
	"github.com/labstack/echo/v4"
	goliblogger "github.com/robinjoseph08/golib/echo/v4/middleware/logger"
	"github.com/robinjoseph08/golib/errutils"
)

// Handler translates returned errors into the stable API error envelope.
type Handler struct{}

// NewHandler constructs an API error handler.
func NewHandler() *Handler {
	return &Handler{}
}

// Handle writes a safe API error response for Echo, errcodes, and unknown errors.
func (h *Handler) Handle(err error, c echo.Context) {
	if c.Response().Committed || errutils.IsIgnorableErr(err) || errors.Is(err, context.Canceled) {
		return
	}

	httpCode, payload := h.generatePayload(c, err)
	if httpCode == http.StatusInternalServerError {
		goliblogger.FromEchoContext(c).Error("server error")
	}
	if writeErr := c.JSON(httpCode, payload); writeErr != nil {
		goliblogger.FromEchoContext(c).Error("error handler json error")
	}
}

func (h *Handler) generatePayload(c echo.Context, err error) (int, map[string]any) {
	httpCode := http.StatusInternalServerError
	code := "internal_server_error"
	message := http.StatusText(http.StatusInternalServerError)
	var fieldErrors map[string]string

	var httpError *echo.HTTPError
	if errors.As(err, &httpError) && validHTTPErrorStatus(httpError.Code) {
		httpCode = httpError.Code
		message = http.StatusText(httpCode)
		code = strcase.ToSnake(message)
		if httpCode == http.StatusNotFound {
			message = "Page not found."
			code = "not_found"
		}
	}

	var codedError *Error
	if errors.As(err, &codedError) && validHTTPErrorStatus(codedError.HTTPCode) && codedError.Code != "" && codedError.Message != "" {
		httpCode = codedError.HTTPCode
		code = codedError.Code
		message = codedError.Message
		fieldErrors = codedError.FieldErrors
	}

	errorPayload := map[string]any{
		"code":        code,
		"message":     message,
		"status_code": httpCode,
	}
	if len(fieldErrors) != 0 {
		errorPayload["field_errors"] = fieldErrors
	}
	if requestID := goliblogger.IDFromEchoContext(c); requestID != "" {
		errorPayload["request_id"] = requestID
	}
	return httpCode, map[string]any{"error": errorPayload}
}

func validHTTPErrorStatus(status int) bool {
	return status >= http.StatusBadRequest && status <= 599 && http.StatusText(status) != ""
}
