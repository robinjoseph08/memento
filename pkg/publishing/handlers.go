package publishing

import (
	"net/http"
	"strconv"

	"github.com/labstack/echo/v5"
	"github.com/robinjoseph08/memento/pkg/errorstack"
)

type handlers struct{ module *Module }

func respond(c *echo.Context, result any, err error) error {
	if err != nil {
		return err
	}
	return errorstack.CaptureContext(c.Request().Context(), c.JSON(http.StatusOK, result))
}
func (h *handlers) sources(c *echo.Context) error {
	page, _ := strconv.Atoi(c.QueryParam("page"))
	result, err := h.module.ListSources(c.Request().Context(), c.QueryParam("q"), page)
	return respond(c, result, err)
}
func (h *handlers) albums(c *echo.Context) error {
	result, err := h.module.ListAlbums(c.Request().Context(), c.QueryParam("q"))
	return respond(c, result, err)
}
func (h *handlers) album(c *echo.Context) error {
	result, err := h.module.GetAlbum(c.Request().Context(), c.Param("id"))
	return respond(c, result, err)
}
func (h *handlers) startImport(c *echo.Context) error {
	var request ImportRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.StartImport(c.Request().Context(), request.SourceID)
	return respond(c, result, err)
}
func (h *handlers) updateAlbum(c *echo.Context) error {
	var request UpdateAlbumRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.UpdateAlbum(c.Request().Context(), c.Param("id"), request)
	return respond(c, result, err)
}
func (h *handlers) retryImport(c *echo.Context) error {
	var request struct{}
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.RetryImport(c.Request().Context(), c.Param("id"))
	return respond(c, result, err)
}
