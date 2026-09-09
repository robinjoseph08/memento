// Package fixture creates disposable Immich libraries through supported public APIs.
// Compatibility tests can reuse this package without granting Memento write access.
package fixture

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/robinjoseph08/memento/pkg/immich"
)

const Release = "v3.1.0"

var readPermissions = []string{"album.read", "asset.read", "asset.view", "face.read", "person.read"}

type Album struct {
	ID           string
	Name         string
	Description  string
	AssetIDs     []string
	CoverAssetID string
}

type Asset struct {
	ID string
	Photo
}

type Person struct {
	ID, Name, BirthDate, AssetID string
	ImageWidth, ImageHeight      int
	X, Y, Width, Height          int
}

// Library retains the read secret privately; bootstrap sessions never reach Memento.
type Library struct {
	Albums  []Album
	Assets  []Asset
	Person  Person
	baseURL string
	secret  string
}

func (f *Library) Source() *immich.Client { return immich.New(f.baseURL, f.secret) }

type api struct {
	baseURL string
	token   string
	http    *http.Client
}

func (a *api) request(ctx context.Context, method, path, contentType string, body io.Reader, target any) error {
	req, err := http.NewRequestWithContext(ctx, method, a.baseURL+"/api"+path, body)
	if err != nil {
		return fmt.Errorf("build fixture request")
	}
	req.Header.Set("Content-Type", contentType)
	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}
	response, err := a.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("fixture %s %s: transport failure", method, path)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
		// Never print upstream bodies: authentication responses may contain credentials.
		return fmt.Errorf("fixture %s %s: HTTP %d", method, path, response.StatusCode)
	}
	if target == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(target); err != nil {
		return fmt.Errorf("fixture %s %s: invalid JSON response", method, path)
	}
	return nil
}

func (a *api) json(ctx context.Context, method, path string, body, target any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode fixture request")
		}
		reader = bytes.NewReader(data)
	}
	return a.request(ctx, method, path, "application/json", reader, target)
}

func (a *api) login(ctx context.Context, email, password string) error {
	var response struct {
		AccessToken string `json:"accessToken"`
	}
	if err := a.json(ctx, "POST", "/auth/login", map[string]string{"email": email, "password": password}, &response); err != nil {
		return err
	}
	if response.AccessToken == "" {
		return fmt.Errorf("fixture login returned no session")
	}
	a.token = response.AccessToken
	return nil
}

// Setup requires an empty loopback instance. Admin signup fails on an existing installation.
// The expected release is explicit so future compatibility cases can reuse the fixtures.
func Setup(ctx context.Context, baseURL, expectedRelease string) (*Library, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme != "http" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("fixture requires a disposable loopback HTTP origin")
	}
	a := &api{baseURL: baseURL, http: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}
	var version struct {
		Major      int  `json:"major"`
		Minor      int  `json:"minor"`
		Patch      int  `json:"patch"`
		Prerelease *int `json:"prerelease"`
	}
	if err := a.json(ctx, "GET", "/server/version", nil, &version); err != nil {
		return nil, err
	}
	actual := fmt.Sprintf("v%d.%d.%d", version.Major, version.Minor, version.Patch)
	if actual != expectedRelease || version.Prerelease != nil {
		return nil, fmt.Errorf("fixture expected stable %s, got %s", expectedRelease, actual)
	}
	// An exact version match must not bypass the production import gate.
	if err := immich.New(baseURL, "").CheckImport(ctx); err != nil {
		return nil, err
	}
	adminPassword, sourcePassword := rand.Text()+"aA1!", rand.Text()+"aA1!"
	if err := a.json(ctx, "POST", "/auth/admin-sign-up", map[string]string{"email": "admin@memento.invalid", "name": "Smoke admin", "password": adminPassword}, nil); err != nil {
		return nil, err
	}
	if err := a.login(ctx, "admin@memento.invalid", adminPassword); err != nil {
		return nil, err
	}
	var owner struct {
		IsAdmin bool `json:"isAdmin"`
	}
	if err := a.json(ctx, "POST", "/admin/users", map[string]any{"email": "source@memento.invalid", "name": "Smoke source", "password": sourcePassword, "isAdmin": false, "shouldChangePassword": false, "notify": false}, &owner); err != nil {
		return nil, err
	}
	if owner.IsAdmin {
		return nil, fmt.Errorf("fixture source owner unexpectedly has admin access")
	}
	if err := a.login(ctx, "source@memento.invalid", sourcePassword); err != nil {
		return nil, err
	}
	f := &Library{baseURL: baseURL}
	for i, photo := range Photos() {
		id, err := a.upload(ctx, photo, i)
		if err != nil {
			return nil, err
		}
		f.Assets = append(f.Assets, Asset{ID: id, Photo: photo})
	}
	person := Person{Name: "Smoke person", BirthDate: "1990-01-02", AssetID: f.Assets[0].ID, ImageWidth: 64, ImageHeight: 48, X: 8, Y: 6, Width: 20, Height: 24}
	var createdPerson struct {
		ID string `json:"id"`
	}
	if err := a.json(ctx, http.MethodPost, "/people", map[string]any{"name": person.Name, "birthDate": person.BirthDate, "isHidden": false}, &createdPerson); err != nil {
		return nil, err
	}
	if createdPerson.ID == "" {
		return nil, fmt.Errorf("fixture person has no ID")
	}
	person.ID = createdPerson.ID
	if err := a.json(ctx, http.MethodPost, "/faces", map[string]any{
		"assetId": person.AssetID, "personId": person.ID, "imageWidth": person.ImageWidth, "imageHeight": person.ImageHeight,
		"x": person.X, "y": person.Y, "width": person.Width, "height": person.Height,
	}, nil); err != nil {
		return nil, err
	}
	f.Person = person
	// The equal-time pair shares a Moment. Select its later source ID so neither
	// album can pass the cover check by keeping that Moment's earliest entry.
	coverAssetID := max(f.Assets[1].ID, f.Assets[2].ID)
	for i, indices := range [][]int{{0, 1, 2, 3}, {1, 2}} {
		album := Album{Name: fmt.Sprintf("Smoke album %d", i+1), Description: fmt.Sprintf("Unchanged source description %d", i+1), CoverAssetID: coverAssetID}
		for _, index := range indices {
			album.AssetIDs = append(album.AssetIDs, f.Assets[index].ID)
		}
		var response struct {
			ID string `json:"id"`
		}
		if err := a.json(ctx, "POST", "/albums", map[string]any{"albumName": album.Name, "description": album.Description, "assetIds": album.AssetIDs}, &response); err != nil {
			return nil, err
		}
		if response.ID == "" {
			return nil, fmt.Errorf("fixture album has no ID")
		}
		album.ID = response.ID
		if err := a.json(ctx, "PATCH", "/albums/"+album.ID, map[string]string{"albumThumbnailAssetId": album.CoverAssetID}, nil); err != nil {
			return nil, err
		}
		f.Albums = append(f.Albums, album)
	}
	var key struct {
		Secret string `json:"secret"`
		APIKey struct {
			Permissions []string `json:"permissions"`
		} `json:"apiKey"`
	}
	if err := a.json(ctx, "POST", "/api-keys", map[string]any{"name": "Memento smoke read", "permissions": readPermissions}, &key); err != nil {
		return nil, err
	}
	slices.Sort(key.APIKey.Permissions)
	if key.Secret == "" || !slices.Equal(key.APIKey.Permissions, readPermissions) {
		return nil, fmt.Errorf("fixture key must grant exactly album.read, asset.read, asset.view, face.read, person.read")
	}
	f.secret = key.Secret
	return f, nil
}

func (a *api) upload(ctx context.Context, photo Photo, index int) (string, error) {
	data, err := JPEG(photo, index)
	if err != nil {
		return "", err
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	file, err := writer.CreateFormFile("assetData", photo.Filename)
	if err != nil {
		return "", err
	}
	if _, err := file.Write(data); err != nil {
		return "", err
	}
	// Deliberately different from EXIF. Waiting must observe extracted metadata, not upload defaults.
	for _, field := range []string{"fileCreatedAt", "fileModifiedAt"} {
		if err := writer.WriteField(field, "2020-01-01T00:00:00Z"); err != nil {
			return "", err
		}
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	var response struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := a.request(ctx, "POST", "/assets", writer.FormDataContentType(), &body, &response); err != nil {
		return "", err
	}
	if response.ID == "" || strings.ToLower(response.Status) != "created" {
		return "", fmt.Errorf("fixture upload did not create a distinct asset")
	}
	return response.ID, nil
}
