package immich

// Connection reports a read-only diagnostic without upstream bodies or secrets.
type Connection struct {
	Usable          bool   `json:"usable"`
	Version         string `json:"version"`
	Message         string `json:"message"`
	ImportSupported *bool  `json:"import_supported,omitempty"`
}

// Face is an Immich asset face and its assigned person. ID is empty when the
// face is unassigned; FaceID always identifies the individual face record.
// UpdatedAt is the person's last change in Immich, which moves when its
// featured photo changes, so it versions the person thumbnail.
type Face struct {
	FaceID        string `json:"faceId"`
	ID            string `json:"id"`
	Name          string `json:"name"`
	ThumbnailPath string `json:"thumbnailPath"`
	UpdatedAt     string `json:"updatedAt"`
	Hidden        bool   `json:"isHidden"`
	ImageHeight   int    `json:"imageHeight"`
	ImageWidth    int    `json:"imageWidth"`
	BoundingBoxX1 int    `json:"boundingBoxX1"`
	BoundingBoxX2 int    `json:"boundingBoxX2"`
	BoundingBoxY1 int    `json:"boundingBoxY1"`
	BoundingBoxY2 int    `json:"boundingBoxY2"`
	SourceType    string `json:"sourceType"`
}
