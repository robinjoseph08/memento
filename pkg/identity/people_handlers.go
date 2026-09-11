package identity

import (
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/robinjoseph08/memento/pkg/errorstack"
)

func (h *Handlers) browserToken(c *echo.Context) string {
	cookie, err := c.Cookie(h.cookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func jsonResult(c *echo.Context, result any, err error) error {
	if err != nil {
		return err
	}
	return errorstack.CaptureContext(c.Request().Context(), c.JSON(http.StatusOK, result))
}

func emptyResult(c *echo.Context, err error) error {
	if err != nil {
		return err
	}
	return errorstack.CaptureContext(c.Request().Context(), c.NoContent(http.StatusNoContent))
}

func (h *Handlers) listPeople(c *echo.Context) error {
	result, err := h.module.ListPeople(c.Request().Context(), h.browserToken(c), c.QueryParam("q"))
	return jsonResult(c, result, err)
}

func (h *Handlers) createPerson(c *echo.Context) error {
	var request CreatePersonRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.CreatePerson(c.Request().Context(), h.browserToken(c), request)
	return jsonResult(c, result, err)
}

func (h *Handlers) getPerson(c *echo.Context) error {
	result, err := h.module.GetPerson(c.Request().Context(), h.browserToken(c), c.Param("id"))
	return jsonResult(c, result, err)
}

func (h *Handlers) linkFace(c *echo.Context) error {
	var request LinkFaceRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.LinkFace(c.Request().Context(), h.browserToken(c), c.Param("id"), request)
	return jsonResult(c, result, err)
}

func (h *Handlers) createPersonFromFace(c *echo.Context) error {
	var request CreatePersonFromFaceRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.CreatePersonFromFace(c.Request().Context(), h.browserToken(c), request)
	return jsonResult(c, result, err)
}

func (h *Handlers) ignoreFace(c *echo.Context) error {
	var request struct{}
	if err := c.Bind(&request); err != nil {
		return err
	}
	return emptyResult(c, h.module.IgnoreFace(c.Request().Context(), h.browserToken(c), c.Param("sourceID")))
}

func (h *Handlers) setPersonAvatar(c *echo.Context) error {
	var request SetPersonAvatarRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.SetPersonAvatar(c.Request().Context(), h.browserToken(c), c.Param("id"), request)
	return jsonResult(c, result, err)
}

func (h *Handlers) updatePerson(c *echo.Context) error {
	var request UpdatePersonRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.UpdatePerson(c.Request().Context(), h.browserToken(c), c.Param("id"), request)
	return jsonResult(c, result, err)
}

func (h *Handlers) preauthorize(c *echo.Context) error {
	var request PreauthorizeRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.Preauthorize(c.Request().Context(), h.browserToken(c), c.Param("id"), request)
	return jsonResult(c, result, err)
}

func (h *Handlers) revokePreauthorization(c *echo.Context) error {
	var request struct{}
	if err := c.Bind(&request); err != nil {
		return err
	}
	return emptyResult(c, h.module.RevokePreauthorization(c.Request().Context(), h.browserToken(c), c.Param("id"), c.Param("preauthorizationID")))
}

func (h *Handlers) profile(c *echo.Context) error {
	result, err := h.module.Profile(c.Request().Context(), h.browserToken(c))
	return jsonResult(c, result, err)
}

func (h *Handlers) updateProfile(c *echo.Context) error {
	var request UpdateProfileRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.UpdateProfile(c.Request().Context(), h.browserToken(c), request)
	return jsonResult(c, result, err)
}

func (h *Handlers) sessions(c *echo.Context) error {
	result, err := h.module.Sessions(c.Request().Context(), h.browserToken(c))
	return jsonResult(c, result, err)
}

func (h *Handlers) signOutEverywhere(c *echo.Context) error {
	var request struct{}
	if err := c.Bind(&request); err != nil {
		return err
	}
	if err := h.module.SignOutEverywhere(c.Request().Context(), h.browserToken(c)); err != nil {
		return err
	}
	h.clearCookie(c)
	return emptyResult(c, nil)
}

func (h *Handlers) unlinkIdentity(c *echo.Context) error {
	var request struct{}
	if err := c.Bind(&request); err != nil {
		return err
	}
	return emptyResult(c, h.module.UnlinkIdentity(c.Request().Context(), h.browserToken(c), c.Param("id"), c.Param("identityID")))
}
