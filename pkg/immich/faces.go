package immich

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/robinjoseph08/memento/pkg/errcodes"
)

const maxSafeInteger int64 = 9007199254740991

type faceResponse struct {
	FaceID        string          `json:"id"`
	ImageHeight   int             `json:"imageHeight"`
	ImageWidth    int             `json:"imageWidth"`
	BoundingBoxX1 int             `json:"boundingBoxX1"`
	BoundingBoxX2 int             `json:"boundingBoxX2"`
	BoundingBoxY1 int             `json:"boundingBoxY1"`
	BoundingBoxY2 int             `json:"boundingBoxY2"`
	SourceType    string          `json:"sourceType"`
	Person        *personResponse `json:"person"`
}

type personResponse struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	BirthDate     *string `json:"birthDate"`
	ThumbnailPath string  `json:"thumbnailPath"`
	Hidden        bool    `json:"isHidden"`
	UpdatedAt     *string `json:"updatedAt"`
	Favorite      *bool   `json:"isFavorite"`
	Color         *string `json:"color"`
}

// ListFaces returns the Immich face and person records associated with an asset.
func (c *Client) ListFaces(ctx context.Context, assetID string) ([]Face, error) {
	if assetID == "" {
		return nil, errcodes.ValidationError("An Immich asset ID is required.")
	}
	var documents []json.RawMessage
	if err := c.json(ctx, http.MethodGet, "/api/faces?"+url.Values{"id": {assetID}}.Encode(), nil, &documents, "face.read"); err != nil {
		return nil, err
	}
	if documents == nil {
		return nil, unreadable("face list")
	}
	faces := make([]Face, 0, len(documents))
	seen := make(map[string]bool, len(documents))
	for _, document := range documents {
		face, err := decodeFace(document)
		if err != nil {
			return nil, err
		}
		if seen[face.FaceID] {
			return nil, unreadable("duplicate face")
		}
		seen[face.FaceID] = true
		faces = append(faces, face)
	}
	return faces, nil
}

func decodeFace(data json.RawMessage) (Face, error) {
	var response faceResponse
	fields, ok := objectFields(data, &response, "id", "imageHeight", "imageWidth", "boundingBoxX1", "boundingBoxX2", "boundingBoxY1", "boundingBoxY2")
	if !ok || len(fields["person"]) == 0 || response.FaceID == "" || response.ImageHeight < 0 || int64(response.ImageHeight) > maxSafeInteger || response.ImageWidth < 0 || int64(response.ImageWidth) > maxSafeInteger || !safeCoordinate(response.BoundingBoxX1) || !safeCoordinate(response.BoundingBoxX2) || !safeCoordinate(response.BoundingBoxY1) || !safeCoordinate(response.BoundingBoxY2) {
		return Face{}, unreadable("face metadata")
	}
	if source, exists := fields["sourceType"]; exists {
		if string(source) == "null" || (response.SourceType != "machine-learning" && response.SourceType != "exif" && response.SourceType != "manual") {
			return Face{}, unreadable("face metadata")
		}
	}
	face := Face{
		FaceID: response.FaceID, ImageHeight: response.ImageHeight, ImageWidth: response.ImageWidth,
		BoundingBoxX1: response.BoundingBoxX1, BoundingBoxX2: response.BoundingBoxX2,
		BoundingBoxY1: response.BoundingBoxY1, BoundingBoxY2: response.BoundingBoxY2, SourceType: response.SourceType,
	}
	if string(fields["person"]) != "null" {
		person, err := decodePerson(fields["person"])
		if err != nil {
			return Face{}, err
		}
		face.ID, face.Name, face.ThumbnailPath, face.Hidden = person.ID, person.Name, person.ThumbnailPath, person.Hidden
		if person.UpdatedAt != nil {
			face.UpdatedAt = *person.UpdatedAt
		}
	}
	return face, nil
}

func decodePerson(data json.RawMessage) (personResponse, error) {
	var person personResponse
	fields, ok := objectFields(data, &person, "id", "name", "thumbnailPath", "isHidden")
	if !ok || person.ID == "" || len(fields["birthDate"]) == 0 {
		return personResponse{}, unreadable("person metadata")
	}
	if person.BirthDate != nil {
		parsed, err := time.Parse("2006-01-02", *person.BirthDate)
		if err != nil || parsed.Format("2006-01-02") != *person.BirthDate {
			return personResponse{}, unreadable("person metadata")
		}
	}
	if person.UpdatedAt != nil && !timestamp(*person.UpdatedAt) {
		return personResponse{}, unreadable("person metadata")
	}
	for _, field := range []string{"updatedAt", "isFavorite", "color"} {
		if value, exists := fields[field]; exists && string(value) == "null" {
			return personResponse{}, unreadable("person metadata")
		}
	}
	return person, nil
}

func safeCoordinate(value int) bool {
	return int64(value) >= -maxSafeInteger && int64(value) <= maxSafeInteger
}
