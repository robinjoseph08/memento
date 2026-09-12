package publishing

import (
	"context"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v5"
	"github.com/robinjoseph08/memento/pkg/errorstack"
)

// ViewerUseCases keeps HTTP identity selection separate from viewer projections.
type ViewerUseCases interface {
	ViewAlbum(context.Context, string, string, string) (ViewerAlbum, error)
	ViewAlbums(context.Context, string) ([]ViewerAlbum, error)
	ViewEntries(context.Context, string, string, string, string, string) (ViewerPage, error)
}

type viewerHandlers struct{ module ViewerUseCases }

func actorID(c *echo.Context) string {
	id, _ := c.Get("identity.person_id").(string)
	return id
}
func (h *viewerHandlers) album(c *echo.Context) error {
	result, err := h.module.ViewAlbum(c.Request().Context(), actorID(c), c.Param("personID"), c.Param("id"))
	return respond(c, result, err)
}
func (h *viewerHandlers) albums(c *echo.Context) error {
	result, err := h.module.ViewAlbums(c.Request().Context(), actorID(c))
	return respond(c, result, err)
}
func (h *viewerHandlers) photos(c *echo.Context) error { return h.entries(c, "IMAGE") }
func (h *viewerHandlers) videos(c *echo.Context) error { return h.entries(c, "VIDEO") }
func (h *viewerHandlers) entries(c *echo.Context, kind string) error {
	result, err := h.module.ViewEntries(c.Request().Context(), actorID(c), c.Param("personID"), c.Param("id"), kind, c.QueryParam("cursor"))
	return respond(c, result, err)
}

type handlers struct{ module *Module }

// PublicationUseCases is the HTTP seam for publication and permanent removal.
type PublicationUseCases interface {
	ReviewPublication(context.Context, string) (PublicationReview, error)
	PublishAlbum(context.Context, string, PublishRequest) (AlbumDetail, error)
	UnpublishAlbum(context.Context, string) (AlbumDetail, error)
	DeleteAlbum(context.Context, string, DeleteAlbumRequest) error
}

type publicationHandlers struct{ module PublicationUseCases }

func (h *publicationHandlers) publication(c *echo.Context) error {
	result, err := h.module.ReviewPublication(c.Request().Context(), c.Param("id"))
	return respond(c, result, err)
}
func (h *publicationHandlers) publish(c *echo.Context) error {
	var request PublishRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.PublishAlbum(c.Request().Context(), c.Param("id"), request)
	return respond(c, result, err)
}
func (h *publicationHandlers) deleteAlbum(c *echo.Context) error {
	var request DeleteAlbumRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	if err := h.module.DeleteAlbum(c.Request().Context(), c.Param("id"), request); err != nil {
		return err
	}
	return respond(c, struct{}{}, nil)
}

func (h *publicationHandlers) unpublish(c *echo.Context) error {
	var request struct{}
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.UnpublishAlbum(c.Request().Context(), c.Param("id"))
	return respond(c, result, err)
}

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

// AccessUseCases is the HTTP seam for complete Curator access operations.
//
//nolint:interfacebloat // One cohesive route group; splitting it would duplicate registration and test setup.
type AccessUseCases interface {
	SetAlbumAccess(context.Context, string, SetAlbumAccessRequest) (AccessResult, error)
	SetMomentAccess(context.Context, string, string, SetMomentAccessRequest) (MomentAccessResult, error)
	SetEntryAccess(context.Context, string, string, SetEntryAccessRequest) (AccessResult, error)
	AddMomentSuggestions(context.Context, string, string) (MomentAccessResult, error)
	UndoAlbumAccess(context.Context, string, UndoMomentAccessRequest) (AlbumDetail, error)
	UndoMomentAccess(context.Context, string, string, UndoMomentAccessRequest) (AlbumDetail, error)
	UndoEntryAccess(context.Context, string, string, UndoMomentAccessRequest) (AlbumDetail, error)
	SaveMomentRules(context.Context, string, string, SaveRulesRequest) (AlbumDetail, error)
	SaveEntryRules(context.Context, string, string, SaveRulesRequest) (AlbumDetail, error)
	PreviewRemoveAccess(context.Context, string, RemoveAccessPreviewRequest) (RemoveAccessPreview, error)
	RemoveAccess(context.Context, string, RemoveAccessRequest) (AlbumDetail, error)
}

type accessHandlers struct{ module AccessUseCases }

func (h *accessHandlers) setAlbumAccess(c *echo.Context) error {
	var request SetAlbumAccessRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.SetAlbumAccess(c.Request().Context(), c.Param("id"), request)
	return respond(c, result, err)
}

func (h *accessHandlers) setEntryAccess(c *echo.Context) error {
	var request SetEntryAccessRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.SetEntryAccess(c.Request().Context(), c.Param("id"), c.Param("entryID"), request)
	return respond(c, result, err)
}

func (h *accessHandlers) undoAlbumAccess(c *echo.Context) error {
	var request UndoMomentAccessRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.UndoAlbumAccess(c.Request().Context(), c.Param("id"), request)
	return respond(c, result, err)
}

func (h *accessHandlers) undoEntryAccess(c *echo.Context) error {
	var request UndoMomentAccessRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.UndoEntryAccess(c.Request().Context(), c.Param("id"), c.Param("entryID"), request)
	return respond(c, result, err)
}

func (h *accessHandlers) saveMomentRules(c *echo.Context) error {
	var request SaveRulesRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.SaveMomentRules(c.Request().Context(), c.Param("id"), c.Param("momentID"), request)
	return respond(c, result, err)
}

func (h *accessHandlers) saveEntryRules(c *echo.Context) error {
	var request SaveRulesRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.SaveEntryRules(c.Request().Context(), c.Param("id"), c.Param("entryID"), request)
	return respond(c, result, err)
}

func (h *accessHandlers) previewRemoveAccess(c *echo.Context) error {
	var request RemoveAccessPreviewRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.PreviewRemoveAccess(c.Request().Context(), c.Param("id"), request)
	return respond(c, result, err)
}

func (h *accessHandlers) removeAccess(c *echo.Context) error {
	var request RemoveAccessRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.RemoveAccess(c.Request().Context(), c.Param("id"), request)
	return respond(c, result, err)
}

func (h *accessHandlers) setMomentAccess(c *echo.Context) error {
	var request SetMomentAccessRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.SetMomentAccess(c.Request().Context(), c.Param("id"), c.Param("momentID"), request)
	return respond(c, result, err)
}

func (h *accessHandlers) addMomentSuggestions(c *echo.Context) error {
	var request struct{}
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.AddMomentSuggestions(c.Request().Context(), c.Param("id"), c.Param("momentID"))
	return respond(c, result, err)
}

func (h *accessHandlers) undoMomentAccess(c *echo.Context) error {
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
