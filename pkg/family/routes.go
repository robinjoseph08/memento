package family

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

// Handler exposes Curator-only Family graph operations.
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
			return setup.CuratorSession{}, errcodes.Forbidden("Changing Family relationships without a valid CSRF token")
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
	response, err := h.service.List(c.Request().Context(), includeArchived)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) Branch(c echo.Context) error {
	if _, err := h.authorize(c, false); err != nil {
		return err
	}
	personID, err := uuid.Parse(c.Param("person_id"))
	if err != nil {
		return errcodes.NotFound("Person")
	}
	branch, err := h.service.Branch(c.Request().Context(), personID)
	if err != nil {
		return familyError(err)
	}
	return c.JSON(http.StatusOK, branch)
}

func (h *Handler) Create(c echo.Context) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	var request MutationRequest
	if err := bindJSON(c, &request); err != nil {
		return err
	}
	relationship, err := h.service.Create(h.requestContext(c), actor, request)
	if err != nil {
		return familyError(err)
	}
	return c.JSON(http.StatusCreated, relationship)
}

func (h *Handler) Update(c echo.Context) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return errcodes.NotFound("Family relationship")
	}
	var request MutationRequest
	if err := bindJSON(c, &request); err != nil {
		return err
	}
	relationship, err := h.service.Update(h.requestContext(c), actor, id, request)
	if err != nil {
		return familyError(err)
	}
	return c.JSON(http.StatusOK, relationship)
}

func (h *Handler) Archive(c echo.Context) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return errcodes.NotFound("Family relationship")
	}
	var request VersionRequest
	if err := bindJSON(c, &request); err != nil {
		return err
	}
	relationship, err := h.service.Archive(h.requestContext(c), actor, id, request.Version)
	if err != nil {
		return familyError(err)
	}
	return c.JSON(http.StatusOK, relationship)
}

func familyError(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return errcodes.NotFound("Family relationship")
	case errors.Is(err, ErrPersonNotFound):
		return errcodes.NotFound("Person")
	case errors.Is(err, ErrPersonUnavailable):
		return errcodes.Conflict("Choose current People for Family relationships.")
	case errors.Is(err, ErrStale):
		return errcodes.Conflict("This Family relationship changed after it was loaded. Reload and try again.")
	case errors.Is(err, ErrDuplicate):
		return errcodes.Conflict("That active Family relationship already exists.")
	case errors.Is(err, ErrCycle):
		return errcodes.Conflict("That parent-child connection would create a cycle. The Family graph was not changed.")
	case errors.Is(err, ErrInvalid):
		return errcodes.ValidationError("Choose two different People, a valid connection type, and a current or former status for partner connections.")
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
	group := e.Group("/api/relationships", noStore)
	list := group.GET("", handler.List)
	list.Name = curatorReadPolicy
	branch := group.GET("/branches/:person_id", handler.Branch)
	branch.Name = curatorReadPolicy
	create := group.POST("", handler.Create)
	create.Name = curatorMutationPolicy
	update := group.PATCH("/:id", handler.Update)
	update.Name = curatorMutationPolicy
	archive := group.POST("/:id/archive", handler.Archive)
	archive.Name = curatorMutationPolicy
}
