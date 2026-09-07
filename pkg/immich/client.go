package immich

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

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

// Check verifies the version and authenticates the key against the read-only account endpoint.
// Upstream response bodies and network errors can contain credentials, so neither is returned.
func (c *Client) Check(ctx context.Context) Connection {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var version struct {
		Major *int `json:"major"`
		Minor *int `json:"minor"`
		Patch *int `json:"patch"`
	}
	if message := c.get(ctx, "/api/server/version", &version); message != "" {
		return Connection{Message: message}
	}
	if version.Major == nil || version.Minor == nil || version.Patch == nil || *version.Major < 0 || *version.Minor < 0 || *version.Patch < 0 {
		return Connection{Message: "Immich returned an unreadable version. Check immich_url points to the Immich server."}
	}
	reported := fmt.Sprintf("%d.%d.%d", *version.Major, *version.Minor, *version.Patch)
	var account struct {
		ID string `json:"id"`
	}
	if message := c.get(ctx, "/api/users/me", &account); message != "" {
		return Connection{Version: reported, Message: message}
	}
	if account.ID == "" {
		return Connection{Version: reported, Message: "Immich returned an unreadable account response. Check immich_url points to the Immich server."}
	}
	return Connection{Usable: true, Version: reported, Message: "Immich is connected. The API key can read your account."}
}

func (c *Client) get(ctx context.Context, path string, target any) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return "Check immich_url points to the Immich server."
	}
	req.Header.Set("X-Api-Key", c.apiKey)
	req.Header.Set("Accept", "application/json")
	response, err := c.http.Do(req)
	if err != nil {
		return "Immich is unavailable. Check its address and network connection, then retry."
	}
	defer func() { _ = response.Body.Close() }()
	switch response.StatusCode {
	case http.StatusOK:
		decoder := json.NewDecoder(io.LimitReader(response.Body, 64*1024))
		if err := decoder.Decode(target); err != nil {
			return "Immich returned an unreadable response. Check its address and server version."
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return "Immich returned an unreadable response. Check its address and server version."
		}
		return ""
	case http.StatusUnauthorized:
		return "Immich rejected the API key. Check immich_api_key, then retry."
	case http.StatusForbidden:
		return "The Immich API key is missing permission to read your account. Enable user.read, then retry."
	case http.StatusNotFound:
		return "Immich endpoint not found. Check immich_url points to the server, without /api."
	default:
		return fmt.Sprintf("Immich is unavailable (HTTP %d). Check the server, then retry.", response.StatusCode)
	}
}
