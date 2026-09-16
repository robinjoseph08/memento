package ffprobe

// Status is the Settings diagnostic for chapter extraction. Version is what
// the binary reports; the configured path and process output stay out of it.
type Status struct {
	Usable  bool   `json:"usable"`
	Version string `json:"version"`
	Message string `json:"message"`
}
