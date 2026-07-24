// Package binder binds, normalizes, and validates Echo request payloads.
package binder

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/creasty/defaults"
	"github.com/go-playground/mold/v4"
	"github.com/go-playground/mold/v4/modifiers"
	"github.com/go-playground/validator/v10"
	"github.com/gorilla/schema"
	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/errcodes"
)

var (
	unknownFieldsRE       = regexp.MustCompile(`^json: unknown field "(.*)"$`)
	errInvalidBindTarget  = errors.New("bind form files: target must be a non-nil struct pointer")
	errInvalidFormFileMap = errors.New("bind form files: FormFiles must be map[string]*multipart.FileHeader")
	formFilesType         = reflect.TypeFor[map[string]*multipart.FileHeader]()
)

// Binder implements Echo binding with strict decoding, normalization, defaults, and validation.
type Binder struct {
	queryDecoder *schema.Decoder
	formDecoder  *schema.Decoder
	conform      *mold.Transformer
	validate     *validator.Validate
}

// New initializes a Binder and its custom validators.
func New() (*Binder, error) {
	queryDecoder := schema.NewDecoder()
	queryDecoder.SetAliasTag("query")
	formDecoder := schema.NewDecoder()
	formDecoder.SetAliasTag("form")
	conform := modifiers.New()
	validate := validator.New(validator.WithRequiredStructEnabled())
	validate.RegisterTagNameFunc(func(field reflect.StructField) string {
		name := strings.SplitN(field.Tag.Get("json"), ",", 2)[0]
		if name == "-" {
			return ""
		}
		return name
	})
	if err := validate.RegisterValidation("date", dateValidator); err != nil {
		return nil, fmt.Errorf("register date validator: %w", err)
	}
	if err := validate.RegisterValidation("url", urlValidator); err != nil {
		return nil, fmt.Errorf("register URL validator: %w", err)
	}
	return &Binder{
		queryDecoder: queryDecoder,
		formDecoder:  formDecoder,
		conform:      conform,
		validate:     validate,
	}, nil
}

// Bind decodes, normalizes, defaults, and validates a request into target.
func (b *Binder) Bind(target any, c echo.Context) error {
	request := c.Request()
	disallowEmptyBody := contextBool(c, "disallow_empty_body", true)
	hasBody, err := requestHasBody(request)
	if err != nil {
		return err
	}

	if hasBody {
		contentType, _, parseErr := mime.ParseMediaType(request.Header.Get(echo.HeaderContentType))
		if parseErr != nil {
			return errcodes.UnsupportedMediaType()
		}
		switch contentType {
		case echo.MIMEApplicationJSON:
			disallowUnknownFields := contextBool(c, "disallow_unknown_fields", true)
			if err := decodeJSON(request, target, disallowUnknownFields); err != nil {
				return err
			}
		case echo.MIMEApplicationForm:
			if err := b.decodeForm(target, c); err != nil {
				return err
			}
		case echo.MIMEMultipartForm:
			if err := b.decodeForm(target, c); err != nil {
				return err
			}
			if err := bindFormFiles(target, c); err != nil {
				return err
			}
		default:
			return errcodes.UnsupportedMediaType()
		}
	} else if request.Method == http.MethodGet || request.Method == http.MethodDelete {
		if err := b.decodeQuery(target, c.QueryParams(), b.queryDecoder); err != nil {
			return err
		}
	} else if disallowEmptyBody {
		return errcodes.EmptyRequestBody()
	}

	if err := b.conform.Struct(request.Context(), target); err != nil {
		return fmt.Errorf("normalize request: %w", err)
	}
	if err := defaults.Set(target); err != nil {
		return fmt.Errorf("apply request defaults: %w", err)
	}
	if err := b.validate.Struct(target); err != nil {
		validationErrors, ok := err.(validator.ValidationErrors)
		if !ok || len(validationErrors) == 0 {
			return fmt.Errorf("validate request: %w", err)
		}
		return errcodes.ValidationError(formatValidationError(validationErrors[0]))
	}
	return nil
}

type bufferedReadCloser struct {
	io.Reader
	io.Closer
}

func requestHasBody(request *http.Request) (bool, error) {
	if request.Body == nil || request.Body == http.NoBody || request.ContentLength == 0 {
		return false, nil
	}
	if request.ContentLength > 0 {
		return true, nil
	}
	reader := bufio.NewReader(request.Body)
	_, err := reader.Peek(1)
	request.Body = bufferedReadCloser{Reader: reader, Closer: request.Body}
	if errors.Is(err, io.EOF) {
		return false, nil
	}
	if err != nil {
		return false, translateReadError(err)
	}
	return true, nil
}

func decodeJSON(request *http.Request, target any, disallowUnknownFields bool) error {
	stream := json.NewDecoder(request.Body)
	var raw json.RawMessage
	if err := stream.Decode(&raw); err != nil {
		if errors.Is(err, io.EOF) {
			return errcodes.EmptyRequestBody()
		}
		return translateReadError(err)
	}
	var trailing json.RawMessage
	if err := stream.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err != nil {
			if translated := oversizedBodyError(err); translated != nil {
				return translated
			}
		}
		return errcodes.MalformedPayload()
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errcodes.MalformedPayload()
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	if disallowUnknownFields {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(target); err != nil {
		if matches := unknownFieldsRE.FindStringSubmatch(err.Error()); len(matches) > 1 {
			return errcodes.UnknownParameter(matches[1])
		}
		var typeError *json.UnmarshalTypeError
		if errors.As(err, &typeError) {
			return errcodes.ValidationTypeError(formatUnmarshalTypeError(typeError))
		}
		return errcodes.MalformedPayload()
	}
	return nil
}

func translateReadError(err error) error {
	if translated := oversizedBodyError(err); translated != nil {
		return translated
	}
	return errcodes.MalformedPayload()
}

func oversizedBodyError(err error) error {
	var httpError *echo.HTTPError
	if errors.As(err, &httpError) && httpError.Code == http.StatusRequestEntityTooLarge {
		return httpError
	}
	return nil
}

func (b *Binder) decodeForm(target any, c echo.Context) error {
	parameters, err := c.FormParams()
	if err != nil {
		return translateReadError(err)
	}
	return b.decodeQuery(target, parameters, b.formDecoder)
}

func (b *Binder) decodeQuery(target any, parameters url.Values, decoder *schema.Decoder) error {
	if err := decoder.Decode(target, parameters); err != nil {
		var multiError schema.MultiError
		if errors.As(err, &multiError) && len(multiError) != 0 {
			keys := make([]string, 0, len(multiError))
			for key := range multiError {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			parameterError := multiError[keys[0]]
			var conversionError schema.ConversionError
			if errors.As(parameterError, &conversionError) {
				return errcodes.ValidationTypeError(formatSchemaConversionError(conversionError))
			}
			var unknownKeyError schema.UnknownKeyError
			if errors.As(parameterError, &unknownKeyError) {
				return errcodes.UnknownParameter(unknownKeyError.Key)
			}
			return fmt.Errorf("decode request parameters: %w", parameterError)
		}
		return fmt.Errorf("decode request parameters: %w", err)
	}
	return nil
}

func bindFormFiles(target any, c echo.Context) error {
	form, err := c.MultipartForm()
	if err != nil {
		return translateReadError(err)
	}
	value := reflect.ValueOf(target)
	if value.Kind() != reflect.Pointer || value.IsNil() || value.Elem().Kind() != reflect.Struct {
		return errInvalidBindTarget
	}
	field := value.Elem().FieldByName("FormFiles")
	if !field.IsValid() || !field.CanSet() {
		return nil
	}
	if field.Kind() != reflect.Map || field.Type() != formFilesType {
		return errInvalidFormFileMap
	}
	files := reflect.MakeMap(field.Type())
	for key, headers := range form.File {
		if len(headers) == 0 {
			continue
		}
		keyValue := reflect.ValueOf(key)
		headerValue := reflect.ValueOf(headers[0])
		if !keyValue.Type().AssignableTo(field.Type().Key()) || !headerValue.Type().AssignableTo(field.Type().Elem()) {
			return errInvalidFormFileMap
		}
		files.SetMapIndex(keyValue, headerValue)
	}
	field.Set(files)
	return nil
}

func contextBool(c echo.Context, key string, fallback bool) bool {
	value, ok := c.Get(key).(bool)
	if !ok {
		return fallback
	}
	return value
}
