package visibility

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
	curatorReadPolicy        = "policy:curator_visibility"
	curatorMutationPolicy    = "policy:curator_visibility_csrf"
	recipientDiscoveryPolicy = "policy:recipient_discovery"
	recipientInterestPolicy  = "policy:recipient_self_interest"
	recipientMutationPolicy  = "policy:recipient_self_interest_csrf"
)

// Authorizer authenticates both Curator and Recipient sessions.
type Authorizer interface {
	AuthorizeSession(ctx context.Context, credential, csrfToken string, mutation bool) (setup.SessionActor, error)
}

type Handler struct {
	service    *Service
	authorizer Authorizer
}

func NewHandler(service *Service, authorizer Authorizer) *Handler {
	return &Handler{service: service, authorizer: authorizer}
}

func (h *Handler) authorize(c echo.Context, mutation, curatorOnly bool) (setup.SessionActor, error) {
	cookie, err := c.Cookie(setup.CookieName)
	if err != nil || cookie.Value == "" {
		return setup.SessionActor{}, errcodes.Unauthorized("A valid Session is required.")
	}
	actor, err := h.authorizer.AuthorizeSession(c.Request().Context(), cookie.Value, c.Request().Header.Get(setup.CSRFHeader), mutation)
	if err != nil {
		switch {
		case errors.Is(err, setup.ErrUnauthenticated):
			return setup.SessionActor{}, errcodes.Unauthorized("A valid Session is required.")
		case errors.Is(err, setup.ErrCSRF):
			return setup.SessionActor{}, errcodes.Forbidden("Changing Visibility circles or an Interest list without a valid CSRF token")
		default:
			return setup.SessionActor{}, err
		}
	}
	if curatorOnly && !actor.Curator {
		return setup.SessionActor{}, errcodes.NotFound("Page")
	}
	return actor, nil
}

func (h *Handler) requestContext(c echo.Context) context.Context {
	if service, ok := h.authorizer.(*setup.Service); ok {
		return service.ContextWithRequestMetadata(c.Request().Context(), c.Request())
	}
	return c.Request().Context()
}

func (h *Handler) ListCircles(c echo.Context) error {
	actor, err := h.authorize(c, false, true)
	if err != nil {
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
	response, err := h.service.ListCircles(c.Request().Context(), actor, includeArchived)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) CreateCircle(c echo.Context) error {
	actor, err := h.authorize(c, true, true)
	if err != nil {
		return err
	}
	var request CircleRequest
	if err := bindJSON(c, &request); err != nil {
		return err
	}
	circle, err := h.service.CreateCircle(h.requestContext(c), actor, request)
	if err != nil {
		return visibilityError(err)
	}
	return c.JSON(http.StatusCreated, circle)
}

func (h *Handler) UpdateCircle(c echo.Context) error {
	actor, err := h.authorize(c, true, true)
	if err != nil {
		return err
	}
	circleID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return errcodes.NotFound("Visibility circle")
	}
	var request CircleRequest
	if err := bindJSON(c, &request); err != nil {
		return err
	}
	circle, err := h.service.UpdateCircle(h.requestContext(c), actor, circleID, request)
	if err != nil {
		return visibilityError(err)
	}
	return c.JSON(http.StatusOK, circle)
}

func (h *Handler) ArchiveCircle(c echo.Context) error {
	actor, err := h.authorize(c, true, true)
	if err != nil {
		return err
	}
	circleID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return errcodes.NotFound("Visibility circle")
	}
	var request CircleVersionRequest
	if err := bindJSON(c, &request); err != nil {
		return err
	}
	circle, err := h.service.ArchiveCircle(h.requestContext(c), actor, circleID, request.Version)
	if err != nil {
		return visibilityError(err)
	}
	return c.JSON(http.StatusOK, circle)
}

func (h *Handler) SetMembership(c echo.Context) error {
	actor, err := h.authorize(c, true, true)
	if err != nil {
		return err
	}
	circleID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return errcodes.NotFound("Visibility circle")
	}
	personID, err := uuid.Parse(c.Param("person_id"))
	if err != nil {
		return errcodes.NotFound("Person")
	}
	var request MembershipRequest
	if err := bindJSON(c, &request); err != nil {
		return err
	}
	if request.Included == nil {
		return errcodes.ValidationError("included is required.")
	}
	circle, err := h.service.SetMembership(h.requestContext(c), actor, circleID, personID, request)
	if err != nil {
		return visibilityError(err)
	}
	return c.JSON(http.StatusOK, circle)
}

func (h *Handler) CuratorDiscover(c echo.Context) error {
	actor, err := h.authorize(c, false, true)
	if err != nil {
		return err
	}
	recipientID, err := uuid.Parse(c.Param("recipient_id"))
	if err != nil {
		return errcodes.NotFound("Recipient")
	}
	page, err := discoveryPage(c)
	if err != nil {
		return err
	}
	response, err := h.service.Discover(c.Request().Context(), actor, recipientID, page)
	if err != nil {
		return visibilityError(err)
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) CuratorInterestList(c echo.Context) error {
	actor, err := h.authorize(c, false, true)
	if err != nil {
		return err
	}
	recipientID, err := uuid.Parse(c.Param("recipient_id"))
	if err != nil {
		return errcodes.NotFound("Recipient")
	}
	page, err := historyPage(c)
	if err != nil {
		return err
	}
	response, err := h.service.InterestList(c.Request().Context(), actor, recipientID, page)
	if err != nil {
		return visibilityError(err)
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) CuratorMutateInterest(c echo.Context) error {
	actor, err := h.authorize(c, true, true)
	if err != nil {
		return err
	}
	return h.mutateInterest(c, actor, c.Param("recipient_id"))
}

func (h *Handler) DiscoverSelf(c echo.Context) error {
	actor, err := h.authorize(c, false, false)
	if err != nil {
		return err
	}
	page, err := discoveryPage(c)
	if err != nil {
		return err
	}
	response, err := h.service.Discover(c.Request().Context(), actor, actor.PersonID, page)
	if err != nil {
		return visibilityError(err)
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) InterestListSelf(c echo.Context) error {
	actor, err := h.authorize(c, false, false)
	if err != nil {
		return err
	}
	page, err := historyPage(c)
	if err != nil {
		return err
	}
	response, err := h.service.InterestList(c.Request().Context(), actor, actor.PersonID, page)
	if err != nil {
		return visibilityError(err)
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) MutateInterestSelf(c echo.Context) error {
	actor, err := h.authorize(c, true, false)
	if err != nil {
		return err
	}
	return h.mutateInterest(c, actor, actor.PersonID.String())
}

func (h *Handler) mutateInterest(c echo.Context, actor setup.SessionActor, recipientValue string) error {
	recipientID, err := uuid.Parse(recipientValue)
	if err != nil {
		return errcodes.NotFound("Recipient")
	}
	selectedID, err := uuid.Parse(c.Param("person_id"))
	if err != nil {
		return errcodes.NotFound("Person")
	}
	var request InterestMutationRequest
	if err := bindJSON(c, &request); err != nil {
		return err
	}
	if request.Selected == nil {
		return errcodes.ValidationError("selected is required.")
	}
	response, err := h.service.MutateInterest(h.requestContext(c), actor, recipientID, selectedID, *request.Selected)
	if err != nil {
		return visibilityError(err)
	}
	return c.JSON(http.StatusOK, response)
}

func discoveryPage(c echo.Context) (DiscoveryPageRequest, error) {
	page := DiscoveryPageRequest{Cursor: c.QueryParam("cursor"), Limit: 100}
	if value := c.QueryParam("limit"); value != "" {
		limit, err := strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 200 {
			return DiscoveryPageRequest{}, errcodes.ValidationError("limit must be between 1 and 200.")
		}
		page.Limit = limit
	}
	return page, nil
}

func historyPage(c echo.Context) (HistoryPageRequest, error) {
	page := HistoryPageRequest{Cursor: c.QueryParam("history_cursor"), Limit: 50}
	if value := c.QueryParam("history_limit"); value != "" {
		limit, err := strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 200 {
			return HistoryPageRequest{}, errcodes.ValidationError("history_limit must be between 1 and 200.")
		}
		page.Limit = limit
	}
	return page, nil
}

func visibilityError(err error) error {
	switch {
	case errors.Is(err, ErrCircleNotFound):
		return errcodes.NotFound("Visibility circle")
	case errors.Is(err, ErrPersonNotFound):
		return errcodes.NotFound("Person")
	case errors.Is(err, ErrPersonUnavailable):
		return errcodes.Conflict("Choose a current Person.")
	case errors.Is(err, ErrRecipientRequired):
		return errcodes.Conflict("Choose a Person with Recipient access for this Interest list.")
	case errors.Is(err, ErrNotAuthorized):
		return errcodes.NotFound("Visibility resource")
	case errors.Is(err, ErrSelfSelection):
		return errcodes.ValidationError("A Recipient cannot add their own Person to their Interest list.")
	case errors.Is(err, ErrNotDiscoverable):
		return errcodes.Forbidden("That Person is not currently discoverable through a shared Visibility circle.")
	case errors.Is(err, ErrDuplicateName):
		return errcodes.Conflict("An active Visibility circle already uses that name.")
	case errors.Is(err, ErrStale):
		return errcodes.Conflict("This Visibility circle changed after it was loaded. Reload and try again.")
	case errors.Is(err, ErrInvalidCursor):
		return errcodes.ValidationError("The People or Interest history cursor is invalid. Reload and try again.")
	case errors.Is(err, ErrInvalid):
		return errcodes.ValidationError("Enter valid Visibility fields and a current version.")
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

// RegisterRoutes keeps discovery and Interest-list policies separate from every content policy.
func RegisterRoutes(e *echo.Echo, handler *Handler) {
	circles := e.Group("/api/visibility-circles", noStore)
	list := circles.GET("", handler.ListCircles)
	list.Name = curatorReadPolicy
	create := circles.POST("", handler.CreateCircle)
	create.Name = curatorMutationPolicy
	update := circles.PATCH("/:id", handler.UpdateCircle)
	update.Name = curatorMutationPolicy
	archive := circles.POST("/:id/archive", handler.ArchiveCircle)
	archive.Name = curatorMutationPolicy
	membership := circles.PUT("/:id/members/:person_id", handler.SetMembership)
	membership.Name = curatorMutationPolicy

	interest := e.Group("/api/interest-lists", noStore)
	curatorDiscovery := interest.GET("/:recipient_id/discoverable", handler.CuratorDiscover)
	curatorDiscovery.Name = curatorReadPolicy
	curatorList := interest.GET("/:recipient_id", handler.CuratorInterestList)
	curatorList.Name = curatorReadPolicy
	curatorMutation := interest.PUT("/:recipient_id/people/:person_id", handler.CuratorMutateInterest)
	curatorMutation.Name = curatorMutationPolicy

	me := e.Group("/api/me", noStore)
	discoverSelf := me.GET("/people", handler.DiscoverSelf)
	discoverSelf.Name = recipientDiscoveryPolicy
	listSelf := me.GET("/interest-list", handler.InterestListSelf)
	listSelf.Name = recipientInterestPolicy
	mutateSelf := me.PUT("/interest-list/:person_id", handler.MutateInterestSelf)
	mutateSelf.Name = recipientMutationPolicy
}
