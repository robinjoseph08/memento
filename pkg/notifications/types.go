package notifications

import "time"

// Delivery is the Curator-facing state of one email. Status is one of queued,
// sending, delivered, failed, uncertain, or skipped.
type Delivery struct {
	ID          string     `json:"id"`
	Status      string     `json:"status"`
	Attempts    int        `json:"attempts"`
	Message     string     `json:"message"`
	UpdatedAt   time.Time  `json:"updated_at"`
	DeliveredAt *time.Time `json:"delivered_at"`
}

// Baseline counts the content announced to a Person so far.
type Baseline struct {
	Albums  int `json:"albums"`
	Entries int `json:"entries"`
}

// NotificationAlbum is one Album inside an approved summary. Status is new
// when the Album had never been announced to the Person, otherwise updated.
// In the "New albums you can view" section it is always offered, and the
// counts cover what is offered to them. Photos and videos are only counted,
// never listed.
type NotificationAlbum struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Status     string `json:"status"`
	PhotoCount int    `json:"photo_count"`
	VideoCount int    `json:"video_count"`
}

// PreviewPerson is one collapsible row. EmailEligible reports whether a
// later email delivery could go anywhere; the Update Notification is created
// either way. Albums are changes to the Person's own Albums. OfferedAlbums
// are every one of their "New albums you can view", newest first, so the
// Curator can leave any of them out; the notification itself shows only the
// newest few. ReviewToken freezes what the row showed for approval.
type PreviewPerson struct {
	PersonID      string              `json:"person_id"`
	DisplayName   string              `json:"display_name"`
	UpdateEmail   string              `json:"update_email"`
	EmailUpdates  bool                `json:"email_updates"`
	EmailEligible bool                `json:"email_eligible"`
	Albums        []NotificationAlbum `json:"albums"`
	OfferedAlbums []NotificationAlbum `json:"offered_albums"`
	ReviewToken   string              `json:"review_token"`
}

// Preview is the batch a Curator reviews. EmailConfigured is false when the
// installation has no SMTP settings, so every row is in app only.
type Preview struct {
	People          []PreviewPerson `json:"people"`
	EmailConfigured bool            `json:"email_configured"`
}

// MailStatus is the Settings diagnostic for email. Configured is false when
// the installation has no SMTP settings, which is a quiet state rather than a
// failure. Usable is true once the mail server accepted a connection and any
// sign-in. Sender is the configured From address; the server address and
// credentials are never reported.
type MailStatus struct {
	Configured bool   `json:"configured"`
	Usable     bool   `json:"usable"`
	Sender     string `json:"sender"`
	Message    string `json:"message"`
}

// ApprovePerson names one reviewed row. ExcludedAlbumIDs drops whole Album
// updates from that row, and ExcludedOfferedAlbumIDs drops Albums from its
// "New albums you can view"; a Person the Curator left out is simply omitted.
type ApprovePerson struct {
	PersonID                string   `json:"person_id" validate:"required,uuid"`
	ReviewToken             string   `json:"review_token" validate:"required"`
	ExcludedAlbumIDs        []string `json:"excluded_album_ids" validate:"dive,uuid"`
	ExcludedOfferedAlbumIDs []string `json:"excluded_offered_album_ids" validate:"dive,uuid"`
}

type ApproveRequest struct {
	Note   string          `json:"note" mod:"trim" validate:"max=1000"`
	People []ApprovePerson `json:"people" validate:"required,min=1,dive"`
}

// DismissRequest selects reviewed changes to add to the baseline silently.
type DismissRequest struct {
	People []ApprovePerson `json:"people" validate:"required,min=1,dive"`
}

// PersonResult reports one review outcome. Status is notified, dismissed, or
// skipped; Message explains a skip in Curator-facing words. Email and Delivery
// are set only when an email was queued. AlbumCount and the media counts
// cover the Person's own Albums; OfferedAlbumCount counts every Album
// announced as new to view.
type PersonResult struct {
	PersonID          string    `json:"person_id"`
	DisplayName       string    `json:"display_name"`
	Status            string    `json:"status"`
	Message           string    `json:"message"`
	NotificationID    string    `json:"notification_id"`
	AlbumCount        int       `json:"album_count"`
	PhotoCount        int       `json:"photo_count"`
	VideoCount        int       `json:"video_count"`
	OfferedAlbumCount int       `json:"offered_album_count"`
	Email             string    `json:"email"`
	Delivery          *Delivery `json:"delivery"`
}

// DeliveryWork is one email a Curator may need to watch or act on. PersonID
// and PersonName are set for update and Invitation email; InvitationID is set
// for Invitations, whose retry lives on the Person page.
type DeliveryWork struct {
	Delivery     `tstype:",extends"`
	Kind         string `json:"kind"`
	Recipient    string `json:"recipient"`
	PersonID     string `json:"person_id"`
	PersonName   string `json:"person_name"`
	InvitationID string `json:"invitation_id"`
}

// UnsubscribeStatus describes an unsubscribe link's owner. Subscribed is
// false once update email is off, or when no destination is selected.
type UnsubscribeStatus struct {
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	Subscribed  bool   `json:"subscribed"`
}

// Approval reports the outcome of sending or dismissing reviewed updates.
type Approval struct {
	People []PersonResult `json:"people"`
}

// Notification is a Person's own approved summary. It renders from stored
// JSON, so it survives title edits and Album deletion; the Album list and
// pages still decide what the Person may open today. OfferedAlbums and
// MoreOfferedAlbums are its "New albums you can view": the newest few and how
// many more there were.
type Notification struct {
	ID                string              `json:"id"`
	CreatedAt         time.Time           `json:"created_at"`
	ReadAt            *time.Time          `json:"read_at"`
	Albums            []NotificationAlbum `json:"albums"`
	OfferedAlbums     []NotificationAlbum `json:"offered_albums"`
	MoreOfferedAlbums int                 `json:"more_offered_albums"`
	Note              string              `json:"note"`
}

type NotificationList struct {
	Notifications []Notification `json:"notifications"`
	Unread        int            `json:"unread"`
}
