package errcodes

import (
	"context"
	"errors"
	"net/http"

	"github.com/iancoleman/strcase"
	"github.com/labstack/echo/v5"
	echologger "github.com/robinjoseph08/golib/echo/v5/middleware/logger"
	"github.com/robinjoseph08/golib/errutils"
)

// Handler converts application and Echo errors into the API error envelope.
type Handler struct{}

func NewHandler() *Handler {
	return &Handler{}
}

// Handle is an Echo HTTP error handler.
func (h *Handler) Handle(c *echo.Context, err error) {
	if errutils.IsIgnorableErr(err) || errors.Is(err, context.Canceled) {
		return
	}
	if response, unwrapErr := echo.UnwrapResponse(c.Response()); unwrapErr == nil && response.Committed {
		return
	}

	httpCode, payload := h.generatePayload(err)
	if httpCode == http.StatusInternalServerError {
		echologger.FromEchoContext(c).Err(err).Error("server error")
	}

	if writeErr := c.JSON(httpCode, payload); writeErr != nil {
		echologger.FromEchoContext(c).Err(writeErr).Error("error handler json error")
	}
}

func (*Handler) generatePayload(err error) (int, ErrorResponse) {
	httpCode := http.StatusInternalServerError
	code := "internal_server_error"
	message := http.StatusText(httpCode)

	if statusCode := echo.StatusCode(err); statusCode != 0 {
		httpCode = statusCode
		message = http.StatusText(statusCode)
		code = strcase.ToSnake(message)
		if statusCode == http.StatusNotFound {
			message = "Page not found."
			code = "not_found"
		}
	}

	var echoErr *echo.HTTPError
	if errors.As(err, &echoErr) && echoErr.Message != "" {
		httpCode = echoErr.Code
		message = echoErr.Message
		code = strcase.ToSnake(message)
	}

	if applicationErr, ok := errors.AsType[*Error](err); ok {
		httpCode = applicationErr.HTTPCode
		message = applicationErr.Message
		code = applicationErr.Code
	}

	detail := ErrorDetail{Code: code, Message: message, StatusCode: httpCode}
	if fieldErr, ok := errors.AsType[*FieldError](err); ok {
		detail.Fields = fieldErr.Fields
	}
	return httpCode, ErrorResponse{Error: detail}
}
