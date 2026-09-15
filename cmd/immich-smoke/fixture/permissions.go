package fixture

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/immich"
)

// WithoutPermission issues a disposable owner key with exactly one read grant removed.
// The secret stays inside the production adapter and never appears in diagnostics.
func (f *Library) WithoutPermission(ctx context.Context, name string) (*immich.Client, error) {
	if !slices.Contains(readPermissions, name) {
		return nil, fmt.Errorf("unknown fixture read permission")
	}
	permissions := slices.DeleteFunc(slices.Clone(readPermissions), func(p string) bool { return p == name })
	var key struct {
		Secret string `json:"secret"`
		APIKey struct {
			Permissions []string `json:"permissions"`
		} `json:"apiKey"`
	}
	if err := f.owner.json(ctx, http.MethodPost, "/api-keys", map[string]any{"name": "Memento smoke without " + name, "permissions": permissions}, &key); err != nil {
		return nil, err
	}
	slices.Sort(key.APIKey.Permissions)
	if key.Secret == "" || !slices.Equal(permissions, key.APIKey.Permissions) {
		return nil, fmt.Errorf("fixture restricted key grants differ")
	}
	return immich.New(f.baseURL, key.Secret), nil
}

// PermissionFailure performs the read requiring name and returns its adapter error.
// A CLI negative mode can return this directly to prove a nonzero, redacted failure.
func (f *Library) PermissionFailure(ctx context.Context, name string) error {
	source, err := f.WithoutPermission(ctx, name)
	if err != nil {
		return err
	}
	switch name {
	case "album.read":
		_, err = source.ListAlbums(ctx)
	case "asset.read":
		_, err = source.GetAsset(ctx, f.Assets[0].ID)
	case "face.read":
		_, err = source.ListFaces(ctx, f.Assets[0].ID)
	case "asset.download":
		result, openErr := source.Original(ctx, f.Assets[0].ID)
		err = openErr
		if result.Body != nil {
			_ = result.Body.Close()
		}
	case "asset.view":
		result, openErr := source.Thumbnail(ctx, f.Assets[0].ID)
		err = openErr
		if result.Body != nil {
			_ = result.Body.Close()
		}
	case "person.read":
		result, openErr := source.PersonThumbnail(ctx, f.Person.ID)
		err = openErr
		if result.Body != nil {
			_ = result.Body.Close()
		}
	}
	return err
}

// VerifyPermissions proves that each omitted grant fails at its production read boundary.
func (f *Library) VerifyPermissions(ctx context.Context) error {
	for _, permission := range readPermissions {
		err := f.PermissionFailure(ctx, permission)
		if err == nil {
			return fmt.Errorf("omitting %s unexpectedly allowed its read", permission)
		}
		var code *errcodes.Error
		if !errors.As(err, &code) || code.Code != "immich_permission_denied" || !strings.Contains(code.Message, "Enable "+permission+",") {
			return fmt.Errorf("omitting %s did not produce its actionable permission error: %w", permission, err)
		}
	}
	return nil
}
