package binder

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strings"

	"github.com/creasty/defaults"
	"github.com/go-playground/mold/v4"
	"github.com/go-playground/mold/v4/modifiers"
	"github.com/go-playground/validator/v10"
	"github.com/gorilla/schema"
	"github.com/labstack/echo/v5"
	"github.com/robinjoseph08/memento/pkg/errcodes"
)

const (
	disallowEmptyBodyKey     = "disallow_empty_body"
	disallowUnknownFieldsKey = "disallow_unknown_fields"
)

var unknownFieldsRE = regexp.MustCompile(`^json: unknown field "(.*)"$`)

// Binder binds request data, normalizes it, applies defaults, and validates the
// result.
type Binder struct {
	queryDecoder *schema.Decoder
	formDecoder  *schema.Decoder
	conform      *mold.Transformer
	validate     *validator.Validate
}

// New creates a request binder.
func New() (*Binder, error) {
	queryDecoder := schema.NewDecoder()
	queryDecoder.SetAliasTag("query")
	formDecoder := schema.NewDecoder()
	formDecoder.SetAliasTag("form")

	validate := validator.New()
	validate.RegisterTagNameFunc(func(field reflect.StructField) string {
		name := strings.SplitN(field.Tag.Get("json"), ",", 2)[0]
		if name == "-" {
			return ""
		}
		return name
	})
	if err := validate.RegisterValidation("date", dateValidator); err != nil {
		return nil, err
	}
	if err := validate.RegisterValidation("url", urlValidator); err != nil {
		return nil, err
	}

	return &Binder{
		queryDecoder: queryDecoder,
		formDecoder:  formDecoder,
		conform:      modifiers.New(),
		validate:     validate,
	}, nil
}

// Bind implements echo.Binder.
func (b *Binder) Bind(c *echo.Context, target any) error {
	req := c.Request()
	disallowEmptyBody := contextBool(c, disallowEmptyBodyKey, true)

	hasBody := req.Body != nil && req.Body != http.NoBody
	if hasBody {
		mediaType, _, err := mime.ParseMediaType(req.Header.Get(echo.HeaderContentType))
		if err != nil {
			return errcodes.UnsupportedMediaType()
		}

		switch mediaType {
		case echo.MIMEApplicationJSON:
			if err := b.bindJSON(c, target, disallowEmptyBody); err != nil {
				return err
			}
		case echo.MIMEApplicationForm:
			params, err := c.FormValues()
			if err != nil {
				return errcodes.MalformedPayload()
			}
			if err := b.decode(target, params, b.formDecoder); err != nil {
				return err
			}
		case echo.MIMEMultipartForm:
			if err := b.bindMultipart(c, target); err != nil {
				return err
			}
		default:
			return errcodes.UnsupportedMediaType()
		}
	} else if req.Method == http.MethodGet || req.Method == http.MethodDelete {
		if err := b.decode(target, c.QueryParams(), b.queryDecoder); err != nil {
			return err
		}
	} else if disallowEmptyBody {
		return errcodes.EmptyRequestBody()
	}

	if err := b.conform.Struct(req.Context(), target); err != nil {
		return err
	}
	if err := defaults.Set(target); err != nil {
		return err
	}
	if err := b.validate.Struct(target); err != nil {
		var validationErrors validator.ValidationErrors
		if !errors.As(err, &validationErrors) || len(validationErrors) == 0 {
			return err
		}
		return errcodes.ValidationError(formatValidationError(validationErrors[0]))
	}

	return nil
}

func (b *Binder) bindJSON(c *echo.Context, target any, disallowEmptyBody bool) error {
	decoder := json.NewDecoder(c.Request().Body)
	if contextBool(c, disallowUnknownFieldsKey, true) {
		decoder.DisallowUnknownFields()
	}

	if err := decoder.Decode(target); err != nil {
		if errors.Is(err, io.EOF) {
			if disallowEmptyBody {
				return errcodes.EmptyRequestBody()
			}
			return nil
		}
		return decodeJSONError(err)
	}

	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errcodes.MalformedPayload()
	}
	return nil
}

func decodeJSONError(err error) error {
	if matches := unknownFieldsRE.FindStringSubmatch(err.Error()); len(matches) == 2 {
		return errcodes.UnknownParameter(matches[1])
	}

	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		return errcodes.ValidationTypeError(formatUnmarshalTypeError(typeErr))
	}

	return errcodes.MalformedPayload()
}

func (b *Binder) bindMultipart(c *echo.Context, target any) error {
	form, err := c.MultipartForm()
	if err != nil {
		return errcodes.MalformedPayload()
	}
	if err := b.decode(target, form.Value, b.formDecoder); err != nil {
		return err
	}
	bindFormFiles(target, form)
	return nil
}

func bindFormFiles(target any, form *multipart.Form) {
	value := reflect.ValueOf(target)
	if value.Kind() != reflect.Pointer || value.IsNil() || value.Elem().Kind() != reflect.Struct {
		return
	}

	field := value.Elem().FieldByName("FormFiles")
	if !field.IsValid() || !field.CanSet() || field.Kind() != reflect.Map {
		return
	}

	files := reflect.MakeMap(field.Type())
	for key, headers := range form.File {
		if len(headers) == 0 {
			continue
		}
		keyValue := reflect.ValueOf(key)
		headerValue := reflect.ValueOf(headers[0])
		if keyValue.Type().AssignableTo(field.Type().Key()) && headerValue.Type().AssignableTo(field.Type().Elem()) {
			files.SetMapIndex(keyValue, headerValue)
		}
	}
	field.Set(files)
}

func (b *Binder) decode(target any, params url.Values, decoder *schema.Decoder) error {
	if err := decoder.Decode(target, params); err != nil {
		var multiError schema.MultiError
		if !errors.As(err, &multiError) {
			return err
		}
		for _, itemErr := range multiError {
			var conversionErr schema.ConversionError
			if errors.As(itemErr, &conversionErr) {
				return errcodes.ValidationTypeError(formatSchemaConversionError(conversionErr))
			}
			var unknownKeyErr schema.UnknownKeyError
			if errors.As(itemErr, &unknownKeyErr) {
				return errcodes.UnknownParameter(unknownKeyErr.Key)
			}
			return itemErr
		}
	}
	return nil
}

func contextBool(c *echo.Context, key string, fallback bool) bool {
	value, ok := c.Get(key).(bool)
	if !ok {
		return fallback
	}
	return value
}
