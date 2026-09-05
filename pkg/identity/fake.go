package identity

// FakeClaims simulates verified provider claims only on explicitly enabled development routes.
// Real provider adapters call the same SignIn use case after verifying their credentials.
func FakeClaims(request SignInRequest) Claims {
	return Claims{Provider: "fake", Subject: request.Subject, Email: request.Email, EmailVerified: true, DisplayName: request.DisplayName}
}
