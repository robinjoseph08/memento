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
	case "date":
		return fmt.Sprintf("%q should be in the format of YYYY-MM-DD", field)
	case "email":
		return fmt.Sprintf("%q is not a valid email", field)
	case "gt":
		value := err.Param()
		if value == "" && err.Type() == timeType {
			value = "now"
		}
		return fmt.Sprintf("%q must be greater than %s", field, value)
	case "gte":
		value := err.Param()
		if value == "" && err.Type() == timeType {
			value = "now"
		}
		return fmt.Sprintf("%q must be greater than or equal to %s", field, value)
	case "gtfield":
		return fmt.Sprintf("%q must be greater than %s", field, err.Param())
	case "ltfield":
		return fmt.Sprintf("%q must be less than %s", field, err.Param())
	case "max":
		return formatLimit(field, err, "less than or equal to")
	case "min":
		return formatLimit(field, err, "greater than or equal to")
	case "ne":
		return fmt.Sprintf("%q can't be %q", field, err.Param())
	case "oneof":
		values := make([]string, 0, len(strings.Fields(err.Param())))
		for _, value := range strings.Fields(err.Param()) {
			values = append(values, fmt.Sprintf("%q", value))
		}
		return fmt.Sprintf("%q must be one of the following: %s", field, strings.Join(values, ", "))
	case "required":
		return fmt.Sprintf("%q is required", field)
	case "url":
		return fmt.Sprintf("%q is not a valid URL", field)
	default:
		return fmt.Sprintf("%q failed validation %q", field, err.Tag())
	}
}

func formatLimit(field string, err validator.FieldError, comparison string) string {
	switch err.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return fmt.Sprintf("%q must be %s %s", field, comparison, err.Param())
	case reflect.Slice, reflect.Array:
		return fmt.Sprintf("%q length must be %s %s %s", field, comparison, err.Param(), pluralize("element", err.Param()))
	default:
		return fmt.Sprintf("%q length must be %s %s %s", field, comparison, err.Param(), pluralize("character", err.Param()))
	}
}

func pluralize(resource, count string) string {
	if count == "1" {
		return resource
	}
	return resource + "s"
}
