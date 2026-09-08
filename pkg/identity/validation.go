package identity

import (
	"net/mail"
	"strings"

	"github.com/robinjoseph08/memento/pkg/errcodes"
)

// ValidationMessage supplies sign-in guidance for missing fields.
func (SignInRequest) ValidationMessage(field, rule string) string {
	if rule == "required" {
		switch field {
		case "email":
			return "Enter an email address."
		case "display_name":
			return "Enter a display name."
		}
	}
	return ""
}

func (CreatePersonRequest) ValidationMessage(field, rule string) string {
	return SignInRequest{}.ValidationMessage(field, rule)
}
func (UpdatePersonRequest) ValidationMessage(field, rule string) string {
	return SignInRequest{}.ValidationMessage(field, rule)
}
func (UpdateProfileRequest) ValidationMessage(field, rule string) string {
	return SignInRequest{}.ValidationMessage(field, rule)
}
func (PreauthorizeRequest) ValidationMessage(field, rule string) string {
	return SignInRequest{}.ValidationMessage(field, rule)
}

func displayName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fieldError("display_name", "Enter a display name.")
	}
	if len([]rune(value)) > 100 {
		return "", fieldError("display_name", "Use 100 characters or fewer.")
	}
	if strings.ContainsRune(value, 0) {
		return "", fieldError("display_name", "Remove the invalid character.")
	}
	return value, nil
}

func fieldError(field, message string) error {
	return errcodes.ValidationFields("Check the highlighted fields.", map[string]string{field: message})
}

func validateClaims(claims Claims) error {
	if !claims.EmailVerified {
		return ErrUnverifiedIdentity
	}
	email, err := mail.ParseAddress(claims.Email)
	if err != nil || email.Address != claims.Email || len(claims.Email) > 254 {
		return ErrUnverifiedIdentity
	}
	if claims.Provider == "" || strings.TrimSpace(claims.Subject) == "" || len([]rune(claims.Subject)) > 255 || strings.TrimSpace(claims.DisplayName) == "" || len([]rune(claims.DisplayName)) > 100 {
		return ErrUnverifiedIdentity
	}
	for _, value := range []string{claims.Provider, claims.Subject, claims.Email, claims.DisplayName} {
		if strings.ContainsRune(value, 0) {
			return ErrUnverifiedIdentity
		}
	}
	return nil
}
