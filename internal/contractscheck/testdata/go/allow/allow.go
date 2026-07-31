package allow

import "github.com/labstack/echo/v4"

type Request struct {
	Name string `json:"name"`
}

type Response struct {
	ID string `json:"id"`
}

func bindJSON(c echo.Context, target any) error {
	return c.Bind(target)
}

func Handler(c echo.Context) error {
	var request Request
	if err := bindJSON(c, &request); err != nil {
		return err
	}
	response := Response{ID: request.Name}
	return c.JSONPretty(200, response, "  ")
}

func IndexByID(values []Response) map[string]Response {
	result := make(map[string]Response, len(values))
	for _, value := range values {
		result[value.ID] = value
	}
	return result
}
