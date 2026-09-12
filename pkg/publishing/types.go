package publishing

import "time"

// Decision is an explicit access choice. Missing decisions inherit.
type Decision string

const (
	DecisionAllow   Decision = "allow"
	DecisionDeny    Decision = "deny"
	DecisionInherit Decision = "inherit"
)

// AudienceChange describes the Album Entries whose effective access changes.
type AudienceChange struct {
	PersonID       string   `json:"person_id"`
	DisplayName    string   `json:"display_name"`
	GainedEntryIDs []string `json:"gained_entry_ids"`
	LostEntryIDs   []string `json:"lost_entry_ids"`
}

// ViewerAlbum contains only the selected Person's accessible presentation.
type ViewerAlbum struct {
	ID          string      `json:"id"`
	Title       string      `json:"title"`
	Description string      `json:"description"`
	PhotoCount  int         `json:"photo_count"`
	VideoCount  int         `json:"video_count"`
	StartDate   string      `json:"start_date"`
	EndDate     string      `json:"end_date"`
	CoverURL    string      `json:"cover_url"`
	Days        []ViewerDay `json:"days"`
}

type ViewerDay struct {
	Date       string `json:"date"`
	PhotoCount int    `json:"photo_count"`
	VideoCount int    `json:"video_count"`
}

type ViewerEntry struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	Title        string `json:"title"`
	CapturedAt   string `json:"captured_at"`
	Available    bool   `json:"available"`
	ThumbnailURL string `json:"thumbnail_url"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
}

type ViewerPage struct {
	Entries    []ViewerEntry `json:"entries"`
	NextCursor string        `json:"next_cursor"`
}

type PublicationAudience struct {
	PersonID    string `json:"person_id"`
	DisplayName string `json:"display_name"`
	PhotoCount  int    `json:"photo_count"`
	VideoCount  int    `json:"video_count"`
}

type PublicationReview struct {
	Title       string                `json:"title"`
	PhotoCount  int                   `json:"photo_count"`
	VideoCount  int                   `json:"video_count"`
	Audience    []PublicationAudience `json:"audience"`
	Blockers    []string              `json:"blockers"`
	Warnings    []string              `json:"warnings"`
	ReviewToken string                `json:"review_token"`
}

type PublishRequest struct {
	ReviewToken string `json:"review_token" validate:"required"`
}

type DeleteAlbumRequest struct {
	Title string `json:"title" validate:"required"`
}

// SourceAlbum describes an Immich Album available for import.
type SourceAlbum struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Count       int    `json:"count"`
	StartDate   string `json:"start_date"`
	EndDate     string `json:"end_date"`
	CoverURL    string `json:"cover_url"`
	AlbumID     string `json:"album_id"`
}

type SourcePage struct {
	Albums []SourceAlbum `json:"albums"`
	Page   int           `json:"page"`
	Pages  int           `json:"pages"`
	Total  int           `json:"total"`
}

type Album struct {
	ID          string `json:"id"`
	SourceID    string `json:"source_id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Published   bool   `json:"published"`
	Status      string `json:"status"`
	Message     string `json:"message"`
	Processed   int    `json:"processed"`
	Total       int    `json:"total"`
	PhotoCount  int    `json:"photo_count"`
	VideoCount  int    `json:"video_count"`
	StartDate   string `json:"start_date"`
	EndDate     string `json:"end_date"`
	CoverURL    string `json:"cover_url"`
}

type AlbumDetail struct {
	Album   `tstype:",extends"`
	Moments []Moment       `json:"moments"`
	Access  []AccessPerson `json:"access"`
}

type Moment struct {
	ID           string       `json:"id"`
	Title        string       `json:"title"`
	Label        string       `json:"label"`
	Date         string       `json:"date"`
	EndDate      string       `json:"end_date"`
	CoverEntryID string       `json:"cover_entry_id"`
	Entries      []Entry      `json:"entries"`
	Access       MomentAccess `json:"access"`
}

type Entry struct {
	ID           string         `json:"id"`
	MediaID      string         `json:"media_id"`
	Filename     string         `json:"filename"`
	Kind         string         `json:"kind"`
	CapturedAt   string         `json:"captured_at"`
	Available    bool           `json:"available"`
	ThumbnailURL string         `json:"thumbnail_url"`
	Access       []AccessPerson `json:"access"`
}

type ImportRequest struct {
	SourceID string `json:"source_id" validate:"required,max=1024"`
}

type UpdateAlbumRequest struct {
	Title string `json:"title" mod:"trim" validate:"required,max=200"`
}

type UpdateMomentRequest struct {
	Title string `json:"title" mod:"trim" validate:"max=200"`
}

type SetMomentCoverRequest struct {
	EntryID string `json:"entry_id" validate:"required,uuid"`
}

type AccessPerson struct {
	PersonID          string   `json:"person_id"`
	DisplayName       string   `json:"display_name"`
	AvatarURL         string   `json:"avatar_url"`
	Decision          Decision `json:"decision"`
	Detected          bool     `json:"detected"`
	Suggested         bool     `json:"suggested"`
	SupportingEntries int      `json:"supporting_entries"`
	Inherited         bool     `json:"inherited"`
	Effective         bool     `json:"effective"`
	AccessibleCount   int      `json:"accessible_count"`
	ExcludedCount     int      `json:"excluded_count"`
}

type MomentAccess struct {
	People      []AccessPerson `json:"people"`
	Faces       []FaceRecord   `json:"faces"`
	RefreshedAt *time.Time     `json:"refreshed_at"`
}

type FaceRecord struct {
	SourceID     string `json:"source_id"`
	SourceName   string `json:"source_name"`
	ThumbnailURL string `json:"thumbnail_url"`
	ImmichURL    string `json:"immich_url"`
	PersonID     string `json:"person_id"`
	PersonName   string `json:"person_name"`
	Ignored      bool   `json:"ignored"`
	Occurrences  int    `json:"occurrences"`
}

type SetAlbumAccessRequest struct {
	PersonID string   `json:"person_id" validate:"required,uuid"`
	Decision Decision `json:"decision" validate:"required,oneof=allow inherit"`
}

type SetMomentAccessRequest struct {
	PersonID string   `json:"person_id" validate:"required,uuid"`
	Decision Decision `json:"decision" validate:"required,oneof=allow deny inherit"`
}

type SetEntryAccessRequest struct {
	PersonID string   `json:"person_id" validate:"required,uuid"`
	Decision Decision `json:"decision" validate:"required,oneof=allow deny inherit"`
}

type UndoAccessChange struct {
	PersonID         string    `json:"person_id"`
	Current          Decision  `json:"current"`
	Previous         Decision  `json:"previous"`
	CurrentUpdatedAt time.Time `json:"current_updated_at"`
}

type UndoMomentAccessRequest struct {
	Changes []UndoAccessChange `json:"changes"`
}

type MomentAccessResult struct {
	Album AlbumDetail             `json:"album"`
	Undo  UndoMomentAccessRequest `json:"undo"`
}

type AccessResult = MomentAccessResult

type SaveRulesRequest struct {
	Decisions []AccessResolution `json:"decisions" validate:"required,dive"`
}

type RemoveAccessPreviewRequest struct {
	PersonID string `json:"person_id" validate:"required,uuid"`
}

type RemoveAccessRequest struct {
	PersonID    string `json:"person_id" validate:"required,uuid"`
	ReviewToken string `json:"review_token" validate:"required"`
}

type RemoveAccessPreview struct {
	PersonID        string `json:"person_id"`
	DisplayName     string `json:"display_name"`
	AlbumDecisions  int    `json:"album_decisions"`
	MomentDecisions int    `json:"moment_decisions"`
	EntryDecisions  int    `json:"entry_decisions"`
	AccessibleCount int    `json:"accessible_count"`
	ReviewToken     string `json:"review_token"`
}

type AccessConflict struct {
	PersonID    string   `json:"person_id"`
	DisplayName string   `json:"display_name"`
	Source      Decision `json:"source"`
	Target      Decision `json:"target"`
}

type AccessResolution struct {
	PersonID string   `json:"person_id" validate:"required,uuid"`
	Decision Decision `json:"decision" validate:"required,oneof=allow deny inherit"`
}

type StructurePreview struct {
	Ready         bool             `json:"ready"`
	ReviewToken   string           `json:"review_token"`
	RemovesMoment bool             `json:"removes_moment"`
	Changes       []AudienceChange `json:"changes"`
	Conflicts     []AccessConflict `json:"conflicts"`
}

// Moves and splits pick covers themselves: a Moment that loses its cover, or a
// Moment created by a split, starts with its earliest item. Curators change
// covers afterwards from the Moment itself.
type MoveEntriesRequest struct {
	EntryIDs            []string `json:"entry_ids" validate:"required,min=1,dive,uuid"`
	DestinationMomentID string   `json:"destination_moment_id" validate:"required,uuid"`
	ReviewToken         string   `json:"review_token"`
}

type SplitMomentRequest struct {
	EntryIDs    []string `json:"entry_ids" validate:"required,min=1,dive,uuid"`
	NewTitle    string   `json:"new_title" mod:"trim" validate:"max=200"`
	ReviewToken string   `json:"review_token"`
}

type MergeMomentsRequest struct {
	TargetMomentID string             `json:"target_moment_id" validate:"required,uuid"`
	Title          string             `json:"title" mod:"trim" validate:"max=200"`
	CoverEntryID   string             `json:"cover_entry_id" validate:"required,uuid"`
	Resolutions    []AccessResolution `json:"resolutions" validate:"dive"`
	ReviewToken    string             `json:"review_token"`
}
