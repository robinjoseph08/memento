// Package immich implements the pinned, server-side Immich v3.0.3 contract.
package immich

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/config"
)

const (
	maxJSONResponse  = 10 << 20
	supportedVersion = "3.0.3"
	assetPageSize    = 1000
)

var requiredPermissions = []string{
	"album.read",
	"asset.download",
	"asset.read",
	"asset.view",
	"face.read",
	"person.read",
}

type safeError string

func (err safeError) Error() string { return string(err) }

var (
	errParseURL           = errors.New("parse Immich URL")
	errCreateRequest      = errors.New("create Immich request")
	errUnreachable        = safeError("Immich is unreachable")
	errRequestFailed      = safeError("Immich validation failed")
	errInvalidResponse    = safeError("Immich returned an invalid response")
	errUnsupportedVersion = safeError("Immich version is unsupported")
	errInvalidPermissions = safeError("Immich API key permissions are invalid")
	errInvalidCredentials = safeError("Immich API key is invalid")
	errOwnedAlbumsFailed  = safeError("Immich album discovery failed")
	errAlbumAssetsFailed  = safeError("Immich album membership lookup failed")
)

// IsConfigurationError reports whether validation failed because the operator
// must change the Immich version or API key permissions before retrying.
func IsConfigurationError(err error) bool {
	return errors.Is(err, errUnsupportedVersion) || errors.Is(err, errInvalidPermissions) || errors.Is(err, errInvalidCredentials)
}

// Client accesses only allowlisted Immich v3.0.3 read operations. It never
// returns raw DTOs, source URLs, paths, owner IDs, library IDs, or face data.
type Client struct {
	baseURL       *url.URL
	apiKey        string
	healthTimeout time.Duration
	httpClient    *http.Client
}

type versionResponse struct {
	Major      *int            `json:"major"`
	Minor      *int            `json:"minor"`
	Patch      *int            `json:"patch"`
	Prerelease json.RawMessage `json:"prerelease"`
}

type apiKeyResponse struct {
	Permissions []string `json:"permissions"`
}

type albumResponse struct {
	ID                         *string `json:"id"`
	AlbumName                  *string `json:"albumName"`
	Description                *string `json:"description"`
	AssetCount                 *int    `json:"assetCount"`
	CreatedAt                  *string `json:"createdAt"`
	UpdatedAt                  *string `json:"updatedAt"`
	StartDate                  *string `json:"startDate"`
	EndDate                    *string `json:"endDate"`
	LastModifiedAssetTimestamp *string `json:"lastModifiedAssetTimestamp"`
}

type searchResponse struct {
	Assets *searchAssetsResponse `json:"assets"`
}

type searchAssetsResponse struct {
	Count    *int             `json:"count"`
	Items    *[]assetResponse `json:"items"`
	NextPage json.RawMessage  `json:"nextPage"`
	Total    *int             `json:"total"`
}

type assetResponse struct {
	ID            *string         `json:"id"`
	Type          *string         `json:"type"`
	Width         json.RawMessage `json:"width"`
	Height        json.RawMessage `json:"height"`
	LocalDateTime *string         `json:"localDateTime"`
}

func (response *versionResponse) UnmarshalJSON(contents []byte) error {
	type exactVersionResponse versionResponse
	if err := rejectCaseVariantFields(contents, "major", "minor", "patch", "prerelease"); err != nil {
		return err
	}
	return json.Unmarshal(contents, (*exactVersionResponse)(response))
}

func (response *apiKeyResponse) UnmarshalJSON(contents []byte) error {
	type exactAPIKeyResponse apiKeyResponse
	if err := rejectCaseVariantFields(contents, "permissions"); err != nil {
		return err
	}
	return json.Unmarshal(contents, (*exactAPIKeyResponse)(response))
}

func (response *albumResponse) UnmarshalJSON(contents []byte) error {
	type exactAlbumResponse albumResponse
	if err := rejectCaseVariantFields(contents,
		"id", "albumName", "description", "assetCount", "createdAt", "updatedAt",
		"startDate", "endDate", "lastModifiedAssetTimestamp"); err != nil {
		return err
	}
	return json.Unmarshal(contents, (*exactAlbumResponse)(response))
}

func (response *searchResponse) UnmarshalJSON(contents []byte) error {
	type exactSearchResponse searchResponse
	if err := rejectCaseVariantFields(contents, "assets"); err != nil {
		return err
	}
	return json.Unmarshal(contents, (*exactSearchResponse)(response))
}

func (response *searchAssetsResponse) UnmarshalJSON(contents []byte) error {
	type exactSearchAssetsResponse searchAssetsResponse
	if err := rejectCaseVariantFields(contents, "count", "items", "nextPage", "total"); err != nil {
		return err
	}
	return json.Unmarshal(contents, (*exactSearchAssetsResponse)(response))
}

func (response *assetResponse) UnmarshalJSON(contents []byte) error {
	type exactAssetResponse assetResponse
	if err := rejectCaseVariantFields(contents, "id", "type", "width", "height", "localDateTime"); err != nil {
		return err
	}
	return json.Unmarshal(contents, (*exactAssetResponse)(response))
}

// AlbumSummary is the normalized subset Memento retains from an owned album.
type AlbumSummary struct {
	SourceID                   uuid.UUID
	Name                       string
	Description                string
	AssetCount                 int
	CreatedAt                  time.Time
	UpdatedAt                  time.Time
	StartDate                  *time.Time
	EndDate                    *time.Time
	LastModifiedAssetTimestamp *time.Time
}

// AssetSummary is the normalized, path-free subset returned by membership pagination.
type AssetSummary struct {
	SourceID      uuid.UUID
	MediaType     string
	Width         *int
	Height        *int
	LocalDateTime string
}

// AssetPage is one explicit entry point into Immich metadata pagination.
type AssetPage struct {
	Items    []AssetSummary
	NextPage *int
}

// New returns a least-privilege server-side client.
func New(cfg config.ImmichConfig, httpClient *http.Client) (*Client, error) {
	baseURL, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, errParseURL
	}
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	safeHTTPClient := *httpClient
	safeHTTPClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &Client{baseURL: baseURL, apiKey: cfg.APIKey, healthTimeout: cfg.HealthTimeout, httpClient: &safeHTTPClient}, nil
}

// Check verifies the exact server version and the exact required read-only API
// key permission set. Extra permissions fail the least-privilege gate.
func (c *Client) Check(ctx context.Context) error {
	var version versionResponse
	if err := c.getJSON(ctx, "server/version", &version, errRequestFailed); err != nil {
		return err
	}
	if version.Major == nil || version.Minor == nil || version.Patch == nil || len(version.Prerelease) == 0 {
		return errInvalidResponse
	}
	actual := fmt.Sprintf("%d.%d.%d", *version.Major, *version.Minor, *version.Patch)
	if actual != supportedVersion || string(version.Prerelease) != "null" {
		return errUnsupportedVersion
	}

	var key apiKeyResponse
	if err := c.getJSON(ctx, "api-keys/me", &key, errRequestFailed); err != nil {
		return err
	}
	if !samePermissions(key.Permissions, requiredPermissions) {
		return errInvalidPermissions
	}
	return nil
}

// OwnedAlbums retrieves and normalizes only albums owned by the API-key owner.
func (c *Client) OwnedAlbums(ctx context.Context) ([]AlbumSummary, error) {
	var response []albumResponse
	if err := c.getJSONQuery(ctx, "albums", url.Values{"isOwned": {"true"}}, &response, errOwnedAlbumsFailed); err != nil {
		return nil, err
	}
	if response == nil {
		return nil, errInvalidResponse
	}
	albums := make([]AlbumSummary, 0, len(response))
	seen := make(map[uuid.UUID]struct{}, len(response))
	for _, raw := range response {
		album, err := normalizeAlbum(raw)
		if err != nil {
			return nil, errInvalidResponse
		}
		if _, duplicate := seen[album.SourceID]; duplicate {
			return nil, errInvalidResponse
		}
		seen[album.SourceID] = struct{}{}
		albums = append(albums, album)
	}
	return albums, nil
}

// AlbumAssetsPage reads one bounded page of complete album membership metadata.
// Callers begin at page 1 and follow NextPage until nil.
func (c *Client) AlbumAssetsPage(ctx context.Context, albumID uuid.UUID, page int) (AssetPage, error) {
	if albumID == uuid.Nil || page < 1 || page > int(^uint(0)>>1)/assetPageSize {
		return AssetPage{}, errInvalidResponse
	}
	body, err := json.Marshal(map[string]any{
		"albumIds":   []string{albumID.String()},
		"page":       page,
		"size":       assetPageSize,
		"withExif":   false,
		"withPeople": false,
	})
	if err != nil {
		return AssetPage{}, errInvalidResponse
	}
	var response searchResponse
	if err := c.doJSON(ctx, http.MethodPost, "search/metadata", nil, body, &response, errAlbumAssetsFailed); err != nil {
		return AssetPage{}, err
	}
	if response.Assets == nil || response.Assets.Count == nil || response.Assets.Items == nil || response.Assets.Total == nil ||
		len(response.Assets.NextPage) == 0 || *response.Assets.Count != len(*response.Assets.Items) ||
		*response.Assets.Total < 0 || len(*response.Assets.Items) > assetPageSize {
		return AssetPage{}, errInvalidResponse
	}
	result := AssetPage{Items: make([]AssetSummary, 0, len(*response.Assets.Items))}
	seen := make(map[uuid.UUID]struct{}, len(*response.Assets.Items))
	for _, raw := range *response.Assets.Items {
		if raw.ID == nil || raw.Type == nil || raw.LocalDateTime == nil {
			return AssetPage{}, errInvalidResponse
		}
		assetID, err := uuid.Parse(*raw.ID)
		width, widthErr := requiredNullableDimension(raw.Width)
		height, heightErr := requiredNullableDimension(raw.Height)
		localDateTime := strings.TrimSpace(*raw.LocalDateTime)
		_, timeErr := time.Parse(time.RFC3339Nano, localDateTime)
		if err != nil || assetID == uuid.Nil || !validAssetType(*raw.Type) || widthErr != nil || heightErr != nil || timeErr != nil {
			return AssetPage{}, errInvalidResponse
		}
		if _, duplicate := seen[assetID]; duplicate {
			return AssetPage{}, errInvalidResponse
		}
		seen[assetID] = struct{}{}
		result.Items = append(result.Items, AssetSummary{
			SourceID: assetID, MediaType: strings.ToLower(*raw.Type), Width: width, Height: height,
			LocalDateTime: truncate(localDateTime, 64),
		})
	}
	if string(response.Assets.NextPage) != "null" {
		var nextValue string
		if err := json.Unmarshal(response.Assets.NextPage, &nextValue); err != nil {
			return AssetPage{}, errInvalidResponse
		}
		next, err := strconv.Atoi(nextValue)
		if err != nil || next != page+1 {
			return AssetPage{}, errInvalidResponse
		}
		result.NextPage = &next
	}
	firstIndex := (page - 1) * assetPageSize
	if result.NextPage != nil {
		if len(result.Items) != assetPageSize || *response.Assets.Total <= page*assetPageSize {
			return AssetPage{}, errInvalidResponse
		}
	} else if *response.Assets.Total != firstIndex+len(result.Items) {
		return AssetPage{}, errInvalidResponse
	}
	return result, nil
}

func (c *Client) getJSON(ctx context.Context, path string, target any, statusError error) error {
	return c.getJSONQuery(ctx, path, nil, target, statusError)
}

func (c *Client) getJSONQuery(ctx context.Context, path string, query url.Values, target any, statusError error) error {
	return c.doJSON(ctx, http.MethodGet, path, query, nil, target, statusError)
}

func (c *Client) doJSON(ctx context.Context, method, path string, query url.Values, body []byte, target any, statusError error) error {
	requestCtx, cancel := context.WithTimeout(ctx, c.healthTimeout)
	defer cancel()
	endpoint := c.baseURL.JoinPath("api", path)
	if query != nil {
		endpoint.RawQuery = query.Encode()
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(requestCtx, method, endpoint.String(), reader)
	if err != nil {
		return errCreateRequest
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := c.httpClient.Do(req)
	if err != nil {
		return errUnreachable
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxJSONResponse))
		if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
			return errInvalidCredentials
		}
		return statusError
	}
	contents, err := io.ReadAll(io.LimitReader(response.Body, maxJSONResponse+1))
	if err != nil || len(contents) > maxJSONResponse {
		return errInvalidResponse
	}
	if err := validateUniqueJSON(contents); err != nil {
		return errInvalidResponse
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	if err := decoder.Decode(target); err != nil {
		return errInvalidResponse
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errInvalidResponse
	}
	return nil
}

func rejectCaseVariantFields(contents []byte, exactFields ...string) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(contents, &object); err != nil {
		return err
	}
	for key := range object {
		for _, exact := range exactFields {
			if key != exact && strings.EqualFold(key, exact) {
				return errInvalidResponse
			}
		}
	}
	return nil
}

func validateUniqueJSON(contents []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(contents))
	if err := consumeUniqueJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err != nil {
			return err
		}
		return errInvalidResponse
	}
	return nil
}

func consumeUniqueJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errInvalidResponse
			}
			foldedKey := strings.ToLower(key)
			if _, duplicate := seen[foldedKey]; duplicate {
				return errInvalidResponse
			}
			seen[foldedKey] = struct{}{}
			if err := consumeUniqueJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errInvalidResponse
		}
	case '[':
		for decoder.More() {
			if err := consumeUniqueJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errInvalidResponse
		}
	default:
		return errInvalidResponse
	}
	return nil
}

func samePermissions(actual, required []string) bool {
	if len(actual) != len(required) {
		return false
	}
	seen := make(map[string]struct{}, len(actual))
	for _, permission := range actual {
		seen[permission] = struct{}{}
	}
	if len(seen) != len(required) {
		return false
	}
	for _, permission := range required {
		if _, ok := seen[permission]; !ok {
			return false
		}
	}
	return true
}

func normalizeAlbum(raw albumResponse) (AlbumSummary, error) {
	if raw.ID == nil || raw.AlbumName == nil || raw.Description == nil || raw.AssetCount == nil || raw.CreatedAt == nil || raw.UpdatedAt == nil {
		return AlbumSummary{}, errInvalidResponse
	}
	id, err := uuid.Parse(*raw.ID)
	if err != nil || id == uuid.Nil || *raw.AssetCount < 0 {
		return AlbumSummary{}, errInvalidResponse
	}
	createdAt, err := time.Parse(time.RFC3339, *raw.CreatedAt)
	if err != nil {
		return AlbumSummary{}, errInvalidResponse
	}
	updatedAt, err := time.Parse(time.RFC3339, *raw.UpdatedAt)
	if err != nil {
		return AlbumSummary{}, errInvalidResponse
	}
	startDate, err := optionalTime(raw.StartDate)
	if err != nil {
		return AlbumSummary{}, errInvalidResponse
	}
	endDate, err := optionalTime(raw.EndDate)
	if err != nil {
		return AlbumSummary{}, errInvalidResponse
	}
	lastModified, err := optionalTime(raw.LastModifiedAssetTimestamp)
	if err != nil {
		return AlbumSummary{}, errInvalidResponse
	}
	name := truncate(strings.TrimSpace(*raw.AlbumName), 240)
	if name == "" {
		name = "Untitled Source album"
	}
	return AlbumSummary{
		SourceID: id, Name: name, Description: truncate(strings.TrimSpace(*raw.Description), 2000),
		AssetCount: *raw.AssetCount, CreatedAt: createdAt.UTC(), UpdatedAt: updatedAt.UTC(),
		StartDate: startDate, EndDate: endDate, LastModifiedAssetTimestamp: lastModified,
	}, nil
}

func optionalTime(value *string) (*time.Time, error) {
	if value == nil || *value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, *value)
	if err != nil {
		return nil, err
	}
	parsed = parsed.UTC()
	return &parsed, nil
}

func truncate(value string, maximum int) string {
	if utf8.RuneCountInString(value) <= maximum {
		return value
	}
	return string([]rune(value)[:maximum])
}

func validAssetType(value string) bool {
	return value == "IMAGE" || value == "VIDEO" || value == "AUDIO" || value == "OTHER"
}

func requiredNullableDimension(raw json.RawMessage) (*int, error) {
	if len(raw) == 0 {
		return nil, errInvalidResponse
	}
	if string(raw) == "null" {
		return nil, nil
	}
	var value int
	if err := json.Unmarshal(raw, &value); err != nil || value < 0 {
		return nil, errInvalidResponse
	}
	return &value, nil
}
