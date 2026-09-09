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
func (h *handlers) updateMoment(c *echo.Context) error {
	var request UpdateMomentRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.UpdateMoment(c.Request().Context(), c.Param("id"), c.Param("momentID"), request)
	return respond(c, result, err)
}

func (h *handlers) setMomentCover(c *echo.Context) error {
	var request SetMomentCoverRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.SetMomentCover(c.Request().Context(), c.Param("id"), c.Param("momentID"), request)
	return respond(c, result, err)
}

func (h *handlers) refreshMomentFaces(c *echo.Context) error {
	var request struct{}
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.RefreshMomentFaces(c.Request().Context(), c.Param("id"), c.Param("momentID"))
	return respond(c, result, err)
}

func (h *handlers) setMomentAccess(c *echo.Context) error {
	var request SetMomentAccessRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.SetMomentAccess(c.Request().Context(), c.Param("id"), c.Param("momentID"), request)
	return respond(c, result, err)
}

func (h *handlers) addMomentSuggestions(c *echo.Context) error {
	var request struct{}
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.AddMomentSuggestions(c.Request().Context(), c.Param("id"), c.Param("momentID"))
	return respond(c, result, err)
}

func (h *handlers) undoMomentAccess(c *echo.Context) error {
	var request UndoMomentAccessRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.UndoMomentAccess(c.Request().Context(), c.Param("id"), c.Param("momentID"), request)
	return respond(c, result, err)
}

func (h *handlers) previewMove(c *echo.Context) error {
	var request MoveEntriesRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.PreviewMove(c.Request().Context(), c.Param("id"), c.Param("momentID"), request)
	return respond(c, result, err)
}

func (h *handlers) moveEntries(c *echo.Context) error {
	var request MoveEntriesRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.MoveEntries(c.Request().Context(), c.Param("id"), c.Param("momentID"), request)
	return respond(c, result, err)
}

func (h *handlers) previewSplit(c *echo.Context) error {
	var request SplitMomentRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.PreviewSplit(c.Request().Context(), c.Param("id"), c.Param("momentID"), request)
	return respond(c, result, err)
}

func (h *handlers) splitMoment(c *echo.Context) error {
	var request SplitMomentRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.SplitMoment(c.Request().Context(), c.Param("id"), c.Param("momentID"), request)
	return respond(c, result, err)
}

func (h *handlers) previewMerge(c *echo.Context) error {
	var request MergeMomentsRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.PreviewMerge(c.Request().Context(), c.Param("id"), c.Param("momentID"), request)
	return respond(c, result, err)
}

func (h *handlers) mergeMoments(c *echo.Context) error {
	var request MergeMomentsRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.MergeMoments(c.Request().Context(), c.Param("id"), c.Param("momentID"), request)
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
