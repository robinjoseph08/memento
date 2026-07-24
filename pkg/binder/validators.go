package binder

import (
	"net/url"
	"time"

	"github.com/go-playground/validator/v10"
)

func dateValidator(field validator.FieldLevel) bool {
	value := field.Field().String()
	if value == "" {
		return true
	}
	_, err := time.Parse(time.DateOnly, value)
	return err == nil
}

func urlValidator(field validator.FieldLevel) bool {
	value := field.Field().String()
	if value == "" {
		return true
	}
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme != "" && parsed.Host != ""
}
