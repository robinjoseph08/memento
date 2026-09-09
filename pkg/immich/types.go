package immich

// Connection reports a read-only diagnostic without upstream bodies or secrets.
type Connection struct {
	Usable          bool   `json:"usable"`
	Version         string `json:"version"`
	Message         string `json:"message"`
	ImportSupported *bool  `json:"import_supported,omitempty"`
}
