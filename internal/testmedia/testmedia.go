// Package testmedia embeds two tiny playable WebM videos for fixtures and
// adapter tests. Both are six seconds of a flat color at 160x90, encoded with
// VP8 so Chromium, Firefox, and WebKit can all play them. They were produced
// with ffmpeg from a color source and an FFMETADATA chapter list, and hold no
// downloaded media.
package testmedia

import _ "embed"

// Chaptered carries three embedded chapters: Arrival at 0s, Cake at 2s, and
// Goodbyes at 4s, each two seconds long.
//
//go:embed chapters.webm
var Chaptered []byte

// Plain has the same shape with no chapter metadata.
//
//go:embed plain.webm
var Plain []byte

// Duration is the length of both videos in whole seconds.
const Duration = 6

// ContentType is the media type Immich reports for both files.
const ContentType = "video/webm"
