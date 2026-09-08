package immich

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/errorstack"
)

var _ Library = (*Client)(nil)

// ListAlbums returns accessible albums. Immich does not paginate this endpoint.
func (c *Client) ListAlbums(ctx context.Context) ([]Album, error) {
	var documents []json.RawMessage
	if err := c.json(ctx, http.MethodGet, "/api/albums", nil, &documents, "album.read"); err != nil {
		return nil, err
	}
	if documents == nil {
		return nil, unreadable("album list")
	}
	albums := make([]Album, 0, len(documents))
	for _, document := range documents {
		album, err := decodeAlbum(document)
		if err != nil {
			return nil, err
		}
		albums = append(albums, album)
	}
	return albums, nil
}

func (c *Client) GetAlbum(ctx context.Context, id string) (Album, error) {
	if id == "" {
		return Album{}, errcodes.ValidationError("An Immich album ID is required.")
	}
	var document json.RawMessage
	if err := c.json(ctx, http.MethodGet, "/api/albums/"+escapeID(id), nil, &document, "album.read"); err != nil {
		return Album{}, err
	}
	album, err := decodeAlbum(document)
	if err != nil {
		return Album{}, err
	}
	if album.ID != id {
		return Album{}, unreadable("album identity")
	}
	return album, nil
}

func (c *Client) GetAsset(ctx context.Context, id string) (Asset, error) {
	if id == "" {
		return Asset{}, errcodes.ValidationError("An Immich asset ID is required.")
	}
	var document json.RawMessage
	if err := c.json(ctx, http.MethodGet, "/api/assets/"+escapeID(id), nil, &document, "asset.read"); err != nil {
		return Asset{}, err
	}
	asset, err := decodeAsset(document)
	if err != nil {
		return Asset{}, err
	}
	if asset.ID != id {
		return Asset{}, unreadable("asset identity")
	}
	return asset, nil
}

// ListMembers reads one source membership page, retaining non-trashed timeline
// and archive assets, including stacked members, to match Immich's album count.
// A filtered page may be empty with a nonzero next; zero means the end.
// EXIF is omitted because Immich's EXIF join excludes assets without a record.
func (c *Client) ListMembers(ctx context.Context, id string, page int) ([]Asset, int, error) {
	if id == "" || page < 1 || int64(page) > 9007199254740991 {
		return nil, 0, errcodes.ValidationError("An Immich album ID and a positive page number are required.")
	}
	body, err := json.Marshal(struct {
		AlbumIDs    []string `json:"albumIds"`
		Page        int      `json:"page"`
		Size        int      `json:"size"`
		WithStacked bool     `json:"withStacked"`
		Order       string   `json:"order"`
	}{[]string{id}, page, 250, true, "asc"})
	if err != nil {
		return nil, 0, errorstack.Capture(err)
	}
	var response struct {
		Assets json.RawMessage `json:"assets"`
	}
	if err := c.json(ctx, http.MethodPost, "/api/search/metadata", bytes.NewReader(body), &response, "asset.read and album.read"); err != nil {
		return nil, 0, err
	}
	var members struct {
		Items    []json.RawMessage `json:"items"`
		Count    int               `json:"count"`
		NextPage *string           `json:"nextPage"`
	}
	fields, ok := objectFields(response.Assets, &members, "items", "count")
	if !ok || fields["nextPage"] == nil || members.Items == nil || members.Count != len(members.Items) || len(members.Items) > 250 {
		return nil, 0, unreadable("membership page")
	}
	assets := make([]Asset, 0, len(members.Items))
	seen := make(map[string]bool, len(members.Items))
	for _, document := range members.Items {
		asset, err := decodeAsset(document)
		if err != nil {
			return nil, 0, err
		}
		if seen[asset.ID] {
			return nil, 0, unreadable("duplicate membership")
		}
		seen[asset.ID] = true
		if !asset.Trashed && (asset.Visibility == "archive" || asset.Visibility == "timeline") {
			assets = append(assets, asset)
		}
	}
	next := 0
	if members.NextPage != nil {
		var err error
		next, err = strconv.Atoi(*members.NextPage)
		if err != nil || next <= page || int64(next) > 9007199254740991 || strconv.Itoa(next) != *members.NextPage || len(members.Items) == 0 {
			return nil, 0, unreadable("membership page")
		}
	}
	return assets, next, nil
}

func decodeAsset(data json.RawMessage) (Asset, error) {
	var asset Asset
	fields, ok := objectFields(data, &asset, "id", "checksum", "originalFileName", "type", "visibility", "localDateTime", "fileCreatedAt", "updatedAt", "isOffline", "isTrashed")
	if !ok || asset.ID == "" || asset.Filename == "" || !timestamp(asset.LocalDateTime) || !timestamp(asset.FileCreatedAt) || !timestamp(asset.UpdatedAt) {
		return Asset{}, unreadable("asset metadata")
	}
	for _, field := range []string{"width", "height", "duration", "thumbhash"} {
		if fields[field] == nil {
			return Asset{}, unreadable("asset metadata")
		}
	}
	checksum, err := base64.StdEncoding.DecodeString(asset.Checksum)
	if err != nil || len(checksum) != 20 {
		return Asset{}, unreadable("asset checksum")
	}
	switch asset.Visibility {
	case "archive", "timeline", "hidden", "locked":
	default:
		return Asset{}, unreadable("asset visibility")
	}
	switch asset.Kind {
	case "IMAGE", "VIDEO", "AUDIO", "OTHER":
	default:
		return Asset{}, unreadable("asset type")
	}
	if (asset.Width != nil && *asset.Width < 0) || (asset.Height != nil && *asset.Height < 0) || (asset.Duration != nil && (*asset.Duration < 0 || *asset.Duration > 2147483647)) || (asset.LivePhotoVideoID != nil && *asset.LivePhotoVideoID == "") {
		return Asset{}, unreadable("asset metadata")
	}
	if asset.Stack != nil {
		_, ok := objectFields(fields["stack"], asset.Stack, "id", "primaryAssetId", "assetCount")
		if !ok || asset.Stack.ID == "" || asset.Stack.PrimaryAssetID == "" || asset.Stack.AssetCount < 0 {
			return Asset{}, unreadable("stack metadata")
		}
	}
	return asset, nil
}

func decodeAlbum(data json.RawMessage) (Album, error) {
	var album Album
	fields, ok := objectFields(data, &album, "id", "albumName", "description", "assetCount", "updatedAt")
	if !ok || fields["albumThumbnailAssetId"] == nil || album.ID == "" || album.Count < 0 || !timestamp(album.UpdatedAt) || (album.StartDate != "" && !timestamp(album.StartDate)) || (album.EndDate != "" && !timestamp(album.EndDate)) || (album.ThumbnailID != nil && *album.ThumbnailID == "") {
		return Album{}, unreadable("album metadata")
	}
	return album, nil
}

// objectFields validates fields used by Memento without rejecting future upstream fields.
func objectFields(data json.RawMessage, target any, required ...string) (map[string]json.RawMessage, bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil || json.Unmarshal(data, target) != nil {
		return nil, false
	}
	for _, key := range required {
		value := fields[key]
		if len(value) == 0 || string(value) == "null" {
			return nil, false
		}
	}
	return fields, true
}

func escapeID(id string) string {
	if id == "." || id == ".." {
		return strings.ReplaceAll(id, ".", "%2E")
	}
	return url.PathEscape(id)
}

func timestamp(value string) bool {
	_, err := time.Parse(time.RFC3339Nano, value)
	return err == nil
}
