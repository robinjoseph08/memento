package dashboard

import (
	"context"
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/robinjoseph08/memento/pkg/errorstack"
)

// UseCases is the HTTP seam.
type UseCases interface {
	Overview(context.Context) (Dashboard, error)
}

type handlers struct{ module UseCases }

func (h *handlers) overview(c *echo.Context) error {
	result, err := h.module.Overview(c.Request().Context())
	if err != nil {
		return err
	}
	return errorstack.CaptureContext(c.Request().Context(), c.JSON(http.StatusOK, result))
}
