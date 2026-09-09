package immich

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/errorstack"
)

const maxJSONBytes = 8 << 20

// Client reads the configured Immich API. It never follows redirects with its key.
type Client struct {
	baseURL, apiKey string
	http            *http.Client
}

func New(baseURL, apiKey string) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, http: &http.Client{
		Timeout:       5 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

type serverVersion struct {
	Major      *int            `json:"major"`
	Minor      *int            `json:"minor"`
	Patch      *int            `json:"patch"`
	Prerelease json.RawMessage `json:"prerelease"`
}

func (v serverVersion) supported() bool {
	return *v.Major == 3 && *v.Minor <= 1 && string(v.Prerelease) == "null"
}

func (c *Client) version(ctx context.Context) (serverVersion, error) {
	var v serverVersion
	if err := c.json(ctx, http.MethodGet, "/api/server/version", nil, &v, ""); err != nil {
		return v, err
	}
	if v.Major == nil || v.Minor == nil || v.Patch == nil || *v.Major < 0 || *v.Minor < 0 || *v.Patch < 0 {
		return v, unreadable("version")
	}
	if len(v.Prerelease) != 0 && string(v.Prerelease) != "null" {
		var number int
		if json.Unmarshal(v.Prerelease, &number) != nil || number < 0 {
			return v, unreadable("version")
		}
	}
	return v, nil
}

// CheckImport gates imports on the supported stable Immich minors. Call before import reads.
func (c *Client) CheckImport(ctx context.Context) error {
	version, err := c.version(ctx)
	if err != nil {
		return err
	}
	if !version.supported() {
		return unsupportedVersion()
	}
	return nil
}

func unsupportedVersion() error {
	return &errcodes.Error{HTTPCode: http.StatusConflict, Code: "immich_unsupported_version", Message: "Import requires stable Immich 3.0.x and 3.1.x. Update Immich or Memento before importing."}
}

// Check authenticates through album.read, including on versions too old to import.
// Only safe status messages are returned; no upstream response or URL is exposed.
func (c *Client) Check(ctx context.Context) Connection {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	version, err := c.version(ctx)
	if err != nil {
		return Connection{Message: err.Error()}
	}
	supported := version.supported()
	result := Connection{Version: fmt.Sprintf("%d.%d.%d", *version.Major, *version.Minor, *version.Patch), ImportSupported: &supported}
	var albums []json.RawMessage
	if err := c.json(ctx, http.MethodGet, "/api/albums", nil, &albums, "album.read"); err != nil {
		result.Message = err.Error()
		return result
	}
	if albums == nil {
		result.Message = unreadable("album list").Error()
		return result
	}
	result.Usable = true
	result.Message = "Immich is connected. The API key can read albums."
	if !supported {
		result.Message = "Immich is connected. " + unsupportedVersion().Error()
	}
	return result
}

func unreadable(part string) error {
	return errorstack.Capture(&errcodes.Error{HTTPCode: http.StatusBadGateway, Code: "immich_invalid_response", Message: "Immich returned an unreadable " + part + ". Check its address and server version."})
}

// transportError discards upstream errors, which can contain URLs, headers, or bodies.
func transportError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errorstack.Capture(fmt.Errorf("immich request timed out: %w", context.DeadlineExceeded))
	}
	return errorstack.Capture(&errcodes.Error{HTTPCode: http.StatusBadGateway, Code: "immich_unavailable", Message: "Immich is unavailable. Check its address and network connection, then retry."})
}

func (c *Client) request(ctx context.Context, method, path string, body io.Reader, permission string) (*http.Response, error) {
	base, err := url.Parse(c.baseURL)
	if err != nil || base == nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.User != nil || base.RawQuery != "" || base.ForceQuery || strings.Contains(c.baseURL, "#") || base.Opaque != "" {
		return nil, errcodes.ValidationError("Check immich_url points to the Immich server, without credentials, a query, or a fragment.")
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, transportError(ctx, err)
	}
	req.Header.Set("X-Api-Key", c.apiKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(req)
	if err != nil {
		return nil, transportError(ctx, err)
	}
	if response.StatusCode == http.StatusOK {
		return response, nil
	}
	_ = response.Body.Close()
	switch response.StatusCode {
	case http.StatusUnauthorized:
		return nil, &errcodes.Error{HTTPCode: http.StatusBadGateway, Code: "immich_unauthorized", Message: "Immich rejected the API key. Check immich_api_key, then retry."}
	case http.StatusForbidden:
		if permission == "" {
			return nil, &errcodes.Error{HTTPCode: http.StatusForbidden, Code: "immich_permission_denied", Message: "Immich denied access to its public version endpoint. Check the server and reverse proxy access rules."}
		}
		return nil, &errcodes.Error{HTTPCode: http.StatusForbidden, Code: "immich_permission_denied", Message: "The Immich API key is missing permission. Enable " + permission + ", then retry."}
	case http.StatusNotFound:
		return nil, &errcodes.Error{HTTPCode: http.StatusNotFound, Code: "not_found", Message: "Immich resource or endpoint not found. Check access and immich_url points to the server without /api."}
	default:
		return nil, errorstack.Capture(&errcodes.Error{HTTPCode: http.StatusBadGateway, Code: "immich_unavailable", Message: fmt.Sprintf("Immich is unavailable (HTTP %d). Check the server, then retry.", response.StatusCode)})
	}
}

func (c *Client) json(ctx context.Context, method, path string, body io.Reader, target any, permission string) error {
	response, err := c.request(ctx, method, path, body, permission)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxJSONBytes+1))
	if err != nil {
		return transportError(ctx, err)
	}
	if len(data) > maxJSONBytes {
		return unreadable("response: size limit exceeded")
	}
	// Unmarshal rejects malformed input and trailing JSON while tolerating unknown fields.
	if json.Unmarshal(data, target) != nil {
		return unreadable("response")
	}
	return nil
}
