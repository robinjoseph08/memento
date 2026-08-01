package echo

type Context interface {
	Bind(any) error
	JSON(int, any) error
	JSONPretty(int, any, string) error
}
