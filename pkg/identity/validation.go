package identity

import (
	"errors"
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
func (SendInvitationRequest) ValidationMessage(field, _ string) string {
	if field == "preauthorization_id" {
		return "Choose an unused email approval to invite."
	}
	return ""
}
func (ApproveAccessRequestRequest) ValidationMessage(field, rule string) string {
	switch field {
	case "person_id":
		return "Choose an existing person."
	case "display_name":
		return SignInRequest{}.ValidationMessage(field, rule)
	}
	return ""
}
func (LinkFaceRequest) ValidationMessage(field, _ string) string {
	if field == "source_face_id" {
		return "Refresh faces and choose one shown in this Album."
	}
	return ""
}
func (CreatePersonFromFaceRequest) ValidationMessage(field, rule string) string {
	if field == "display_name" {
		return SignInRequest{}.ValidationMessage(field, rule)
	}
	return LinkFaceRequest{}.ValidationMessage(field, rule)
}
func (SetPersonAvatarRequest) ValidationMessage(field, _ string) string {
	if field == "source_face_id" {
		return "Choose one of this Person's linked Immich faces."
	}
	return ""
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

// normalizeClaims admits only a verified, well-formed address and returns it
// lowercased, which is how every Linked Email is stored and compared.
func normalizeClaims(claims Claims) (Claims, error) {
	if !claims.EmailVerified {
		return claims, ErrUnverifiedEmail
	}
	email, err := normalizeEmail(claims.Email)
	if err != nil {
		return claims, ErrUnverifiedEmail
	}
	claims.Email = email
	claims.DisplayName = strings.TrimSpace(claims.DisplayName)
	if claims.DisplayName == "" || len([]rune(claims.DisplayName)) > 100 || strings.ContainsRune(claims.DisplayName, 0) {
		return claims, ErrUnverifiedEmail
	}
	return claims, nil
}

// normalizeEmail accepts one bare address and lowercases it.
func normalizeEmail(value string) (string, error) {
	value = strings.TrimSpace(value)
	parsed, err := mail.ParseAddress(value)
	if err != nil || parsed.Address != value || len(value) > 254 || strings.ContainsRune(value, 0) {
		return "", errors.New("invalid email address")
	}
	return strings.ToLower(value), nil
}
