package media

// SignRequest asks for a URL that a TV can fetch without the session cookie.
// A video is signed as its playback stream and a photo as its large preview.
type SignRequest struct {
	EntryID string `json:"entry_id" validate:"required,max=64"`
	Variant string `json:"variant" validate:"required,oneof=playback preview"`
}

// SignedURL is an absolute address that authorizes on its own for a few hours.
type SignedURL struct {
	URL string `json:"url"`
}
