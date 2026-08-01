package promotionreject

import "github.com/labstack/echo/v4"

type promotedFields struct {
	NestedMap       map[string]any `json:"nested_map"`
	NestedInterface any            `json:"nested_interface"`
	NestedAnonymous struct {
		ID string `json:"id"`
	} `json:"nested_anonymous"`
	hidden  map[string]string `json:"hidden"`
	Ignored map[string]string `json:"-"`
}

type PromotedResponse struct {
	*promotedFields
	ignoredPromotedFields `json:"-"`
	hidden                map[string]string `json:"hidden"`
	Ignored               map[string]string `json:"-"`
}

type ignoredPromotedFields struct {
	Value any `json:"value"`
}

func Promoted(c echo.Context) error {
	return c.JSON(200, PromotedResponse{})
}
