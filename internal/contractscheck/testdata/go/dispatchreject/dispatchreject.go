package dispatchreject

type localContext interface {
	Bind(any) error
	JSON(int, any) error
	JSONPretty(int, any, string) error
}

type Response struct {
	ID string `json:"id"`
}

func BindThroughLocalInterface(c localContext) error {
	request := map[string]string{}
	return c.Bind(&request)
}

func JSONThroughLocalInterface(c localContext) error {
	return c.JSON(200, map[string]string{"id": "one"})
}

func JSONPrettyThroughLocalInterface(c localContext) error {
	return c.JSONPretty(200, struct {
		ID string `json:"id"`
	}{ID: "one"}, "  ")
}
