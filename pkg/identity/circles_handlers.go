package identity

import "github.com/labstack/echo/v5"

func (h *Handlers) setPersonCircles(c *echo.Context) error {
	var request PersonCirclesRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	return emptyResult(c, h.module.SetPersonCircles(c.Request().Context(), h.browserToken(c), c.Param("id"), request))
}

func (h *Handlers) listCircles(c *echo.Context) error {
	result, err := h.module.ListCircles(c.Request().Context(), h.browserToken(c))
	return jsonResult(c, result, err)
}

func (h *Handlers) createCircle(c *echo.Context) error {
	var request CircleRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.CreateCircle(c.Request().Context(), h.browserToken(c), request)
	return jsonResult(c, result, err)
}

func (h *Handlers) renameCircle(c *echo.Context) error {
	var request CircleRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.RenameCircle(c.Request().Context(), h.browserToken(c), c.Param("id"), request)
	return jsonResult(c, result, err)
}

func (h *Handlers) deleteCircle(c *echo.Context) error {
	var request struct{}
	if err := c.Bind(&request); err != nil {
		return err
	}
	return emptyResult(c, h.module.DeleteCircle(c.Request().Context(), h.browserToken(c), c.Param("id")))
}

func (h *Handlers) setCircleMembers(c *echo.Context) error {
	var request CircleMembersRequest
	if err := c.Bind(&request); err != nil {
		return err
	}
	result, err := h.module.SetCircleMembers(c.Request().Context(), h.browserToken(c), c.Param("id"), request)
	return jsonResult(c, result, err)
}
