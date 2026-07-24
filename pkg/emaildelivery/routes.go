package emaildelivery

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/errcodes"
)

const setupOnlyPolicy = "policy:setup_only"

type requester interface {
	RequestTest(c echo.Context) error
	GetStatus(c echo.Context) error
}

// Handler exposes the setup-only required test-email seam.
type Handler struct{ service *Service }

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

func (h *Handler) RequestTest(c echo.Context) error {
	response, err := h.service.RequestTest(c.Request().Context())
	if err != nil {
		switch {
		case errors.Is(err, ErrNotConfigured):
			return errcodes.ServiceUnavailable("SMTP is not configured.")
		case errors.Is(err, ErrSetupComplete):
			return errcodes.Forbidden("Sending a setup test email")
		default:
			return err
		}
	}
	return c.JSON(http.StatusAccepted, response)
}

func (h *Handler) GetStatus(c echo.Context) error {
	response, err := h.service.Status(c.Request().Context(), c.Param("delivery_id"))
	if errors.Is(err, ErrDeliveryAbsent) {
		return errcodes.NotFound("Email delivery")
	}
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, response)
}

// RegisterRoutes registers setup-only test-email routes.
func RegisterRoutes(e *echo.Echo, handler requester) {
	request := e.POST("/api/setup/email/test", handler.RequestTest)
	request.Name = setupOnlyPolicy
	status := e.GET("/api/setup/email/test/:delivery_id", handler.GetStatus)
	status.Name = setupOnlyPolicy
}
