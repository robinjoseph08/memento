package events

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/setup"
)

const (
	curatorReadPolicy     = "policy:curator"
	curatorMutationPolicy = "policy:curator_csrf"
)

// Authorizer authenticates the sole Curator without exposing Session internals.
type Authorizer interface {
	AuthorizeCurator(ctx context.Context, credential, csrfToken string, mutation bool) (setup.CuratorSession, error)
	ContextWithRequestMetadata(ctx context.Context, request *http.Request) context.Context
}

// Handler exposes Curator Event workflows.
type Handler struct {
	service    *Service
	authorizer Authorizer
}

func NewHandler(service *Service, authorizer Authorizer) *Handler {
	return &Handler{service: service, authorizer: authorizer}
}

func (h *Handler) requestContext(c echo.Context) context.Context {
	return h.authorizer.ContextWithRequestMetadata(c.Request().Context(), c.Request())
}

func (h *Handler) CreateEvent(c echo.Context) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	var request CreateEventRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	event, err := h.service.CreateEvent(h.requestContext(c), actor, request)
	if mapped := draftError(err, "Event"); mapped != nil {
		return mapped
	}
	return c.JSON(http.StatusCreated, event)
}

func (h *Handler) ListEvents(c echo.Context) error {
	if _, err := h.authorize(c, false); err != nil {
		return err
	}
	response, err := h.service.ListEvents(c.Request().Context())
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) GetEvent(c echo.Context) error {
	if _, err := h.authorize(c, false); err != nil {
		return err
	}
	id, err := routeID(c.Param("id"), "Event")
	if err != nil {
		return err
	}
	event, err := h.service.GetEvent(c.Request().Context(), id)
	if mapped := draftError(err, "Event"); mapped != nil {
		return mapped
	}
	return c.JSON(http.StatusOK, event)
}

func (h *Handler) OrganizeEvent(c echo.Context) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	id, err := routeID(c.Param("id"), "Event")
	if err != nil {
		return err
	}
	var request OrganizeEventRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	if !organizationPlaceLabelsValid(request) {
		return draftError(ErrPlaceLabelsInvalid, "Event")
	}
	event, err := h.service.OrganizeEvent(h.requestContext(c), actor, id, request)
	if mapped := draftError(err, "Event"); mapped != nil {
		return mapped
	}
	return c.JSON(http.StatusOK, event)
}

func (h *Handler) Withdraw(c echo.Context) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	var request WithdrawRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	withdrawal, err := h.service.Withdraw(h.requestContext(c), actor, request)
	if mapped := withdrawalError(err); mapped != nil {
		return mapped
	}
	return c.JSON(http.StatusCreated, withdrawal)
}

func (h *Handler) RestorePublishedMedia(c echo.Context) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	id, err := routeID(c.Param("id"), "Event")
	if err != nil {
		return err
	}
	var request RestorePublishedMediaRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	event, err := h.service.RestorePublishedMedia(h.requestContext(c), actor, id, request)
	if mapped := draftError(err, "Event"); mapped != nil {
		return mapped
	}
	return c.JSON(http.StatusOK, event)
}

func (h *Handler) PublishEvent(c echo.Context) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	id, err := routeID(c.Param("id"), "Event")
	if err != nil {
		return err
	}
	var request PublishEventRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	publication, err := h.service.PublishEvent(h.requestContext(c), actor, id, request)
	if mapped := publicationError(err); mapped != nil {
		return mapped
	}
	return c.JSON(http.StatusCreated, publication)
}

func (h *Handler) PreviewRecipients(c echo.Context) error {
	actor, err := h.authorize(c, false)
	if err != nil {
		return err
	}
	eventID, err := routeID(c.Param("id"), "Event")
	if err != nil {
		return err
	}
	response, err := h.service.PreviewRecipients(c.Request().Context(), actor, eventID)
	if mapped := publicationError(err); mapped != nil {
		return mapped
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) PreviewEvent(c echo.Context) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	eventID, err := routeID(c.Param("id"), "Event")
	if err != nil {
		return err
	}
	recipientID, err := uuid.Parse(c.QueryParam("recipient_person_id"))
	if err != nil || recipientID == uuid.Nil {
		return errcodes.ValidationError("Choose a current Recipient to preview.")
	}
	view, err := h.service.PreviewEvent(h.requestContext(c), actor, eventID, recipientID)
	if mapped := publicationError(err); mapped != nil {
		return mapped
	}
	return c.JSON(http.StatusOK, view)
}

func (h *Handler) SourceMedia(c echo.Context) error {
	if _, err := h.authorize(c, false); err != nil {
		return err
	}
	id, err := routeID(c.Param("id"), "Source album")
	if err != nil {
		return err
	}
	response, err := h.service.SourceMedia(c.Request().Context(), id)
	if mapped := draftError(err, "Source album"); mapped != nil {
		return mapped
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) CreateLooseItem(c echo.Context) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	var request CreateLooseItemRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	item, created, err := h.service.CreateLooseItem(h.requestContext(c), actor, request)
	if mapped := draftError(err, "Loose item"); mapped != nil {
		return mapped
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	return c.JSON(status, item)
}

func (h *Handler) ListLooseItems(c echo.Context) error {
	actor, err := h.authorize(c, false)
	if err != nil {
		return err
	}
	response, err := h.service.ListLooseItems(c.Request().Context(), actor)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) GetLooseItem(c echo.Context) error {
	actor, err := h.authorize(c, false)
	if err != nil {
		return err
	}
	id, err := routeID(c.Param("id"), "Loose item")
	if err != nil {
		return err
	}
	item, err := h.service.GetLooseItem(c.Request().Context(), actor, id)
	if mapped := draftError(err, "Loose item"); mapped != nil {
		return mapped
	}
	return c.JSON(http.StatusOK, item)
}

func (h *Handler) UpdateLooseItem(c echo.Context) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	id, err := routeID(c.Param("id"), "Loose item")
	if err != nil {
		return err
	}
	var request UpdateLooseItemRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	item, err := h.service.UpdateLooseItem(h.requestContext(c), actor, id, request)
	if mapped := draftError(err, "Loose item"); mapped != nil {
		return mapped
	}
	return c.JSON(http.StatusOK, item)
}

func (h *Handler) PublishLooseItem(c echo.Context) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	id, err := routeID(c.Param("id"), "Loose item")
	if err != nil {
		return err
	}
	var request PublishLooseItemRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	publication, err := h.service.PublishLooseItem(h.requestContext(c), actor, id, request)
	if mapped := loosePublicationError(err); mapped != nil {
		return mapped
	}
	return c.JSON(http.StatusCreated, publication)
}

func (h *Handler) PreviewLooseItem(c echo.Context) error {
	actor, err := h.authorize(c, true)
	if err != nil {
		return err
	}
	id, err := routeID(c.Param("id"), "Loose item")
	if err != nil {
		return err
	}
	recipientID, err := uuid.Parse(c.QueryParam("recipient_person_id"))
	if err != nil || recipientID == uuid.Nil {
		return errcodes.ValidationError("Choose a current Recipient to preview.")
	}
	view, err := h.service.PreviewLooseItem(h.requestContext(c), actor, id, recipientID)
	if mapped := loosePublicationError(err); mapped != nil {
		return mapped
	}
	return c.JSON(http.StatusOK, view)
}

func (h *Handler) authorize(c echo.Context, mutation bool) (setup.CuratorSession, error) {
	cookie, err := c.Cookie(setup.CookieName)
	if err != nil || cookie.Value == "" {
		return setup.CuratorSession{}, errcodes.Unauthorized("A valid Curator Session is required.")
	}
	actor, err := h.authorizer.AuthorizeCurator(
		c.Request().Context(), cookie.Value, c.Request().Header.Get(setup.CSRFHeader), mutation,
	)
	switch {
	case errors.Is(err, setup.ErrUnauthenticated):
		return setup.CuratorSession{}, errcodes.Unauthorized("A valid Curator Session is required.")
	case errors.Is(err, setup.ErrCSRF):
		return setup.CuratorSession{}, errcodes.Forbidden("This action requires a valid CSRF token")
	case errors.Is(err, setup.ErrNotCurator):
		return setup.CuratorSession{}, errcodes.NotFound("Page")
	case err != nil:
		return setup.CuratorSession{}, err
	default:
		return actor, nil
	}
}

func publicationError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrNoPublication):
		return errcodes.NotFound("Event")
	case errors.Is(err, ErrVersionConflict):
		return errcodes.Conflict("This Event changed in another browser. Reload and review the current editable version before publishing.")
	case errors.Is(err, ErrPublicationNotReady):
		return errcodes.Conflict("Finish Media organization, approve every Moment Audience, and complete final review before publishing.")
	case errors.Is(err, ErrAudienceNotCurrent):
		return errcodes.Conflict("A Recipient's access changed. Recalculate and approve every affected Audience before publishing.")
	default:
		return err
	}
}

func loosePublicationError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrNoPublication):
		return errcodes.NotFound("Loose item")
	case errors.Is(err, ErrVersionConflict):
		return errcodes.Conflict("This Loose item changed in another browser. Reload and review the current editable version before publishing.")
	case errors.Is(err, ErrPublicationNotReady):
		return errcodes.Conflict("Approve the Loose item Audience, including an explicit Curator-only Audience, before publishing.")
	case errors.Is(err, ErrAudienceNotCurrent):
		return errcodes.Conflict("A Recipient's access changed. Recalculate and approve the Loose item Audience before publishing.")
	case errors.Is(err, ErrMediaUnavailable):
		return errcodes.Conflict("The Loose item's Source Media is unavailable. Relink it before publishing.")
	default:
		return err
	}
}

func withdrawalError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrWithdrawalInvalid):
		return errcodes.ValidationError("Choose a currently published Event, Moment, or Media item and provide a reason up to 1000 characters.")
	case errors.Is(err, ErrAlreadyWithdrawn):
		return errcodes.Conflict("This content is already withdrawn. Restoration requires newly reviewed Audiences and a fresh Publication for every Event where it is currently placed.")
	case errors.Is(err, ErrVersionConflict):
		return errcodes.Conflict("The published placement changed while Withdrawal was starting. Review the current targets and try again.")
	case errors.Is(err, ErrNotFound):
		return errcodes.NotFound("Currently published content")
	default:
		return err
	}
}

func organizationPlaceLabelsValid(request OrganizeEventRequest) bool {
	if _, valid := normalizePlaceLabels(request.PlaceLabels); !valid {
		return false
	}
	for _, moment := range request.Moments {
		if _, valid := normalizePlaceLabels(moment.PlaceLabels); !valid {
			return false
		}
	}
	return true
}

func draftError(err error, resource string) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound):
		return errcodes.NotFound(resource)
	case errors.Is(err, ErrPlaceLabelsInvalid):
		return errcodes.ValidationError("Use no more than 20 Place labels, with 1 to 120 characters in each label.")
	case errors.Is(err, ErrInvalid):
		return errcodes.ValidationError("Draft fields must be valid, include at least one Media item with no duplicates, and use only covers from their Moments.")
	case errors.Is(err, ErrVersionConflict) && resource == "Loose item":
		return errcodes.Conflict("This Loose item changed in another browser. Review the newer version before saving again.")
	case errors.Is(err, ErrVersionConflict):
		return errcodes.Conflict("This Event changed in another browser. Review the newer version before saving again.")
	case errors.Is(err, ErrSourceUnavailable):
		return errcodes.Conflict("Every Source album must be available and not ignored.")
	case errors.Is(err, ErrSourceTooLarge):
		return errcodes.Conflict("The Source album has too many Media items to list for drafting.")
	case errors.Is(err, ErrNoMediaAvailable):
		return errcodes.Conflict("Select at least one available Media item from the selected Source albums.")
	case errors.Is(err, ErrMediaUnavailable) && resource == "Loose item":
		return errcodes.Conflict("The selected Media item is unavailable.")
	case errors.Is(err, ErrMediaUnavailable):
		return errcodes.Conflict("Every selected Media item must belong to a selected Source album and remain available.")
	default:
		return err
	}
}

func routeID(value, resource string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, errcodes.NotFound(resource)
	}
	return id, nil
}

// RegisterRoutes registers no-store Curator workflows.
func RegisterRoutes(e *echo.Echo, handler *Handler) {
	events := e.Group("/api/events", noStore)
	createEvent := events.POST("", handler.CreateEvent)
	createEvent.Name = curatorMutationPolicy
	listEvents := events.GET("", handler.ListEvents)
	listEvents.Name = curatorReadPolicy
	getEvent := events.GET("/:id", handler.GetEvent)
	getEvent.Name = curatorReadPolicy
	organizeEvent := events.PUT("/:id/organization", handler.OrganizeEvent)
	organizeEvent.Name = curatorMutationPolicy
	restorePublishedMedia := events.POST("/:id/published-media-restorations", handler.RestorePublishedMedia)
	restorePublishedMedia.Name = curatorMutationPolicy
	publishEvent := events.POST("/:id/publications", handler.PublishEvent)
	publishEvent.Name = curatorMutationPolicy
	previewRecipients := events.GET("/:id/preview-recipients", handler.PreviewRecipients)
	previewRecipients.Name = curatorReadPolicy
	previewEvent := events.POST("/:id/preview", handler.PreviewEvent)
	previewEvent.Name = curatorMutationPolicy

	withdrawal := e.POST("/api/withdrawals", handler.Withdraw, noStore)
	withdrawal.Name = curatorMutationPolicy

	looseItems := e.Group("/api/loose-items", noStore)
	createLoose := looseItems.POST("", handler.CreateLooseItem)
	createLoose.Name = curatorMutationPolicy
	listLoose := looseItems.GET("", handler.ListLooseItems)
	listLoose.Name = curatorReadPolicy
	getLoose := looseItems.GET("/:id", handler.GetLooseItem)
	getLoose.Name = curatorReadPolicy
	updateLoose := looseItems.PUT("/:id", handler.UpdateLooseItem)
	updateLoose.Name = curatorMutationPolicy
	publishLoose := looseItems.POST("/:id/publications", handler.PublishLooseItem)
	publishLoose.Name = curatorMutationPolicy
	previewLooseRecipients := looseItems.GET("/:id/preview-recipients", handler.PreviewRecipients)
	previewLooseRecipients.Name = curatorReadPolicy
	previewLoose := looseItems.POST("/:id/preview", handler.PreviewLooseItem)
	previewLoose.Name = curatorMutationPolicy

	sourceMedia := e.GET("/api/sources/:id/media-items", handler.SourceMedia, noStore)
	sourceMedia.Name = curatorReadPolicy
}

func noStore(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		return next(c)
	}
}
