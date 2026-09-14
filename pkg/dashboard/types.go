package dashboard

import "time"

// Dashboard is the Curator's focused work list. Active reports that an import
// or an email is still running, so the browser polls only then. Chapter
// probes report through the Album page, which polls itself.
type Dashboard struct {
	NeedsAttention Attention `json:"needs_attention"`
	Ready          Ready     `json:"ready"`
	Active         bool      `json:"active"`
}

// Attention groups actual failures and decisions: nothing here is a choice
// the Curator made to wait.
type Attention struct {
	PendingRequests int           `json:"pending_requests"`
	Imports         []AlbumWork   `json:"imports"`
	Deliveries      []Delivery    `json:"deliveries"`
	Chapters        []ChapterWork `json:"chapters"`
}

// Ready groups work the Curator may finish whenever they like. Neither entry
// is a failure.
type Ready struct {
	Unpublished       []AlbumWork `json:"unpublished"`
	UnannouncedPeople int         `json:"unannounced_people"`
}

// AlbumWork is one Album with its import status as Publishing reports it.
// Ready is true when publishing would show the Album to at least one Person.
type AlbumWork struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Status  string `json:"status"`
	Message string `json:"message"`
	Ready   bool   `json:"ready"`
}

// Delivery is one failed or uncertain email. Update and alert email retry
// from here; an Invitation carries its InvitationID and retries from the
// Person page, which also checks that the Person may still be invited.
type Delivery struct {
	ID           string    `json:"id"`
	Kind         string    `json:"kind"`
	Recipient    string    `json:"recipient"`
	Status       string    `json:"status"`
	Message      string    `json:"message"`
	Attempts     int       `json:"attempts"`
	UpdatedAt    time.Time `json:"updated_at"`
	PersonID     string    `json:"person_id"`
	PersonName   string    `json:"person_name"`
	InvitationID string    `json:"invitation_id"`
}

// ChapterWork is one video whose chapter extraction failed, addressed so the
// Curator can open it in its Album and retry.
type ChapterWork struct {
	AlbumID    string `json:"album_id"`
	AlbumTitle string `json:"album_title"`
	MomentID   string `json:"moment_id"`
	EntryID    string `json:"entry_id"`
	Title      string `json:"title"`
	Message    string `json:"message"`
}
