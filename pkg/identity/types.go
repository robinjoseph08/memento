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
	AvatarURL             string     `json:"avatar_url"`
}

// PersonSummary is a People list row: the Person plus where they stand on
// signing in, so a Curator can see who still needs an email, an approval, or
// a first visit. Email is the newest linked email, or the newest open
// Preauthorization when nothing is linked yet. Access is "none",
// "approved", "linked", or "onboarded"; the last two mean they can sign in.
type PersonSummary struct {
	Person     `tstype:",extends"`
	Email      string     `json:"email"`
	Access     string     `json:"access"`
	LastSeenAt *time.Time `json:"last_seen_at"`
}

// Status describes installation claiming and the current browser's identity.
// It is public, and the Mobile App checks Version against the oldest server it
// supports before sign-in starts.
type Status struct {
	Claimed  bool    `json:"claimed"`
	Person   *Person `json:"person"`
	AuthMode string  `json:"auth_mode"`
	Version  string  `json:"version"`
}

type CreatePersonRequest struct {
	DisplayName string `json:"display_name" validate:"required,max=100" mod:"trim"`
}

type UpdatePersonRequest struct {
	DisplayName string `json:"display_name" validate:"required,max=100" mod:"trim"`
	IsCurator   bool   `json:"is_curator"`
	Deactivated bool   `json:"deactivated"`
}

type LinkFaceRequest struct {
	SourceFaceID string `json:"source_face_id" validate:"required,max=1024" mod:"trim"`
}

type CreatePersonFromFaceRequest struct {
	DisplayName  string `json:"display_name" validate:"required,max=100" mod:"trim"`
	SourceFaceID string `json:"source_face_id" validate:"required,max=1024" mod:"trim"`
}

type SetPersonAvatarRequest struct {
	SourceFaceID string `json:"source_face_id" validate:"required,max=1024" mod:"trim"`
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

type LinkedFace struct {
	SourceFaceID string `json:"source_face_id"`
	SourceName   string `json:"source_name"`
	ThumbnailURL string `json:"thumbnail_url"`
	ImmichURL    string `json:"immich_url"`
	Avatar       bool   `json:"avatar"`
}

type PersonDetail struct {
	Person            Person             `json:"person"`
	Faces             []LinkedFace       `json:"faces"`
	Identities        []LinkedIdentity   `json:"identities"`
	Preauthorizations []Preauthorization `json:"preauthorizations"`
	Invitations       []Invitation       `json:"invitations"`
	Sessions          []BrowserSession   `json:"sessions"`
	Announced         AnnouncedContent   `json:"announced"`
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

// SendInvitationRequest names the unused Preauthorization the email should describe.
type SendInvitationRequest struct {
	PreauthorizationID string `json:"preauthorization_id" validate:"required,uuid"`
}

// InvitationDelivery is Memento's delivery state; status is queued, sending,
// delivered, failed, or uncertain.
type InvitationDelivery struct {
	Status      string     `json:"status"`
	Attempts    int        `json:"attempts"`
	Message     string     `json:"message"`
	UpdatedAt   time.Time  `json:"updated_at"`
	DeliveredAt *time.Time `json:"delivered_at"`
}

type Invitation struct {
	ID                 string             `json:"id"`
	PreauthorizationID string             `json:"preauthorization_id"`
	Email              string             `json:"email"`
	SentBy             string             `json:"sent_by"`
	CreatedAt          time.Time          `json:"created_at"`
	Delivery           InvitationDelivery `json:"delivery"`
}

// AnnouncedContent counts a Person's notification baseline.
type AnnouncedContent struct {
	Albums  int `json:"albums"`
	Entries int `json:"entries"`
}

// AccessRequest shows what a Curator may inspect. It never asserts that the
// identity and any Person are the same human.
type AccessRequest struct {
	ID string `json:"id"`
	// Kind is join for an unknown identity and album for an existing Person's request.
	Kind          string     `json:"kind"`
	Provider      string     `json:"provider"`
	Email         string     `json:"email"`
	EmailVerified bool       `json:"email_verified"`
	DisplayName   string     `json:"display_name"`
	PersonID      string     `json:"person_id"`
	PersonName    string     `json:"person_name"`
	AlbumID       string     `json:"album_id"`
	AlbumTitle    string     `json:"album_title"`
	Status        string     `json:"status"`
	SignInCount   int        `json:"sign_in_count"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	ResolvedAt    *time.Time `json:"resolved_at"`
	ResolvedBy    string     `json:"resolved_by"`
}

// ApproveAccessRequestRequest links an unknown identity to an existing Person
// or creates one. Requests from an existing Person need neither field.
type ApproveAccessRequestRequest struct {
	PersonID    string `json:"person_id" validate:"omitempty,uuid"`
	DisplayName string `json:"display_name" validate:"omitempty,max=100" mod:"trim"`
}
