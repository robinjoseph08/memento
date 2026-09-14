package notifications

import "time"

// Delivery is the Curator-facing state of one email. Status is one of queued,
// sending, delivered, failed, or uncertain.
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
// VideoTitles lists the announced videos by presentation title; photos are
// only counted.
type NotificationAlbum struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Status      string   `json:"status"`
	PhotoCount  int      `json:"photo_count"`
	VideoCount  int      `json:"video_count"`
	VideoTitles []string `json:"video_titles"`
}

// PreviewRecipient is one collapsible row. EmailEligible reports whether a
// later email delivery could go anywhere; the in-app notification is created
// either way. ReviewToken freezes what the row showed for approval.
type PreviewRecipient struct {
	PersonID      string              `json:"person_id"`
	DisplayName   string              `json:"display_name"`
	UpdateEmail   string              `json:"update_email"`
	EmailUpdates  bool                `json:"email_updates"`
	EmailEligible bool                `json:"email_eligible"`
	Albums        []NotificationAlbum `json:"albums"`
	ReviewToken   string              `json:"review_token"`
}

type Preview struct {
	Recipients []PreviewRecipient `json:"recipients"`
}

// ApproveRecipient names one reviewed row. ExcludedAlbumIDs drops whole Album
// updates from that row; a recipient the Curator excluded is simply omitted.
type ApproveRecipient struct {
	PersonID         string   `json:"person_id" validate:"required,uuid"`
	ReviewToken      string   `json:"review_token" validate:"required"`
	ExcludedAlbumIDs []string `json:"excluded_album_ids" validate:"dive,uuid"`
}

type ApproveRequest struct {
	Note       string             `json:"note" mod:"trim" validate:"max=1000"`
	Recipients []ApproveRecipient `json:"recipients" validate:"required,min=1,dive"`
}

// RecipientResult reports one approval outcome. Status is notified or
// skipped; Message explains a skip in Curator-facing words.
type RecipientResult struct {
	PersonID       string `json:"person_id"`
	DisplayName    string `json:"display_name"`
	Status         string `json:"status"`
	Message        string `json:"message"`
	NotificationID string `json:"notification_id"`
	AlbumCount     int    `json:"album_count"`
	PhotoCount     int    `json:"photo_count"`
	VideoCount     int    `json:"video_count"`
}

type Approval struct {
	Recipients []RecipientResult `json:"recipients"`
}

// Notification is a Person's own approved summary. It renders from stored
// JSON, so it survives title edits and Album deletion; the Album list and
// pages still decide what the Person may open today.
type Notification struct {
	ID        string              `json:"id"`
	CreatedAt time.Time           `json:"created_at"`
	ReadAt    *time.Time          `json:"read_at"`
	Albums    []NotificationAlbum `json:"albums"`
	Note      string              `json:"note"`
}

type NotificationList struct {
	Notifications []Notification `json:"notifications"`
	Unread        int            `json:"unread"`
}
