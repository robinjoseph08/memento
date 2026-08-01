package reject

import "github.com/labstack/echo/v4"

type privateResponse struct {
	Message string `json:"message"`
}

func bindJSON(c echo.Context, target any) error {
	return c.Bind(target)
}

func AnonymousRequest(c echo.Context) error {
	request := struct {
		Name string `json:"name"`
	}{}
	return bindJSON(c, &request)
}

func MapRequest(c echo.Context) error {
	request := map[string]string{}
	return c.Bind(&request)
}

func PrivateResponse(c echo.Context) error {
	response := privateResponse{Message: "no"}
	return c.JSON(200, response)
}

func MapResponse(c echo.Context) error {
	return c.JSON(200, map[string]string{"status": "no"})
}

func AnonymousResponse(c echo.Context) error {
	return c.JSONPretty(200, struct {
		Status string `json:"status"`
	}{Status: "no"}, "  ")
}

func callBind(bind func(any) error) error {
	return bind(&struct{}{})
}

func HigherOrderBind(c echo.Context) error {
	return callBind(c.Bind)
}

func ReturnedJSON(c echo.Context) func(int, any) error {
	return c.JSON
}

func ReturnedJSONPretty(c echo.Context) func(int, any, string) error {
	return c.JSONPretty
}

func ReturnedBindMethodExpression() func(echo.Context, any) error {
	return echo.Context.Bind
}

func ReturnedBindWrapper() func(echo.Context, any) error {
	return bindJSON
}
