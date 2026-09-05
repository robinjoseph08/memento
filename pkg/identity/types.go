package identity

// SignInRequest supplies claims from the explicitly enabled development provider.
// Production providers verify their own credentials before calling SignIn.
type SignInRequest struct {
	Subject     string `json:"subject" validate:"required,max=255" mod:"trim"`
	Email       string `json:"email" validate:"required,email,max=254" mod:"trim"`
	DisplayName string `json:"display_name" validate:"required,max=100" mod:"trim"`
}

// Person is the signed-in projection, not a writable database model.
type Person struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	IsCurator   bool   `json:"is_curator"`
}

// Status describes installation claiming and the current browser's identity.
type Status struct {
	Claimed  bool    `json:"claimed"`
	Person   *Person `json:"person"`
	AuthMode string  `json:"auth_mode"`
}
