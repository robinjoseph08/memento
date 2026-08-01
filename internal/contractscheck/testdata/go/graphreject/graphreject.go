package graphreject

import (
	"time"

	"github.com/labstack/echo/v4"
)

type AnonymousNestedResponse struct {
	Nested struct {
		ID string `json:"id"`
	} `json:"nested"`
}

type InterfaceRequest struct {
	Value any `json:"value"`
}

type InterfaceDictionaryResponse struct {
	Values map[string]any `json:"values"`
}

type AllowedGraphResponse struct {
	Labels    map[string]string `json:"labels"`
	CreatedAt time.Time         `json:"created_at"`
	State     State             `json:"state"`
}

type State string

func AnonymousNested(c echo.Context) error {
	return c.JSON(200, AnonymousNestedResponse{})
}

func InterfaceField(c echo.Context) error {
	var request InterfaceRequest
	return c.Bind(&request)
}

func InterfaceDictionary(c echo.Context) error {
	return c.JSON(200, InterfaceDictionaryResponse{})
}

func AllowedGraph(c echo.Context) error {
	return c.JSON(200, AllowedGraphResponse{})
}
