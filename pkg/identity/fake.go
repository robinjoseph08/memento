package identity

import "strings"

// FakeClaims treats a typed address as verified, only on explicitly enabled
// development routes. Real adapters call the same SignIn after verifying it.
func FakeClaims(request SignInRequest) Claims {
	return Claims{Email: strings.TrimSpace(request.Email), EmailVerified: true, DisplayName: request.DisplayName}
}
