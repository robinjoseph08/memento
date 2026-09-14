package notifications

import (
	"context"
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/robinjoseph08/memento/pkg/errorstack"
)

// UseCases is the HTTP seam. Handlers translate requests; the module owns
// every transaction and the announcement invariants.
type UseCases interface {
	PreviewUpdates(context.Context) (Preview, error)
	ApproveUpdates(context.Context, ApproveRequest) (Approval, error)
	ListNotifications(ctx context.Context, personID string) (NotificationList, error)
	MarkRead(ctx context.Context, personID, notificationID string) (Notification, error)
	MarkAllRead(ctx context.Context, personID string) (NotificationList, error)
	DeliveryStates(ctx context.Context, ids []string) (map[string]Delivery, error)
	RetryDelivery(ctx context.Context, deliveryID string) (Delivery, error)
	UnsubscribeStatus(ctx context.Context, token string) (UnsubscribeStatus, error)
	Unsubscribe(ctx context.Context, token string) (UnsubscribeStatus, error)
}

type handlers struct{ module UseCases }

func personID(c *echo.Context) string {
	id, _ := c.Get("identity.person_id").(string)
	return id
}

func respond(c *echo.Context, result any, err error) error {
	if err != nil {
		return err
	}
	return errorstack.CaptureContext(c.Request().Context(), c.JSON(http.StatusOK, result))
}

func (h *handlers) preview(c *echo.Context) error {
	var request struct{}
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.PreviewUpdates(c.Request().Context())
	return respond(c, result, err)
}

func (h *handlers) approve(c *echo.Context) error {
	var request ApproveRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.ApproveUpdates(c.Request().Context(), request)
	return respond(c, result, err)
}

func (h *handlers) list(c *echo.Context) error {
	result, err := h.module.ListNotifications(c.Request().Context(), personID(c))
	return respond(c, result, err)
}

func (h *handlers) markRead(c *echo.Context) error {
	var request struct{}
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.MarkRead(c.Request().Context(), personID(c), c.Param("id"))
	return respond(c, result, err)
}

func (h *handlers) markAllRead(c *echo.Context) error {
	var request struct{}
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.MarkAllRead(c.Request().Context(), personID(c))
	return respond(c, result, err)
}

func (h *handlers) deliveries(c *echo.Context) error {
	result, err := h.module.DeliveryStates(c.Request().Context(), c.QueryParams()["id"])
	return respond(c, result, err)
}

func (h *handlers) retryDelivery(c *echo.Context) error {
	var request struct{}
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.RetryDelivery(c.Request().Context(), c.Param("id"))
	return respond(c, result, err)
}

func (h *handlers) unsubscribeStatus(c *echo.Context) error {
	result, err := h.module.UnsubscribeStatus(c.Request().Context(), c.QueryParam("token"))
	return respond(c, result, err)
}

// unsubscribe is the confirmation step. Only this POST changes the preference.
func (h *handlers) unsubscribe(c *echo.Context) error {
	var request struct{}
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.Unsubscribe(c.Request().Context(), c.QueryParam("token"))
	return respond(c, result, err)
}
