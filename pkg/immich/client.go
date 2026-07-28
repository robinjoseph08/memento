// Package immich implements the pinned, server-side Immich v3.0.3 contract.
package immich

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/config"
)

const (
	maxJSONResponse      = 10 << 20
	maxThumbnailResponse = 20 << 20
	maxMediaRedirects    = 5
	supportedVersion     = "3.0.3"
	assetPageSize        = 1000
	maxDatabaseInteger   = 1<<31 - 1
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
	errPeopleFailed       = safeError("Immich people lookup failed")
	errFacesFailed        = safeError("Immich face lookup failed")
	// ErrNotFound identifies an Immich resource that disappeared between evidence reads.
	ErrNotFound = safeError("Immich resource not found")
)

// IsConfigurationError reports whether validation failed because the operator
// must change the Immich version or API key permissions before retrying.
func IsConfigurationError(err error) bool {
	return errors.Is(err, errUnsupportedVersion) || errors.Is(err, errInvalidPermissions) || errors.Is(err, errInvalidCredentials)
}

// Client accesses only allowlisted Immich v3.0.3 read operations. It never
// returns raw DTOs, source URLs, owner IDs, or library IDs. Paths and face
// anchors appear only in normalized server-side repair evidence.
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
	Permissions *[]string `json:"permissions"`
}

type albumResponse struct {
	ID                         *string         `json:"id"`
	AlbumName                  *string         `json:"albumName"`
	Description                *string         `json:"description"`
	AssetCount                 *int            `json:"assetCount"`
	CreatedAt                  *string         `json:"createdAt"`
	UpdatedAt                  *string         `json:"updatedAt"`
	StartDate                  json.RawMessage `json:"startDate"`
	EndDate                    json.RawMessage `json:"endDate"`
	LastModifiedAssetTimestamp json.RawMessage `json:"lastModifiedAssetTimestamp"`
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
	ID               *string         `json:"id"`
	Type             *string         `json:"type"`
	Width            json.RawMessage `json:"width"`
	Height           json.RawMessage `json:"height"`
	LocalDateTime    json.RawMessage `json:"localDateTime"`
	FileCreatedAt    *string         `json:"fileCreatedAt"`
	Checksum         *string         `json:"checksum"`
	OriginalFileName *string         `json:"originalFileName"`
	OriginalPath     *string         `json:"originalPath"`
}

type peopleResponse struct {
	People      *[]personResponse `json:"people"`
	Total       *int              `json:"total"`
	Hidden      *int              `json:"hidden"`
	HasNextPage *bool             `json:"hasNextPage"`
}

type personResponse struct {
	ID       *string `json:"id"`
	Name     *string `json:"name"`
	IsHidden *bool   `json:"isHidden"`
}

type faceResponse struct {
	ID          *string         `json:"id"`
	ImageWidth  *int            `json:"imageWidth"`
	ImageHeight *int            `json:"imageHeight"`
	X1          *int            `json:"boundingBoxX1"`
	Y1          *int            `json:"boundingBoxY1"`
	X2          *int            `json:"boundingBoxX2"`
	Y2          *int            `json:"boundingBoxY2"`
	Person      json.RawMessage `json:"person"`
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
	if err := rejectCaseVariantFields(contents, "id", "type", "width", "height", "localDateTime", "fileCreatedAt", "checksum", "originalFileName", "originalPath"); err != nil {
		return err
	}
	return json.Unmarshal(contents, (*exactAssetResponse)(response))
}

func (response *peopleResponse) UnmarshalJSON(contents []byte) error {
	type exactPeopleResponse peopleResponse
	if err := rejectCaseVariantFields(contents, "people", "total", "hidden", "hasNextPage"); err != nil {
		return err
	}
	return json.Unmarshal(contents, (*exactPeopleResponse)(response))
}

func (response *personResponse) UnmarshalJSON(contents []byte) error {
	type exactPersonResponse personResponse
	if err := rejectCaseVariantFields(contents, "id", "name", "isHidden"); err != nil {
		return err
	}
	return json.Unmarshal(contents, (*exactPersonResponse)(response))
}

func (response *faceResponse) UnmarshalJSON(contents []byte) error {
	type exactFaceResponse faceResponse
	if err := rejectCaseVariantFields(contents, "id", "imageWidth", "imageHeight", "boundingBoxX1", "boundingBoxY1", "boundingBoxX2", "boundingBoxY2", "person"); err != nil {
		return err
	}
	return json.Unmarshal(contents, (*exactFaceResponse)(response))
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

// AssetSummary is normalized server-side membership and private repair evidence.
type AssetSummary struct {
	SourceID      uuid.UUID
	MediaType     string
	Width         *int
	Height        *int
	LocalDateTime *string
	CaptureAt     string
	Checksum      string
	Filename      string
	OriginalPath  string
}

// PersonSummary is the private normalized identity evidence from Immich.
type PersonSummary struct {
	SourceID uuid.UUID
	Name     string
	Hidden   bool
}

// FaceSummary is a private repair anchor. It never grants access.
type FaceSummary struct {
	SourceID    uuid.UUID
	PersonID    *uuid.UUID
	ImageWidth  int
	ImageHeight int
	X1          int
	Y1          int
	X2          int
	Y2          int
}

// AssetPage is one explicit entry point into Immich metadata pagination.
type AssetPage struct {
	Items    []AssetSummary
	NextPage *int
}

// MediaRequest contains the safe browser validators Memento may pass to Immich.
type MediaRequest struct {
	Range           string
	IfRange         string
	IfNoneMatch     string
	IfModifiedSince string
}

// MediaResponse is an allowlisted Immich media response. Callers must close
// Body after streaming it to an already-authorized client. Derivatives are
// size-bounded; video and originals remain streaming and use bounded buffers.
type MediaResponse struct {
	Body          io.ReadCloser
	StatusCode    int
	ContentType   string
	ContentLength int64
	ContentRange  string
	AcceptRanges  string
	ETag          string
	LastModified  string
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
	if key.Permissions == nil {
		return errInvalidResponse
	}
	if !samePermissions(*key.Permissions, requiredPermissions) {
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

// Album retrieves one normalized owned-album summary for stability checks.
func (c *Client) Album(ctx context.Context, albumID uuid.UUID) (AlbumSummary, error) {
	if albumID == uuid.Nil {
		return AlbumSummary{}, errInvalidResponse
	}
	var response albumResponse
	if err := c.getJSON(ctx, "albums/"+albumID.String(), &response, errOwnedAlbumsFailed); err != nil {
		return AlbumSummary{}, err
	}
	album, err := normalizeAlbum(response)
	if err != nil || album.SourceID != albumID {
		return AlbumSummary{}, errInvalidResponse
	}
	return album, nil
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
		*response.Assets.Total != len(*response.Assets.Items) || len(*response.Assets.Items) > assetPageSize {
		return AssetPage{}, errInvalidResponse
	}
	result := AssetPage{Items: make([]AssetSummary, 0, len(*response.Assets.Items))}
	for _, raw := range *response.Assets.Items {
		if raw.ID == nil || raw.Type == nil {
			return AssetPage{}, errInvalidResponse
		}
		assetID, err := uuid.Parse(*raw.ID)
		width, widthErr := requiredNullableDimension(raw.Width)
		height, heightErr := requiredNullableDimension(raw.Height)
		localDateTime, localDateTimeErr := requiredNullableLocalDateTime(raw.LocalDateTime)
		if err != nil || assetID == uuid.Nil || !validAssetType(*raw.Type) || widthErr != nil || heightErr != nil || localDateTimeErr != nil {
			return AssetPage{}, errInvalidResponse
		}
		checksum, checksumErr := optionalChecksum(raw.Checksum)
		captureAt := ""
		if localDateTime != nil {
			captureAt = *localDateTime
		}
		if raw.FileCreatedAt != nil && strings.TrimSpace(*raw.FileCreatedAt) != "" {
			if _, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(*raw.FileCreatedAt)); err != nil {
				return AssetPage{}, errInvalidResponse
			}
			captureAt = strings.TrimSpace(*raw.FileCreatedAt)
		}
		if checksumErr != nil {
			return AssetPage{}, errInvalidResponse
		}
		result.Items = append(result.Items, AssetSummary{
			SourceID: assetID, MediaType: strings.ToLower(*raw.Type), Width: width, Height: height,
			LocalDateTime: localDateTime, CaptureAt: truncate(captureAt, 64), Checksum: checksum,
			Filename: truncate(optionalString(raw.OriginalFileName), 1024), OriginalPath: truncate(optionalString(raw.OriginalPath), 4096),
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
	if result.NextPage != nil && len(result.Items) != assetPageSize {
		return AssetPage{}, errInvalidResponse
	}
	return result, nil
}

// Thumbnail opens a bounded thumbnail after the caller resolves authorization.
func (c *Client) Thumbnail(ctx context.Context, assetID uuid.UUID) (MediaResponse, error) {
	return c.derivative(ctx, assetID, "thumbnail", MediaRequest{})
}

// Preview opens a bounded viewer-sized image derivative.
func (c *Client) Preview(ctx context.Context, assetID uuid.UUID, request MediaRequest) (MediaResponse, error) {
	return c.derivative(ctx, assetID, "preview", request)
}

// Video streams Immich's playback representation with browser range validators.
func (c *Client) Video(ctx context.Context, assetID uuid.UUID, request MediaRequest) (MediaResponse, error) {
	return c.media(ctx, assetID, []string{"video", "playback"}, nil, "video/*", request, false, false)
}

// Original streams the exact bytes of the current Immich original.
func (c *Client) Original(ctx context.Context, assetID uuid.UUID, request MediaRequest) (MediaResponse, error) {
	return c.media(ctx, assetID, []string{"original"}, nil, "image/*,video/*,application/octet-stream", request, false, true)
}

func (c *Client) derivative(ctx context.Context, assetID uuid.UUID, size string, request MediaRequest) (MediaResponse, error) {
	return c.media(ctx, assetID, []string{"thumbnail"}, url.Values{"size": {size}}, "image/avif,image/webp,image/*", request, true, false)
}

func (c *Client) media(ctx context.Context, assetID uuid.UUID, path []string, query url.Values, accept string, request MediaRequest, bounded, original bool) (MediaResponse, error) {
	if assetID == uuid.Nil {
		return MediaResponse{}, errInvalidResponse
	}
	requestCtx, cancel := context.WithCancel(ctx)
	headerTimer := time.AfterFunc(c.healthTimeout, cancel)
	endpointParts := append([]string{"api", "assets", assetID.String()}, path...)
	endpoint := c.baseURL.JoinPath(endpointParts...)
	endpoint.RawQuery = query.Encode()
	response, err := c.doMediaRequest(requestCtx, endpoint, accept, request)
	if !headerTimer.Stop() {
		if response != nil {
			_ = response.Body.Close()
		}
		cancel()
		return MediaResponse{}, errUnreachable
	}
	if err != nil {
		cancel()
		return MediaResponse{}, err
	}
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusPartialContent && response.StatusCode != http.StatusNotModified && response.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		defer response.Body.Close()
		defer cancel()
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxJSONResponse))
		switch response.StatusCode {
		case http.StatusNotFound:
			return MediaResponse{}, ErrNotFound
		case http.StatusUnauthorized, http.StatusForbidden:
			return MediaResponse{}, errInvalidCredentials
		default:
			return MediaResponse{}, errRequestFailed
		}
	}

	result, err := normalizeMediaResponse(response, bounded, original)
	if err != nil {
		_ = response.Body.Close()
		cancel()
		return MediaResponse{}, err
	}
	body := &cancelReadCloser{ReadCloser: response.Body, cancel: cancel}
	result.Body = &safeReadCloser{ReadCloser: body}
	if bounded && result.StatusCode != http.StatusNotModified && result.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		result.Body = &boundedReadCloser{ReadCloser: body, remaining: maxThumbnailResponse}
	}
	return result, nil
}

func (c *Client) doMediaRequest(ctx context.Context, endpoint *url.URL, accept string, validators MediaRequest) (*http.Response, error) {
	current := endpoint
	for redirects := 0; ; redirects++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, current.String(), nil)
		if err != nil {
			return nil, errCreateRequest
		}
		req.Header.Set("Accept", accept)
		req.Header.Set("x-api-key", c.apiKey)
		setMediaRequestHeaders(req.Header, validators)
		response, err := c.httpClient.Do(req)
		if err != nil {
			return nil, errUnreachable
		}
		if response.StatusCode < http.StatusMultipleChoices || response.StatusCode > http.StatusPermanentRedirect || response.StatusCode == http.StatusNotModified {
			return response, nil
		}
		location, err := response.Location()
		_ = response.Body.Close()
		if err != nil || redirects >= maxMediaRedirects || !sameOrigin(c.baseURL, location) {
			return nil, errRequestFailed
		}
		current = location
	}
}

func setMediaRequestHeaders(header http.Header, request MediaRequest) {
	for name, value := range map[string]string{
		"Range": request.Range, "If-Range": request.IfRange,
		"If-None-Match": request.IfNoneMatch, "If-Modified-Since": request.IfModifiedSince,
	} {
		if value != "" {
			header.Set(name, value)
		}
	}
}

func sameOrigin(base, target *url.URL) bool {
	return target != nil && target.User == nil && (target.Scheme == "http" || target.Scheme == "https") &&
		strings.EqualFold(base.Scheme, target.Scheme) && strings.EqualFold(base.Hostname(), target.Hostname()) &&
		effectivePort(base) == effectivePort(target)
}

func effectivePort(endpoint *url.URL) string {
	if endpoint.Port() != "" {
		return endpoint.Port()
	}
	if endpoint.Scheme == "https" {
		return "443"
	}
	return "80"
}

var contentRangePattern = regexp.MustCompile(`^bytes (?:[0-9]+-[0-9]+/[0-9]+|\*/[0-9]+)$`)

func parseContentRange(value string) (length int64, unsatisfied, valid bool) {
	if !contentRangePattern.MatchString(value) {
		return 0, false, false
	}
	value = strings.TrimPrefix(value, "bytes ")
	parts := strings.Split(value, "/")
	total, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || total < 0 {
		return 0, false, false
	}
	if parts[0] == "*" {
		return 0, true, true
	}
	bounds := strings.Split(parts[0], "-")
	start, startErr := strconv.ParseInt(bounds[0], 10, 64)
	end, endErr := strconv.ParseInt(bounds[1], 10, 64)
	if startErr != nil || endErr != nil || start < 0 || start > end || end >= total {
		return 0, false, false
	}
	return end - start + 1, false, true
}

func normalizeMediaResponse(response *http.Response, bounded, original bool) (MediaResponse, error) {
	result := MediaResponse{StatusCode: response.StatusCode, ContentLength: response.ContentLength}
	if response.ContentLength < -1 || (bounded && response.ContentLength > maxThumbnailResponse) {
		return MediaResponse{}, errInvalidResponse
	}
	result.ETag = response.Header.Get("ETag")
	if modified := response.Header.Get("Last-Modified"); modified != "" {
		parsed, err := http.ParseTime(modified)
		if err != nil {
			return MediaResponse{}, errInvalidResponse
		}
		result.LastModified = parsed.UTC().Format(http.TimeFormat)
	}
	if ranges := response.Header.Get("Accept-Ranges"); ranges == "bytes" {
		result.AcceptRanges = ranges
	} else if ranges != "" {
		return MediaResponse{}, errInvalidResponse
	}
	rangeLength, rangeUnsatisfied, rangeValid := parseContentRange(response.Header.Get("Content-Range"))
	if response.Header.Get("Content-Range") != "" {
		if !rangeValid || (response.StatusCode != http.StatusPartialContent && response.StatusCode != http.StatusRequestedRangeNotSatisfiable) {
			return MediaResponse{}, errInvalidResponse
		}
		result.ContentRange = response.Header.Get("Content-Range")
	}
	if response.StatusCode == http.StatusPartialContent && (!rangeValid || rangeUnsatisfied || (response.ContentLength >= 0 && response.ContentLength != rangeLength)) {
		return MediaResponse{}, errInvalidResponse
	}
	if response.StatusCode == http.StatusRequestedRangeNotSatisfiable && (!rangeValid || !rangeUnsatisfied) {
		return MediaResponse{}, errInvalidResponse
	}
	if response.StatusCode == http.StatusNotModified || response.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		result.ContentLength = -1
		return result, nil
	}
	contentType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || !allowedMediaType(contentType, bounded, original) {
		return MediaResponse{}, errInvalidResponse
	}
	result.ContentType = contentType
	return result, nil
}

func allowedMediaType(contentType string, derivative, original bool) bool {
	if derivative {
		return allowedThumbnailType(contentType)
	}
	if !original {
		return strings.HasPrefix(contentType, "video/")
	}
	return (strings.HasPrefix(contentType, "image/") && contentType != "image/svg+xml") ||
		strings.HasPrefix(contentType, "video/") || contentType == "application/octet-stream"
}

func allowedThumbnailType(contentType string) bool {
	switch contentType {
	case "image/avif", "image/gif", "image/jpeg", "image/png", "image/webp":
		return true
	default:
		return false
	}
}

type boundedReadCloser struct {
	io.ReadCloser
	remaining int64
}

func (body *boundedReadCloser) Read(contents []byte) (int, error) {
	if len(contents) == 0 {
		return 0, nil
	}
	if body.remaining == 0 {
		var extra [1]byte
		count, err := body.ReadCloser.Read(extra[:])
		if count > 0 || (err != nil && !errors.Is(err, io.EOF)) {
			return 0, errInvalidResponse
		}
		return 0, err
	}
	if int64(len(contents)) > body.remaining {
		contents = contents[:body.remaining]
	}
	count, err := body.ReadCloser.Read(contents)
	body.remaining -= int64(count)
	if err != nil && !errors.Is(err, io.EOF) {
		return count, errInvalidResponse
	}
	return count, err
}

type safeReadCloser struct {
	io.ReadCloser
}

func (body *safeReadCloser) Read(contents []byte) (int, error) {
	count, err := body.ReadCloser.Read(contents)
	if err != nil && !errors.Is(err, io.EOF) {
		return count, errInvalidResponse
	}
	return count, err
}

type cancelReadCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (body *cancelReadCloser) Close() error {
	err := body.ReadCloser.Close()
	body.cancel()
	return err
}

// People returns every current Immich identity, including hidden clusters.
func (c *Client) People(ctx context.Context) ([]PersonSummary, error) {
	result := make([]PersonSummary, 0)
	seen := map[uuid.UUID]struct{}{}
	for page := 1; ; page++ {
		var response peopleResponse
		if err := c.getJSONQuery(ctx, "people", url.Values{
			"withHidden": {"true"}, "page": {strconv.Itoa(page)}, "size": {strconv.Itoa(assetPageSize)},
		}, &response, errPeopleFailed); err != nil {
			return nil, err
		}
		if response.People == nil || response.Total == nil || response.Hidden == nil || response.HasNextPage == nil ||
			*response.Total < 0 || *response.Hidden < 0 || *response.Hidden > *response.Total || len(*response.People) > assetPageSize {
			return nil, errInvalidResponse
		}
		for _, raw := range *response.People {
			if raw.ID == nil || raw.Name == nil || raw.IsHidden == nil {
				return nil, errInvalidResponse
			}
			id, err := uuid.Parse(*raw.ID)
			if err != nil || id == uuid.Nil {
				return nil, errInvalidResponse
			}
			if _, duplicate := seen[id]; duplicate {
				return nil, errInvalidResponse
			}
			seen[id] = struct{}{}
			result = append(result, PersonSummary{SourceID: id, Name: truncate(strings.TrimSpace(*raw.Name), 240), Hidden: *raw.IsHidden})
		}
		if !*response.HasNextPage {
			return result, nil
		}
		if len(*response.People) == 0 || page > max(1, *response.Total) {
			return nil, errInvalidResponse
		}
	}
}

// Faces returns exact private anchors for one asset.
func (c *Client) Faces(ctx context.Context, assetID uuid.UUID) ([]FaceSummary, error) {
	if assetID == uuid.Nil {
		return nil, errInvalidResponse
	}
	var response []faceResponse
	if err := c.getJSONQuery(ctx, "faces", url.Values{"id": {assetID.String()}}, &response, errFacesFailed); err != nil {
		return nil, err
	}
	if response == nil {
		return nil, errInvalidResponse
	}
	result := make([]FaceSummary, 0, len(response))
	seen := map[uuid.UUID]struct{}{}
	for _, raw := range response {
		if raw.ID == nil || raw.ImageWidth == nil || raw.ImageHeight == nil || raw.X1 == nil || raw.Y1 == nil || raw.X2 == nil || raw.Y2 == nil ||
			*raw.ImageWidth < 0 || *raw.ImageHeight < 0 || *raw.X1 < 0 || *raw.Y1 < 0 || *raw.X2 < *raw.X1 || *raw.Y2 < *raw.Y1 {
			return nil, errInvalidResponse
		}
		id, err := uuid.Parse(*raw.ID)
		if err != nil || id == uuid.Nil {
			return nil, errInvalidResponse
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, errInvalidResponse
		}
		seen[id] = struct{}{}
		personID, err := nestedPersonID(raw.Person)
		if err != nil {
			return nil, errInvalidResponse
		}
		result = append(result, FaceSummary{SourceID: id, PersonID: personID, ImageWidth: *raw.ImageWidth, ImageHeight: *raw.ImageHeight, X1: *raw.X1, Y1: *raw.Y1, X2: *raw.X2, Y2: *raw.Y2})
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
		if response.StatusCode == http.StatusNotFound && path == "faces" {
			return ErrNotFound
		}
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
	if err != nil || id == uuid.Nil || *raw.AssetCount < 0 || *raw.AssetCount > maxDatabaseInteger {
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

func optionalTime(raw json.RawMessage) (*time.Time, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || strings.TrimSpace(value) == "" {
		return nil, errInvalidResponse
	}
	parsed, err := time.Parse(time.RFC3339, value)
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
	return value == "IMAGE" || value == "VIDEO"
}

func requiredNullableLocalDateTime(raw json.RawMessage) (*string, error) {
	if len(raw) == 0 {
		return nil, errInvalidResponse
	}
	if string(raw) == "null" {
		return nil, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, errInvalidResponse
	}
	value = strings.TrimSpace(value)
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.Year() <= 0 {
		valid := false
		for _, layout := range []string{
			"2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05",
			"2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05",
		} {
			if candidate, parseErr := time.Parse(layout, value); parseErr == nil && candidate.Year() > 0 {
				valid = true
				break
			}
		}
		if !valid {
			return nil, errInvalidResponse
		}
	}
	value = truncate(value, 64)
	return &value, nil
}

func optionalString(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func optionalChecksum(value *string) (string, error) {
	if value == nil || strings.TrimSpace(*value) == "" {
		return "", nil
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(*value))
	if err != nil || len(decoded) != 20 {
		return "", errInvalidResponse
	}
	return hex.EncodeToString(decoded), nil
}

func nestedPersonID(raw json.RawMessage) (*uuid.UUID, error) {
	if len(raw) == 0 {
		return nil, errInvalidResponse
	}
	if string(raw) == "null" {
		return nil, nil
	}
	if err := rejectCaseVariantFields(raw, "id"); err != nil {
		return nil, errInvalidResponse
	}
	var person struct {
		ID *string `json:"id"`
	}
	if err := json.Unmarshal(raw, &person); err != nil || person.ID == nil {
		return nil, errInvalidResponse
	}
	id, err := uuid.Parse(*person.ID)
	if err != nil || id == uuid.Nil {
		return nil, errInvalidResponse
	}
	return &id, nil
}

func requiredNullableDimension(raw json.RawMessage) (*int, error) {
	if len(raw) == 0 {
		return nil, errInvalidResponse
	}
	if string(raw) == "null" {
		return nil, nil
	}
	var value int
	if err := json.Unmarshal(raw, &value); err != nil || value < 0 || value > maxDatabaseInteger {
		return nil, errInvalidResponse
	}
	return &value, nil
}
