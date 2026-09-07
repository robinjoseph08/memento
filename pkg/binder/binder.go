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
	"github.com/robinjoseph08/memento/pkg/errorstack"
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

// ValidationMessenger optionally supplies feature-specific field messages on a
// request type. Field is the JSON field name and rule is the failed validator tag.
// Return an empty string to use the binder's shared message. Messages appear next
// to the field, so they should explain how to fix it without repeating its name.
type ValidationMessenger interface {
	ValidationMessage(field, rule string) string
}

// New creates a request binder.
func New() (*Binder, error) {
	queryDecoder := schema.NewDecoder()
	queryDecoder.SetAliasTag("query")
	formDecoder := schema.NewDecoder()
	formDecoder.SetAliasTag("form")

	validate := validator.New()
	validate.RegisterTagNameFunc(func(field reflect.StructField) string {
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "-" {
			return ""
		}
		return name
	})
	if err := validate.RegisterValidation("date", dateValidator); err != nil {
		return nil, errorstack.Capture(err)
	}
	if err := validate.RegisterValidation("url", urlValidator); err != nil {
		return nil, errorstack.Capture(err)
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
		return errorstack.CaptureContext(req.Context(), err)
	}
	if err := defaults.Set(target); err != nil {
		return errorstack.Capture(err)
	}
	if err := b.validate.Struct(target); err != nil {
		var validationErrors validator.ValidationErrors
		if !errors.As(err, &validationErrors) || len(validationErrors) == 0 {
			return errorstack.Capture(err)
		}
		fields := make(map[string]string, len(validationErrors))
		messenger, _ := target.(ValidationMessenger)
		for _, field := range validationErrors {
			message := ""
			if messenger != nil {
				message = messenger.ValidationMessage(field.Field(), field.Tag())
			}
			if message == "" {
				message = formatValidationError(field)
			}
			fields[field.Field()] = message
		}
		return errcodes.ValidationFields("Check the highlighted fields.", fields)
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

	if typeErr, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
		return typeValidationError(strings.Trim(typeErr.Field, "."), formatUnmarshalTypeError(typeErr))
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
			return errorstack.Capture(err)
		}
		for _, itemErr := range multiError {
			if conversionErr, ok := errors.AsType[schema.ConversionError](itemErr); ok {
				return typeValidationError(conversionErr.Key, formatSchemaConversionError(conversionErr))
			}
			if unknownKeyErr, ok := errors.AsType[schema.UnknownKeyError](itemErr); ok {
				return errcodes.UnknownParameter(unknownKeyErr.Key)
			}
			return errorstack.Capture(itemErr)
		}
	}
	return nil
}

func typeValidationError(field, message string) error {
	if field == "" {
		return errcodes.ValidationTypeError("Check the submitted values.")
	}
	return &errcodes.FieldError{Cause: errcodes.ValidationTypeError("Check the highlighted fields."), Fields: map[string]string{field: message}}
}

func contextBool(c *echo.Context, key string, fallback bool) bool {
	value, ok := c.Get(key).(bool)
	if !ok {
		return fallback
	}
	return value
}
