package binder

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/gorilla/schema"
)

const (
	dateTag     = "date"
	emailTag    = "email"
	greaterThan = "gt"
	greaterOrEq = "gte"
	gtField     = "gtfield"
	lessField   = "ltfield"
	maximum     = "max"
	minimum     = "min"
	notEqual    = "ne"
	oneOf       = "oneof"
	required    = "required"
	urlTag      = "url"
)

var timeType = reflect.TypeOf(time.Time{})

func formatUnmarshalTypeError(err *json.UnmarshalTypeError) string {
	return fmt.Sprintf("%q should be of type %s", strings.Trim(err.Field, "."), err.Type)
}

func formatSchemaConversionError(err schema.ConversionError) string {
	return fmt.Sprintf("%q should be of type %s", err.Key, err.Type)
}

func formatValidationError(err validator.FieldError) string {
	field := err.Field()
	switch err.Tag() {
	case dateTag:
		return fmt.Sprintf("%q should be in the format YYYY-MM-DD", field)
	case emailTag:
		return fmt.Sprintf("%q is not a valid email", field)
	case greaterThan:
		value := validationLimit(err)
		return fmt.Sprintf("%q must be greater than %s", field, value)
	case greaterOrEq:
		value := validationLimit(err)
		return fmt.Sprintf("%q must be greater than or equal to %s", field, value)
	case gtField:
		return fmt.Sprintf("%q must be greater than %s", field, err.Param())
	case lessField:
		return fmt.Sprintf("%q must be less than %s", field, err.Param())
	case maximum:
		return formatMaximumError(field, err)
	case minimum:
		return formatMinimumError(field, err)
	case notEqual:
		return fmt.Sprintf("%q can't be %q", field, err.Param())
	case oneOf:
		values := make([]string, 0, len(strings.Fields(err.Param())))
		for _, value := range strings.Fields(err.Param()) {
			values = append(values, fmt.Sprintf("%q", value))
		}
		return fmt.Sprintf("%q must be one of: %s", field, strings.Join(values, ", "))
	case required:
		return fmt.Sprintf("%q is required", field)
	case urlTag:
		return fmt.Sprintf("%q is not a valid URL", field)
	default:
		return fmt.Sprintf("%q is invalid", field)
	}
}

func validationLimit(err validator.FieldError) string {
	if err.Param() == "" && err.Type() == timeType {
		return "now"
	}
	return err.Param()
}

func formatMaximumError(field string, err validator.FieldError) string {
	//exhaustive:ignore
	switch err.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return fmt.Sprintf("%q must be less than or equal to %s", field, err.Param())
	case reflect.Slice:
		return fmt.Sprintf("%q length must be less than or equal to %s %s", field, err.Param(), plural(err.Param(), "element"))
	default:
		return fmt.Sprintf("%q length must be less than or equal to %s %s", field, err.Param(), plural(err.Param(), "character"))
	}
}

func formatMinimumError(field string, err validator.FieldError) string {
	//exhaustive:ignore
	switch err.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return fmt.Sprintf("%q must be greater than or equal to %s", field, err.Param())
	case reflect.Slice:
		return fmt.Sprintf("%q length must be greater than or equal to %s %s", field, err.Param(), plural(err.Param(), "element"))
	default:
		return fmt.Sprintf("%q length must be greater than or equal to %s %s", field, err.Param(), plural(err.Param(), "character"))
	}
}

func plural(count, noun string) string {
	if count == "1" {
		return noun
	}
	return noun + "s"
}
