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
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	PhotoCount  int    `json:"photo_count"`
	VideoCount  int    `json:"video_count"`
	StartDate   string `json:"start_date"`
	EndDate     string `json:"end_date"`
	CoverURL    string `json:"cover_url"`
	// CoverPreviewURL is the same cover at Immich's larger variant for the
	// Album header; list cards keep the small thumbnail.
	CoverPreviewURL string      `json:"cover_preview_url"`
	Days            []ViewerDay `json:"days"`
}

type ViewerDay struct {
	Date       string `json:"date"`
	PhotoCount int    `json:"photo_count"`
	VideoCount int    `json:"video_count"`
}

// ViewerEntry is one gallery item. Title is the Curator's video title when set,
// otherwise the original filename without its extension.
type ViewerEntry struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	Title        string `json:"title"`
	CapturedAt   string `json:"captured_at"`
	Available    bool   `json:"available"`
	ThumbnailURL string `json:"thumbnail_url"`
	PreviewURL   string `json:"preview_url"`
	// DownloadURL streams the original photo or video. It is empty in Curator
	// preview and for unavailable media.
	DownloadURL string `json:"download_url"`
	// PlaybackURL is the ranged video stream, also in preview. Photos have none.
	PlaybackURL string `json:"playback_url"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	// Chapters are read-only navigation marks. ChapterStatus is pending,
	// complete, or failed; photos leave both empty.
	Chapters      []Chapter `json:"chapters"`
	ChapterStatus string    `json:"chapter_status"`
}

// Chapter is one read-only navigation segment, in seconds from the start.
type Chapter struct {
	Title string  `json:"title"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

type ViewerPage struct {
	Entries    []ViewerEntry `json:"entries"`
	NextCursor string        `json:"next_cursor"`
}

type PublicationAudience struct {
	PersonID        string `json:"person_id"`
	DisplayName     string `json:"display_name"`
	AvatarURL       string `json:"avatar_url"`
	AccessibleCount int    `json:"accessible_count"`
}

// PublicationReview lists what publication would expose and why it may wait.
// Warnings never block; blockers do.
type PublicationReview struct {
	Title       string                `json:"title"`
	PhotoCount  int                   `json:"photo_count"`
	VideoCount  int                   `json:"video_count"`
	MomentCount int                   `json:"moment_count"`
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
	ID           string `json:"id"`
	MediaID      string `json:"media_id"`
	Filename     string `json:"filename"`
	Kind         string `json:"kind"`
	CapturedAt   string `json:"captured_at"`
	Available    bool   `json:"available"`
	ThumbnailURL string `json:"thumbnail_url"`
	// Decisions holds only this entry's explicit rules by Person ID.
	Decisions map[string]Decision `json:"decisions"`
	// Title is the global video title override, empty when the filename shows.
	Title string `json:"title"`
	// Chapter fields describe the Media Item's extraction: ChapterStatus is
	// pending, complete, or failed, and ChapterMessage explains a failure.
	Chapters       []Chapter `json:"chapters"`
	ChapterStatus  string    `json:"chapter_status"`
	ChapterMessage string    `json:"chapter_message"`
}

// UpdateVideoRequest sets or, when blank, clears a video's global title.
type UpdateVideoRequest struct {
	Title string `json:"title" mod:"trim" validate:"max=200"`
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

// AccessPerson summarizes one Person at one scope. At Album scope Decision is
// the Album allow, Exceptions counts narrower rules, and MomentsDetected says
// how many Moments recognized them; at Moment scope Inherited reports an Album
// allow and Decision the Moment rule.
type AccessPerson struct {
	PersonID          string   `json:"person_id"`
	DisplayName       string   `json:"display_name"`
	AvatarURL         string   `json:"avatar_url"`
	Decision          Decision `json:"decision"`
	Detected          bool     `json:"detected"`
	Suggested         bool     `json:"suggested"`
	SupportingEntries int      `json:"supporting_entries"`
	MomentsDetected   int      `json:"moments_detected"`
	Inherited         bool     `json:"inherited"`
	Effective         bool     `json:"effective"`
	AccessibleCount   int      `json:"accessible_count"`
	Exceptions        int      `json:"exceptions"`
	// Deactivated people appear at Album scope only while they still hold
	// rules here, so a Curator can see and remove frozen access.
	Deactivated bool `json:"deactivated"`
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

type AlbumAccessChoice struct {
	PersonID string `json:"person_id" validate:"required,uuid"`
	Allowed  bool   `json:"allowed"`
}

type SaveAlbumAccessRequest struct {
	People []AlbumAccessChoice `json:"people" validate:"required,dive"`
}

type AlbumAccessPreview struct {
	Changes []AudienceChange `json:"changes"`
}

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
	PersonID    string           `json:"person_id"`
	DisplayName string           `json:"display_name"`
	Changes     []AudienceChange `json:"changes"`
	ReviewToken string           `json:"review_token"`
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
