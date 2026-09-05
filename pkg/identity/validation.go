package identity

import (
	"net/mail"
	"strings"
)

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
