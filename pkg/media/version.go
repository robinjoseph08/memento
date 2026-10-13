package media

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/robinjoseph08/memento/pkg/immich"
)

// ContentVersion names what a photo or video shows, so its browser URL and
// cache entry change only when that does. Immich touches updatedAt and
// re-encodes the thumbhash whenever it rebuilds thumbnails, so neither is
// part of it.
func ContentVersion(asset immich.Asset) string {
	digest := sha256.Sum256(fmt.Appendf(nil, "thumbnail-v2\x00%s\x00%s\x00%t\x00%s\x00%s", asset.ID, asset.Checksum, asset.Edited, dimension(asset.Width), dimension(asset.Height)))
	return hex.EncodeToString(digest[:])
}

// sameContent reports whether the live asset still shows what was imported.
// Replacing the file changes the checksum, and a non-destructive edit in
// Immich sets the edited flag or, for a crop or quarter turn, changes the
// dimensions. An edit that keeps both, such as mirroring an already edited
// photo, goes unnoticed. A nil edited flag predates Memento reading it and
// matches either state, so a photo imported already edited keeps serving;
// synchronization reviews the edit instead.
func sameContent(item EntryMedia, asset immich.Asset) bool {
	return item.Checksum == asset.Checksum && (item.Edited == nil || *item.Edited == asset.Edited) &&
		sameDimension(item.Width, asset.Width) && sameDimension(item.Height, asset.Height)
}

func sameDimension(stored, live *int) bool {
	return stored == nil && live == nil || stored != nil && live != nil && *stored == *live
}

func dimension(value *int) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(*value)
}
