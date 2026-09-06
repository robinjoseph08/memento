package binder

import (
	"testing"

	"github.com/go-playground/validator/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormatValidationError(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		value    any
		expected string
	}{
		"required": {
			value: struct {
				Name string `json:"name" validate:"required"`
			}{},
			expected: "Enter a value.",
		},
		"minimum string length": {
			value: struct {
				Name string `json:"name" validate:"min=2"`
			}{Name: "a"},
			expected: "Use at least 2 characters.",
		},
		"one of": {
			value: struct {
				Kind string `json:"kind" validate:"oneof=one two"`
			}{Kind: "three"},
			expected: "Check this value.",
		},
	}

	for name, test := range tests {
		name := name
		test := test
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			validationErr := validator.New().Struct(test.value)
			var validationErrors validator.ValidationErrors
			require.ErrorAs(t, validationErr, &validationErrors)
			assert.Equal(t, test.expected, formatValidationError(validationErrors[0]))
		})
	}
}
