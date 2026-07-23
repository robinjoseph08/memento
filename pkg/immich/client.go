// Package immich implements the foundational Immich v3 version readiness check.
package immich

import (
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
		return nil, errors.New("parse Immich URL")
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
		return errors.New("create Immich version request")
	}
	req.Header.Set("x-api-key", c.apiKey)
	response, err := c.httpClient.Do(req)
	if err != nil {
		return errors.New("Immich is unreachable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxVersionResponse))
		return errors.New("Immich version check failed")
	}
	var version versionResponse
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxVersionResponse))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&version); err != nil {
		return errors.New("Immich returned an invalid version")
	}
	actual := fmt.Sprintf("%d.%d.%d", version.Major, version.Minor, version.Patch)
	if actual != supportedVersion {
		return errors.New("Immich version is unsupported")
	}
	return nil
}
