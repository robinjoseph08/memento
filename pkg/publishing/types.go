package publishing

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
}

type AlbumDetail struct {
	Album   `tstype:",extends"`
	Moments []Moment `json:"moments"`
}

type Moment struct {
	ID           string  `json:"id"`
	Label        string  `json:"label"`
	Date         string  `json:"date"`
	CoverEntryID string  `json:"cover_entry_id"`
	Entries      []Entry `json:"entries"`
}

type Entry struct {
	ID           string `json:"id"`
	MediaID      string `json:"media_id"`
	Filename     string `json:"filename"`
	Kind         string `json:"kind"`
	CapturedAt   string `json:"captured_at"`
	Available    bool   `json:"available"`
	ThumbnailURL string `json:"thumbnail_url"`
}

type ImportRequest struct {
	SourceID string `json:"source_id" validate:"required,max=1024"`
}

type UpdateAlbumRequest struct {
	Title string `json:"title" mod:"trim" validate:"required,max=200"`
}
