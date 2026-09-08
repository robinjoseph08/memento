package identity

import "strings"

// FakeClaims simulates verified provider claims only on explicitly enabled development routes.
// Real provider adapters call the same SignIn use case after verifying their credentials.
func FakeClaims(request SignInRequest) Claims {
	email := strings.TrimSpace(request.Email)
	return Claims{Provider: "fake", Subject: strings.ToLower(email), Email: email, EmailVerified: true, DisplayName: request.DisplayName}
}
