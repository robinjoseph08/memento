// Package immich implements the foundational Immich v3 version readiness check.
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
	"time"

	"github.com/robinjoseph08/memento/pkg/config"
)

const (
	maxVersionResponse = 32 << 10
	supportedVersion   = "3.0.3"
)

type safeError string

func (err safeError) Error() string { return string(err) }

var (
	errParseURL             = errors.New("parse Immich URL")
	errCreateVersionRequest = errors.New("create Immich version request")
	errUnreachable          = safeError("Immich is unreachable")
	errVersionCheckFailed   = safeError("Immich version check failed")
	errInvalidVersion       = safeError("Immich returned an invalid version")
	errUnsupportedVersion   = safeError("Immich version is unsupported")
)

// Client checks the configured private Immich API without exposing its URL or key.
type Client struct {
	baseURL       *url.URL
	apiKey        string
	healthTimeout time.Duration
	httpClient    *http.Client
}

type versionResponse struct {
	Major int `json:"major"`
	Minor int `json:"minor"`
	Patch int `json:"patch"`
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

// Check verifies basic reachability and the exact supported server version.
func (c *Client) Check(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, c.healthTimeout)
	defer cancel()

	endpoint := c.baseURL.JoinPath("api", "server", "version")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return errCreateVersionRequest
	}
	req.Header.Set("x-api-key", c.apiKey)
	response, err := c.httpClient.Do(req)
	if err != nil {
		return errUnreachable
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxVersionResponse))
		return errVersionCheckFailed
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxVersionResponse+1))
	if err != nil || len(body) > maxVersionResponse {
		return errInvalidVersion
	}
	var version versionResponse
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&version); err != nil {
		return errInvalidVersion
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errInvalidVersion
	}
	actual := fmt.Sprintf("%d.%d.%d", version.Major, version.Minor, version.Patch)
	if actual != supportedVersion {
		return errUnsupportedVersion
	}
	return nil
}
