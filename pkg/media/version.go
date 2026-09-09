package media

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/robinjoseph08/memento/pkg/immich"
)

// ContentVersion identifies the generated thumbnail using imported source facts.
// UpdatedAt and Thumbhash also change for edits that retain the original checksum.
func ContentVersion(asset immich.Asset) string {
	hash := ""
	if asset.Thumbhash != nil {
		hash = *asset.Thumbhash
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("thumbnail-v1\x00%s\x00%s\x00%s\x00%s", asset.ID, asset.Checksum, asset.UpdatedAt, hash)))
	return hex.EncodeToString(digest[:])
}
