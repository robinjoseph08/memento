package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

type apiSchema struct {
	Ref        string               `json:"$ref"`
	Type       string               `json:"type"`
	Properties map[string]apiSchema `json:"properties"`
	Required   []string             `json:"required"`
	AllOf      []apiSchema          `json:"allOf"`
	Items      *apiSchema           `json:"items"`
}
type apiOperation struct {
	Responses map[string]struct {
		Content map[string]struct {
			Schema apiSchema `json:"schema"`
		} `json:"content"`
	} `json:"responses"`
	Parameters []struct {
		Name     string `json:"name"`
		Required bool   `json:"required"`
	} `json:"parameters"`
}

// CheckOpenAPI checks the operations and wire types consumed by the shipped
// adapter. It is deliberately not an SDK generator or a complete OpenAPI diff.
// Live assertions remain necessary for permissions, semantics, and byte ranges.
func CheckOpenAPI(reader io.Reader, release string) error {
	var doc struct {
		OpenAPI string `json:"openapi"`
		Info    struct {
			Version string `json:"version"`
		} `json:"info"`
		Paths      map[string]map[string]apiOperation `json:"paths"`
		Components struct {
			Schemas map[string]apiSchema `json:"schemas"`
		} `json:"components"`
	}
	if err := json.NewDecoder(io.LimitReader(reader, 8<<20)).Decode(&doc); err != nil {
		return fmt.Errorf("OpenAPI: invalid document")
	}
	if !strings.HasPrefix(doc.OpenAPI, "3.") || "v"+doc.Info.Version != release {
		return fmt.Errorf("OpenAPI: document version does not match %s", release)
	}
	var problems []error
	for _, endpoint := range []struct{ path, method, schema string }{
		{"/server/version", "get", "ServerVersionResponseDto"}, {"/albums", "get", "[]AlbumResponseDto"}, {"/albums/{id}", "get", "AlbumResponseDto"}, {"/search/metadata", "post", "SearchResponseDto"}, {"/assets/{id}", "get", "AssetResponseDto"},
		{"/faces", "get", "[]AssetFaceResponseDto"}, {"/people/{id}/thumbnail", "get", "binary"}, {"/assets/{id}/thumbnail", "get", "binary"}, {"/assets/{id}/original", "get", "binary"}, {"/assets/{id}/video/playback", "get", "binary"},
	} {
		op, ok := doc.Paths[endpoint.path][endpoint.method]
		if !ok || len(op.Responses["200"].Content) == 0 {
			problems = append(problems, fmt.Errorf("OpenAPI: missing %s %s success response", endpoint.method, endpoint.path))
			continue
		}
		response := op.Responses["200"].Content["application/json"].Schema
		want := endpoint.schema
		if strings.HasPrefix(want, "[]") && response.Type == "array" && response.Items != nil {
			response = *response.Items
			want = strings.TrimPrefix(want, "[]")
		}
		if endpoint.schema == "binary" {
			if op.Responses["200"].Content["application/octet-stream"].Schema.Type != "string" {
				problems = append(problems, fmt.Errorf("OpenAPI: %s no longer returns media bytes", endpoint.path))
			}
		} else if response.Ref != "#/components/schemas/"+want {
			problems = append(problems, fmt.Errorf("OpenAPI: %s response schema changed", endpoint.path))
		}
		for _, p := range op.Parameters {
			if p.Required && p.Name != "id" {
				problems = append(problems, fmt.Errorf("OpenAPI: %s requires new parameter %s", endpoint.path, p.Name))
			}
		}
	}
	// Keep this list aligned with fields decoded by pkg/immich, not every field
	// upstream happens to expose. New optional fields are allowed.
	requirements := map[string]map[string]string{
		"ServerVersionResponseDto": {"major": "integer", "minor": "integer", "patch": "integer", "prerelease": "integer"},
		"AlbumResponseDto":         {"id": "string", "albumName": "string", "description": "string", "albumThumbnailAssetId": "string", "assetCount": "integer", "updatedAt": "string"},
		"AssetResponseDto":         {"id": "string", "checksum": "string", "originalFileName": "string", "type": "string", "visibility": "string", "localDateTime": "string", "fileCreatedAt": "string", "updatedAt": "string", "isOffline": "boolean", "isTrashed": "boolean", "width": "integer", "height": "integer", "duration": "integer", "thumbhash": "string", "livePhotoVideoId": "string", "exifInfo": "object", "stack": "object"},
		"AssetStackResponseDto":    {"id": "string", "primaryAssetId": "string", "assetCount": "integer"},
		"MetadataSearchDto":        {"albumIds": "array", "page": "integer", "size": "integer", "withStacked": "boolean", "order": "string"},
		"SearchResponseDto":        {"assets": "object"},
		"SearchAssetResponseDto":   {"items": "array", "count": "integer", "nextPage": "string"},
		"AssetFaceResponseDto":     {"id": "string", "imageWidth": "integer", "imageHeight": "integer", "boundingBoxX1": "integer", "boundingBoxX2": "integer", "boundingBoxY1": "integer", "boundingBoxY2": "integer", "person": "object", "sourceType": "string"},
		"PersonResponseDto":        {"id": "string", "name": "string", "birthDate": "string", "thumbnailPath": "string", "isHidden": "boolean"},
	}
	var wireType func(apiSchema, int) string
	wireType = func(s apiSchema, depth int) string {
		if depth > 12 {
			return "unresolved"
		}
		if s.Type != "" {
			return s.Type
		}
		if name, ok := strings.CutPrefix(s.Ref, "#/components/schemas/"); ok {
			return wireType(doc.Components.Schemas[name], depth+1)
		}
		if len(s.AllOf) == 1 {
			return wireType(s.AllOf[0], depth+1)
		}
		return "missing"
	}
	for name, fields := range requirements {
		for field, want := range fields {
			got := wireType(doc.Components.Schemas[name].Properties[field], 0)
			if got != want {
				problems = append(problems, fmt.Errorf("OpenAPI: %s.%s requires %s, got %s", name, field, want, got))
			}
		}
	}
	for _, field := range doc.Components.Schemas["MetadataSearchDto"].Required {
		if !slices.Contains([]string{"albumIds", "page", "size", "withStacked", "order"}, field) {
			problems = append(problems, fmt.Errorf("OpenAPI: metadata search requires new input %s", field))
		}
	}
	slices.SortFunc(problems, func(a, b error) int { return strings.Compare(a.Error(), b.Error()) })
	return errors.Join(problems...)
}
