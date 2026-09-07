package binder

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/go-playground/validator/v10"
	"github.com/gorilla/schema"
)

func formatUnmarshalTypeError(err *json.UnmarshalTypeError) string {
	return formatExpectedType(err.Type)
}

func formatSchemaConversionError(err schema.ConversionError) string {
	return formatExpectedType(err.Type)
}

func formatExpectedType(kind reflect.Type) string {
	switch kind.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "Enter a whole number."
	case reflect.Float32, reflect.Float64:
		return "Enter a number."
	case reflect.String:
		return "Enter text."
	case reflect.Bool:
		return "Choose yes or no."
	default:
		return "Check this value."
	}
}

func formatValidationError(err validator.FieldError) string {
	switch err.Tag() {
	case "date":
		return "Enter a valid date in YYYY-MM-DD format."
	case "email":
		return "Enter a valid email address."
	case "gt", "lt":
		switch err.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
			reflect.Float32, reflect.Float64:
			if err.Tag() == "gt" {
				return fmt.Sprintf("Enter a number greater than %s.", err.Param())
			}
			return fmt.Sprintf("Enter a number less than %s.", err.Param())
		}
	case "max", "min", "gte", "lte":
		return formatLimit(err)
	case "required":
		return "Enter a value."
	case "url":
		return "Enter a valid web address."
	}
	return "Check this value."
}

func formatLimit(err validator.FieldError) string {
	minimum := err.Tag() == "min" || err.Tag() == "gte"
	switch err.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		if minimum {
			return fmt.Sprintf("Enter %s or more.", err.Param())
		}
		return fmt.Sprintf("Enter %s or less.", err.Param())
	case reflect.Slice, reflect.Array, reflect.Map:
		if minimum {
			return fmt.Sprintf("Choose at least %s %s.", err.Param(), pluralize("item", err.Param()))
		}
		return fmt.Sprintf("Choose %s %s or fewer.", err.Param(), pluralize("item", err.Param()))
	case reflect.String:
		if minimum {
			return fmt.Sprintf("Use at least %s %s.", err.Param(), pluralize("character", err.Param()))
		}
		return fmt.Sprintf("Use %s %s or fewer.", err.Param(), pluralize("character", err.Param()))
	default:
		return "Check this value."
	}
}

func pluralize(resource, count string) string {
	if count == "1" {
		return resource
	}
	return resource + "s"
}
