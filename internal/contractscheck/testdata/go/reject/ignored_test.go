package reject

import "github.com/labstack/echo/v4"

func testOnlyAnonymousContract(c echo.Context) error {
	return c.JSON(200, map[string]string{"test": "only"})
}
