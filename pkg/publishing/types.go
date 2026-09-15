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

// ViewerLibrary counts each accessible media item once across Albums.
type ViewerLibrary struct {
	PhotoCount int         `json:"photo_count"`
	VideoCount int         `json:"video_count"`
	Days       []ViewerDay `json:"days"`
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

// ViewerDay counts one local capture day. PhotoRatios lists each photo's
// width-to-height ratio in gallery order, truncated to three decimals, so the
// browser can lay out every day's rows before the photos themselves arrive.
type ViewerDay struct {
	Date        string    `json:"date"`
	PhotoCount  int       `json:"photo_count"`
	VideoCount  int       `json:"video_count"`
	PhotoRatios []float64 `json:"photo_ratios" bun:"photo_ratios,array"`
}

// EntryPageRequest selects one page of a gallery. From and To are local
// capture days (YYYY-MM-DD) that bound the page, To exclusive, so a browser
// can load every part of a large Album at once; Cursor continues within
// those bounds.
type EntryPageRequest struct {
	Cursor string
	From   string
	To     string
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

// Album is the Curator's view of one import. Status is queued, processing,
// interrupted, failed, or complete. Ready is true for a complete, unpublished
// Album that at least one Person could see once published.
type Album struct {
	ID          string `json:"id"`
	SourceID    string `json:"source_id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Published   bool   `json:"published"`
	Ready       bool   `json:"ready"`
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
	Album    `tstype:",extends"`
	Moments  []Moment        `json:"moments"`
	Access   []AccessPerson  `json:"access"`
	Excluded []ExcludedEntry `json:"excluded"`
}

// ExcludedEntry is media the Curator keeps out of this Album while it stays
// in the Immich album. It keeps its Album Entry identity for Add back.
type ExcludedEntry struct {
	ID           string `json:"id"`
	Filename     string `json:"filename"`
	Kind         string `json:"kind"`
	CapturedAt   string `json:"captured_at"`
	ThumbnailURL string `json:"thumbnail_url"`
	Available    bool   `json:"available"`
	ExcludedAt   string `json:"excluded_at"`
}

// ExcludeEntriesRequest keeps selected media of one Moment out of the Album.
type ExcludeEntriesRequest struct {
	EntryIDs    []string `json:"entry_ids" validate:"required,min=1,dive,uuid"`
	ReviewToken string   `json:"review_token"`
}

// IncludeEntryRequest returns excluded media to an existing Moment or to a
// new Moment for its capture day, keyed "new:YYYY-MM-DD".
type IncludeEntryRequest struct {
	MomentID    string `json:"moment_id" validate:"required,max=64"`
	ReviewToken string `json:"review_token"`
}

type Moment struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Label        string `json:"label"`
	Date         string `json:"date"`
	EndDate      string `json:"end_date"`
	CoverEntryID string `json:"cover_entry_id"`
	// CoverPosition is the Moment's place in the Album's Cover Order, 0 when
	// it only competes in capture order.
	CoverPosition int          `json:"cover_position"`
	Entries       []Entry      `json:"entries"`
	Access        MomentAccess `json:"access"`
}

// SaveCoverOrderRequest replaces an Album's Cover Order with these Moments in
// this order. An empty list clears it.
type SaveCoverOrderRequest struct {
	MomentIDs []string `json:"moment_ids" validate:"dive,uuid"`
}

// ViewingGroups previews who will see which Album cover. People who can see
// exactly the same Moment covers form one Viewing Group; they are derived on
// every read and never stored. Placeholder lists People with some access but
// no visible cover, who see the neutral placeholder instead.
type ViewingGroups struct {
	Groups      []ViewingGroup  `json:"groups"`
	Placeholder []ViewingPerson `json:"placeholder"`
}

// ViewingGroup is one set of People and the Moments whose covers all of them
// can see, in Album display order. Which of those covers they get follows
// from the Cover Order, so the frontend can preview an unsaved order.
type ViewingGroup struct {
	MomentIDs []string        `json:"moment_ids"`
	People    []ViewingPerson `json:"people"`
}

type ViewingPerson struct {
	PersonID    string `json:"person_id"`
	DisplayName string `json:"display_name"`
	AvatarURL   string `json:"avatar_url"`
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
	// PlaybackURL streams an available video to the Curator; photos have none.
	PlaybackURL string `json:"playback_url"`
	// Chapter fields describe the Media Item's extraction: ChapterStatus is
	// pending, complete, or failed, and ChapterMessage explains a failure.
	Chapters       []Chapter `json:"chapters"`
	ChapterStatus  string    `json:"chapter_status"`
	ChapterMessage string    `json:"chapter_message"`
}

// ChapterFailure is one video whose chapter extraction failed, addressed
// through an Album and Moment so a Curator can open it and retry.
type ChapterFailure struct {
	AlbumID    string `json:"album_id"`
	AlbumTitle string `json:"album_title"`
	MomentID   string `json:"moment_id"`
	EntryID    string `json:"entry_id"`
	Title      string `json:"title"`
	Message    string `json:"message"`
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

// SyncPlacement chooses where one source asset joins the Album. MomentID is an
// existing Moment or a proposed Moment key from the review. Exclude keeps the
// asset out of this Album while it remains in the Immich album.
type SyncPlacement struct {
	SourceID string `json:"source_id" validate:"required,max=1024"`
	MomentID string `json:"moment_id" validate:"max=64"`
	Exclude  bool   `json:"exclude"`
}

// SyncCover replaces a Moment cover that the reviewed removals invalidate.
// EntryID is an Album Entry ID or, for media joining in the same review, the
// placeholder the review lists, so it is bounded like a source ID.
type SyncCover struct {
	MomentID string `json:"moment_id" validate:"required,max=64"`
	EntryID  string `json:"entry_id" validate:"required,max=1100"`
}

// SyncRequest carries the Curator's review decisions. A check accepts it
// without a token; apply requires the token from the matching review.
type SyncRequest struct {
	Placements  []SyncPlacement `json:"placements" validate:"dive"`
	Covers      []SyncCover     `json:"covers" validate:"dive"`
	ReviewToken string          `json:"review_token"`
}

type SyncAlbumRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// SyncMomentOption is a destination: an existing Moment or one the apply
// would create, keyed "new:YYYY-MM-DD".
type SyncMomentOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	New   bool   `json:"new"`
}

// SyncAddition is a source asset the Album does not currently show. Returning
// means a removed Album Entry keeps its identity and announcement history.
// Media the Curator excluded is not listed here; it lives in the Album's
// Excluded section.
type SyncAddition struct {
	SourceID          string         `json:"source_id"`
	Filename          string         `json:"filename"`
	Kind              string         `json:"kind"`
	CapturedAt        string         `json:"captured_at"`
	ThumbnailURL      string         `json:"thumbnail_url"`
	Returning         bool           `json:"returning"`
	SuggestedMomentID string         `json:"suggested_moment_id"`
	MomentID          string         `json:"moment_id"`
	Exclude           bool           `json:"exclude"`
	OtherAlbums       []SyncAlbumRef `json:"other_albums"`
}

// SyncRemoval is an Album Entry whose asset is no longer shown by the Immich
// album. Reason is left_album, trashed, or deleted; a trashed asset returns
// as a returning addition if it is restored from the Immich trash. Excluded
// means the entry was in the Excluded section, which it leaves; Moment
// fields are empty for it.
type SyncRemoval struct {
	EntryID      string `json:"entry_id"`
	Filename     string `json:"filename"`
	Kind         string `json:"kind"`
	CapturedAt   string `json:"captured_at"`
	ThumbnailURL string `json:"thumbnail_url"`
	MomentID     string `json:"moment_id"`
	MomentLabel  string `json:"moment_label"`
	Cover        bool   `json:"cover"`
	Excluded     bool   `json:"excluded"`
	Reason       string `json:"reason"`
}

// SyncChange is a Media Item whose source facts differ. Fields names what
// changed: checksum, capture_time, availability, filename, or details.
// Excluded media is refreshed too so Add back never carries stale facts.
type SyncChange struct {
	EntryID       string         `json:"entry_id"`
	Filename      string         `json:"filename"`
	Kind          string         `json:"kind"`
	ThumbnailURL  string         `json:"thumbnail_url"`
	Excluded      bool           `json:"excluded"`
	Fields        []string       `json:"fields"`
	CapturedAt    string         `json:"captured_at"`
	NewCapturedAt string         `json:"new_captured_at"`
	Available     bool           `json:"available"`
	NewAvailable  bool           `json:"new_available"`
	OtherAlbums   []SyncAlbumRef `json:"other_albums"`
}

type SyncCoverOption struct {
	EntryID      string `json:"entry_id"`
	Filename     string `json:"filename"`
	ThumbnailURL string `json:"thumbnail_url"`
}

// SyncCoverChoice is a surviving Moment whose configured cover is being
// removed. EntryID is the Curator's chosen replacement, empty until chosen.
type SyncCoverChoice struct {
	MomentID    string            `json:"moment_id"`
	MomentLabel string            `json:"moment_label"`
	EntryID     string            `json:"entry_id"`
	Options     []SyncCoverOption `json:"options"`
}

type SyncDescription struct {
	Before string `json:"before"`
	After  string `json:"after"`
}

// SyncReview is the temporary diff between the Album and its Immich album.
// Nothing about it is stored; leaving the review discards it.
type SyncReview struct {
	Ready          bool               `json:"ready"`
	UpToDate       bool               `json:"up_to_date"`
	ReviewToken    string             `json:"review_token"`
	Description    *SyncDescription   `json:"description" tstype:"SyncDescription | null"`
	Additions      []SyncAddition     `json:"additions"`
	Removals       []SyncRemoval      `json:"removals"`
	Changes        []SyncChange       `json:"changes"`
	CoverChoices   []SyncCoverChoice  `json:"cover_choices"`
	RemovedMoments []SyncMomentOption `json:"removed_moments"`
	Moments        []SyncMomentOption `json:"moments"`
	Audience       []AudienceChange   `json:"audience"`
	Blockers       []string           `json:"blockers"`
	FacesRefreshed bool               `json:"faces_refreshed"`
	FacesMessage   string             `json:"faces_message"`
}
