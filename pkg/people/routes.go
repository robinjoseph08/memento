package people

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/setup"
)

const (
	curatorReadPolicy     = "policy:curator"
	curatorMutationPolicy = "policy:curator_csrf"
)

// Handler exposes the Curator's durable People directory.
type Handler struct {
	service *Service
	auth    *setup.Service
}

func NewHandler(service *Service, auth *setup.Service) *Handler {
	return &Handler{service: service, auth: auth}
}

func (h *Handler) requestContext(c echo.Context) context.Context {
	return h.auth.ContextWithRequestMetadata(c.Request().Context(), c.Request())
}

func (h *Handler) authorize(c echo.Context, mutation bool) (setup.CuratorSession, error) {
	cookie, err := c.Cookie(setup.CookieName)
	if err != nil || cookie.Value == "" {
		return setup.CuratorSession{}, errcodes.Unauthorized("A valid Session is required.")
	}
	actor, err := h.auth.AuthorizeCurator(c.Request().Context(), cookie.Value, c.Request().Header.Get(setup.CSRFHeader), mutation)
	if err != nil {
		switch {
		case errors.Is(err, setup.ErrUnauthenticated):
			return setup.CuratorSession{}, errcodes.Unauthorized("A valid Session is required.")
		case errors.Is(err, setup.ErrCSRF):
			return setup.CuratorSession{}, errcodes.Forbidden("Changing People without a valid CSRF token")
		case errors.Is(err, setup.ErrNotCurator):
			return setup.CuratorSession{}, errcodes.NotFound("Page")
		default:
			return setup.CuratorSession{}, err
		}
	}
	return actor, nil
}

func (h *Handler) List(c echo.Context) error {
	if _, err := h.authorize(c, false); err != nil {
		return err
	}
	includeArchived := false
	if value := c.QueryParam("include_archived"); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return errcodes.ValidationError("include_archived must be true or false.")
		}
		includeArchived = parsed
	}
	response, err := h.service.List(c.Request().Context(), c.QueryParam("query"), includeArchived)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) Get(c echo.Context) error {
	if _, err := h.authorize(c, false); err != nil {
		return err
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return errcodes.NotFound("Person")
	}
	person, err := h.service.Get(c.Request().Context(), id)
	if err != nil {
		return peopleError(err)
	}
	return c.JSON(http.StatusOK, person)
}

func (h *Handler) Create(c echo.Context) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	var request CreateRequest
	if err := bindJSON(c, &request); err != nil {
		return err
	}
	person, err := h.service.Create(h.requestContext(c), actor, request)
	if err != nil {
		return peopleError(err)
	}
	return c.JSON(http.StatusCreated, person)
}

func (h *Handler) Update(c echo.Context) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return errcodes.NotFound("Person")
	}
	var request UpdateRequest
	if err := bindJSON(c, &request); err != nil {
		return err
	}
	person, err := h.service.Update(h.requestContext(c), actor, id, request)
	if err != nil {
		return peopleError(err)
	}
	return c.JSON(http.StatusOK, person)
}

func (h *Handler) Archive(c echo.Context) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return errcodes.NotFound("Person")
	}
	var request VersionRequest
	if err := bindJSON(c, &request); err != nil {
		return err
	}
	person, err := h.service.Archive(h.requestContext(c), actor, id, request.Version)
	if err != nil {
		return peopleError(err)
	}
	return c.JSON(http.StatusOK, person)
}

func (h *Handler) PreviewMerge(c echo.Context) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	var request MergePreviewRequest
	if err := bindJSON(c, &request); err != nil {
		return err
	}
	sourceID, sourceErr := uuid.Parse(request.SourcePersonID)
	survivorID, survivorErr := uuid.Parse(request.SurvivorPersonID)
	if sourceErr != nil || survivorErr != nil {
		return errcodes.ValidationError("Choose two valid People.")
	}
	preview, err := h.service.PreviewMerge(h.requestContext(c), actor, sourceID, survivorID)
	if err != nil {
		return peopleError(err)
	}
	return c.JSON(http.StatusOK, preview)
}

func (h *Handler) Merge(c echo.Context) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	var request MergeRequest
	if err := bindJSON(c, &request); err != nil {
		return err
	}
	person, err := h.service.Merge(h.requestContext(c), actor, request)
	if err != nil {
		return peopleError(err)
	}
	return c.JSON(http.StatusOK, person)
}

func peopleError(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return errcodes.NotFound("Person")
	case errors.Is(err, ErrMergeStale):
		return errcodes.Conflict("A Person or affected reference changed after this merge was previewed. Preview the merge again.")
	case errors.Is(err, ErrStale):
		return errcodes.Conflict("This Person changed after it was loaded. Reload and try again.")
	case errors.Is(err, ErrCuratorMustSurvive):
		return errcodes.Conflict("The Curator Person must be the merge survivor.")
	case errors.Is(err, ErrSurvivorMustBeCurrent):
		return errcodes.Conflict("Choose a current Person as the merge survivor.")
	case errors.Is(err, ErrTwoCurrentGenerations):
		return errcodes.Conflict("Resolve one current Recipient access generation before merging.")
	case errors.Is(err, ErrGenerationTransferNeeded):
		return errcodes.Conflict("Explicitly transfer the source current Recipient access generation.")
	case errors.Is(err, ErrEmailResolutionNeeded):
		return errcodes.Conflict("Choose which login email survives before merging.")
	case errors.Is(err, ErrFamilyMergeCycle):
		return errcodes.Conflict("Resolve the parent-child path between these People before merging them.")
	case errors.Is(err, ErrFamilyPartnerConflict):
		return errcodes.Conflict("Resolve conflicting current and former partner relationships before merging these People.")
	case errors.Is(err, ErrInvalidPerson), errors.Is(err, ErrInvalidMerge):
		return errcodes.ValidationError("Enter valid Person details.")
	default:
		return err
	}
}

func bindJSON(c echo.Context, target any) error {
	contentType, _, err := mime.ParseMediaType(c.Request().Header.Get(echo.HeaderContentType))
	if err != nil || contentType != echo.MIMEApplicationJSON {
		return errcodes.UnsupportedMediaType()
	}
	return c.Bind(target)
}

func noStore(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		return next(c)
	}
}

// RegisterRoutes registers explicit Curator read and CSRF-protected mutation policies.
func RegisterRoutes(e *echo.Echo, handler *Handler) {
	group := e.Group("/api/people", noStore)
	list := group.GET("", handler.List)
	list.Name = curatorReadPolicy
	get := group.GET("/:id", handler.Get)
	get.Name = curatorReadPolicy
	create := group.POST("", handler.Create)
	create.Name = curatorMutationPolicy
	update := group.PATCH("/:id", handler.Update)
	update.Name = curatorMutationPolicy
	archive := group.POST("/:id/archive", handler.Archive)
	archive.Name = curatorMutationPolicy
	preview := group.POST("/merge-preview", handler.PreviewMerge)
	preview.Name = curatorMutationPolicy
	merge := group.POST("/merge", handler.Merge)
	merge.Name = curatorMutationPolicy
}
