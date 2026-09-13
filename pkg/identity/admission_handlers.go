package identity

import (
	"context"

	"github.com/labstack/echo/v5"
)

// AdmissionUseCases covers Onboarding, Invitations, and Access Requests.
type AdmissionUseCases interface {
	CompleteOnboarding(context.Context, string, UpdateProfileRequest) (Person, error)
	SendInvitation(context.Context, string, string, SendInvitationRequest) (Invitation, error)
	RetryInvitation(context.Context, string, string, string) (Invitation, error)
	RequestAlbumAccess(context.Context, string, string) (AccessRequest, error)
	ListAccessRequests(context.Context, string) ([]AccessRequest, error)
	ApproveAccessRequest(context.Context, string, string, ApproveAccessRequestRequest) (AccessRequest, error)
	DenyAccessRequest(context.Context, string, string) (AccessRequest, error)
	ReconsiderAccessRequest(context.Context, string, string) (AccessRequest, error)
}

func (h *Handlers) completeOnboarding(c *echo.Context) error {
	var request UpdateProfileRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.CompleteOnboarding(c.Request().Context(), h.browserToken(c), request)
	return jsonResult(c, result, err)
}

func (h *Handlers) sendInvitation(c *echo.Context) error {
	var request SendInvitationRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.SendInvitation(c.Request().Context(), h.browserToken(c), c.Param("id"), request)
	return jsonResult(c, result, err)
}

func (h *Handlers) retryInvitation(c *echo.Context) error {
	var request struct{}
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.RetryInvitation(c.Request().Context(), h.browserToken(c), c.Param("id"), c.Param("invitationID"))
	return jsonResult(c, result, err)
}

func (h *Handlers) requestAlbumAccess(c *echo.Context) error {
	var request struct{}
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.RequestAlbumAccess(c.Request().Context(), h.browserToken(c), c.Param("id"))
	return jsonResult(c, result, err)
}

func (h *Handlers) listAccessRequests(c *echo.Context) error {
	result, err := h.module.ListAccessRequests(c.Request().Context(), h.browserToken(c))
	return jsonResult(c, result, err)
}

func (h *Handlers) approveAccessRequest(c *echo.Context) error {
	var request ApproveAccessRequestRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.ApproveAccessRequest(c.Request().Context(), h.browserToken(c), c.Param("id"), request)
	return jsonResult(c, result, err)
}

func (h *Handlers) denyAccessRequest(c *echo.Context) error {
	var request struct{}
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.DenyAccessRequest(c.Request().Context(), h.browserToken(c), c.Param("id"))
	return jsonResult(c, result, err)
}

func (h *Handlers) reconsiderAccessRequest(c *echo.Context) error {
	var request struct{}
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.ReconsiderAccessRequest(c.Request().Context(), h.browserToken(c), c.Param("id"))
	return jsonResult(c, result, err)
}
