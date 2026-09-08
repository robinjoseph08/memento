package identity

import "time"

// SignInRequest supplies claims from the explicitly enabled development provider.
// Production providers verify their own credentials before calling SignIn.
type SignInRequest struct {
	Email       string `json:"email" validate:"required,email,max=254" mod:"trim"`
	DisplayName string `json:"display_name" validate:"required,max=100" mod:"trim"`
}

// Person is a public projection, not a writable database model.
type Person struct {
	ID                    string     `json:"id"`
	DisplayName           string     `json:"display_name"`
	IsCurator             bool       `json:"is_curator"`
	OnboardingCompletedAt *time.Time `json:"onboarding_completed_at"`
	DeactivatedAt         *time.Time `json:"deactivated_at"`
	UpdateEmail           string     `json:"update_email"`
	EmailUpdates          bool       `json:"email_updates"`
}

// Status describes installation claiming and the current browser's identity.
type Status struct {
	Claimed  bool    `json:"claimed"`
	Person   *Person `json:"person"`
	AuthMode string  `json:"auth_mode"`
}

type CreatePersonRequest struct {
	DisplayName string `json:"display_name" validate:"required,max=100" mod:"trim"`
}

type UpdatePersonRequest struct {
	DisplayName string `json:"display_name" validate:"required,max=100" mod:"trim"`
	IsCurator   bool   `json:"is_curator"`
	Deactivated bool   `json:"deactivated"`
}

type PreauthorizeRequest struct {
	Email string `json:"email" validate:"required,email,max=254" mod:"trim"`
}

type UpdateProfileRequest struct {
	DisplayName  string `json:"display_name" validate:"required,max=100" mod:"trim"`
	UpdateEmail  string `json:"update_email" validate:"omitempty,email,max=254"`
	EmailUpdates bool   `json:"email_updates"`
}

type LinkedIdentity struct {
	ID        string    `json:"id"`
	Provider  string    `json:"provider"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
}

type Preauthorization struct {
	ID         string     `json:"id"`
	Email      string     `json:"email"`
	CreatedAt  time.Time  `json:"created_at"`
	ConsumedAt *time.Time `json:"consumed_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}

type PersonDetail struct {
	Person            Person             `json:"person"`
	Identities        []LinkedIdentity   `json:"identities"`
	Preauthorizations []Preauthorization `json:"preauthorizations"`
}

type Profile struct {
	Person     Person           `json:"person"`
	Identities []LinkedIdentity `json:"identities"`
}

// BrowserSession exposes no credential or token hash.
type BrowserSession struct {
	ID         string    `json:"id"`
	IdentityID string    `json:"identity_id"`
	Email      string    `json:"email"`
	Device     string    `json:"device"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Current    bool      `json:"current"`
}
