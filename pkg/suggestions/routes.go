package suggestions

import (
	"context"
	"errors"
	"mime"
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/people"
	"github.com/robinjoseph08/memento/pkg/setup"
)

const (
	recipientReadPolicy     = "policy:recipient_self_suggestions"
	recipientMutationPolicy = "policy:recipient_self_suggestions_csrf"
	curatorReadPolicy       = "policy:curator_suggestions"
	curatorMutationPolicy   = "policy:curator_suggestions_csrf"
)

// Authorizer authenticates completed Recipient and Curator sessions.
type Authorizer interface {
	AuthorizeSession(ctx context.Context, credential, csrfToken string, mutation bool) (setup.SessionActor, error)
	AuthorizeCurator(ctx context.Context, credential, csrfToken string, mutation bool) (setup.CuratorSession, error)
}

// Handler keeps requester-safe and Curator-only representations on separate routes.
type Handler struct {
	service    *Service
	authorizer Authorizer
}

func NewHandler(service *Service, authorizer Authorizer) *Handler {
	return &Handler{service: service, authorizer: authorizer}
}

func (h *Handler) requestContext(c echo.Context) context.Context {
	if service, ok := h.authorizer.(*setup.Service); ok {
		return service.ContextWithRequestMetadata(c.Request().Context(), c.Request())
	}
	return c.Request().Context()
}

func (h *Handler) authorizeRecipient(c echo.Context, mutation bool) (setup.SessionActor, error) {
	cookie, err := c.Cookie(setup.CookieName)
	if err != nil || cookie.Value == "" {
		return setup.SessionActor{}, errcodes.Unauthorized("A valid Session is required.")
	}
	actor, err := h.authorizer.AuthorizeSession(c.Request().Context(), cookie.Value, c.Request().Header.Get(setup.CSRFHeader), mutation)
	if err != nil {
		switch {
		case errors.Is(err, setup.ErrUnauthenticated):
			return setup.SessionActor{}, errcodes.Unauthorized("A valid completed Recipient Session is required.")
		case errors.Is(err, setup.ErrCSRF):
			return setup.SessionActor{}, errcodes.Forbidden("Changing an Invitation suggestion without a valid CSRF token")
		default:
			return setup.SessionActor{}, err
		}
	}
	return actor, nil
}

func (h *Handler) authorizeCurator(c echo.Context, mutation bool) (setup.CuratorSession, error) {
	cookie, err := c.Cookie(setup.CookieName)
	if err != nil || cookie.Value == "" {
		return setup.CuratorSession{}, errcodes.Unauthorized("A valid Session is required.")
	}
	actor, err := h.authorizer.AuthorizeCurator(c.Request().Context(), cookie.Value, c.Request().Header.Get(setup.CSRFHeader), mutation)
	if err != nil {
		switch {
		case errors.Is(err, setup.ErrUnauthenticated):
			return setup.CuratorSession{}, errcodes.Unauthorized("A valid Session is required.")
		case errors.Is(err, setup.ErrCSRF):
			return setup.CuratorSession{}, errcodes.Forbidden("Resolving an Invitation suggestion without a valid CSRF token")
		case errors.Is(err, setup.ErrNotCurator):
			return setup.CuratorSession{}, errcodes.NotFound("Page")
		default:
			return setup.CuratorSession{}, err
		}
	}
	return actor, nil
}

func (h *Handler) ListRequester(c echo.Context) error {
	actor, err := h.authorizeRecipient(c, false)
	if err != nil {
		return err
	}
	response, err := h.service.ListRequester(c.Request().Context(), actor)
	if err != nil {
		return suggestionError(err)
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) Submit(c echo.Context) error {
	actor, err := h.authorizeRecipient(c, true)
	if err != nil {
		return err
	}
	var request SubmitRequest
	if err := bindJSON(c, &request); err != nil {
		return err
	}
	response, err := h.service.Submit(h.requestContext(c), actor, request)
	if err != nil {
		return suggestionError(err)
	}
	return c.JSON(http.StatusCreated, response)
}

func suggestionID(c echo.Context) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil || id == uuid.Nil {
		return uuid.Nil, errcodes.NotFound("Invitation suggestion")
	}
	return id, nil
}

func (h *Handler) Withdraw(c echo.Context) error {
	actor, err := h.authorizeRecipient(c, true)
	if err != nil {
		return err
	}
	id, err := suggestionID(c)
	if err != nil {
		return err
	}
	response, err := h.service.Withdraw(h.requestContext(c), actor, id)
	if err != nil {
		return suggestionError(err)
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) ListCurator(c echo.Context) error {
	if _, err := h.authorizeCurator(c, false); err != nil {
		return err
	}
	response, err := h.service.ListCurator(c.Request().Context(), c.QueryParam("status"))
	if err != nil {
		return suggestionError(err)
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) Reject(c echo.Context) error {
	actor, err := h.authorizeCurator(c, true)
	if err != nil {
		return err
	}
	id, err := suggestionID(c)
	if err != nil {
		return err
	}
	response, err := h.service.Reject(h.requestContext(c), actor, id)
	if err != nil {
		return suggestionError(err)
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) Accept(c echo.Context) error {
	actor, err := h.authorizeCurator(c, true)
	if err != nil {
		return err
	}
	id, err := suggestionID(c)
	if err != nil {
		return err
	}
	var request AcceptRequest
	if err := bindJSON(c, &request); err != nil {
		return err
	}
	if (request.PersonID == "") == (request.CreatePerson == nil) {
		return suggestionError(ErrInvalidResolution)
	}
	var response CuratorSuggestion
	if request.CreatePerson != nil {
		response, err = h.service.AcceptNew(h.requestContext(c), actor, id, people.CreateRequest{
			DisplayName: request.CreatePerson.DisplayName,
			SortName:    request.CreatePerson.SortName,
		})
	} else {
		personID, parseErr := uuid.Parse(request.PersonID)
		if parseErr != nil || personID == uuid.Nil {
			return suggestionError(ErrInvalidResolution)
		}
		response, err = h.service.AcceptExisting(h.requestContext(c), actor, id, personID)
	}
	if err != nil {
		return suggestionError(err)
	}
	return c.JSON(http.StatusOK, response)
}

func suggestionError(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return errcodes.NotFound("Invitation suggestion")
	case errors.Is(err, ErrNotSubmitted):
		return errcodes.Conflict("This Invitation suggestion is no longer Submitted. Refresh before trying again.")
	case errors.Is(err, ErrPersonUnavailable):
		return errcodes.Conflict("Choose a current Person for this Invitation suggestion.")
	case errors.Is(err, ErrInvalidSuggestion):
		return errcodes.ValidationError("Enter a valid name, email, relationship context, and spoken answer for this Invitation suggestion.")
	case errors.Is(err, ErrInvalidStatus):
		return errcodes.ValidationError("Choose a valid Invitation suggestion status filter.")
	case errors.Is(err, ErrInvalidResolution):
		return errcodes.ValidationError("Choose exactly one current or new Person when accepting this Invitation suggestion.")
	case errors.Is(err, people.ErrInvalidPerson):
		return errcodes.ValidationError("Enter valid details for the new Person.")
	case errors.Is(err, setup.ErrUnauthenticated):
		return errcodes.Unauthorized("A valid completed Recipient Session is required.")
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

// RegisterRoutes declares requester-self and Curator policies independently.
func RegisterRoutes(e *echo.Echo, handler *Handler) {
	group := e.Group("/api/invitation-suggestions", noStore)
	listRequester := group.GET("", handler.ListRequester)
	listRequester.Name = recipientReadPolicy
	submit := group.POST("", handler.Submit)
	submit.Name = recipientMutationPolicy
	withdraw := group.POST("/:id/withdraw", handler.Withdraw)
	withdraw.Name = recipientMutationPolicy
	listCurator := group.GET("/curator", handler.ListCurator)
	listCurator.Name = curatorReadPolicy
	reject := group.POST("/curator/:id/reject", handler.Reject)
	reject.Name = curatorMutationPolicy
	accept := group.POST("/curator/:id/accept", handler.Accept)
	accept.Name = curatorMutationPolicy
}
