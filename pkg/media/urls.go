package media

import "net/url"

// FaceThumbnailURL is the private browser URL for an Immich person thumbnail,
// versioned by the person's last change so a new featured photo gets a new URL.
func FaceThumbnailURL(sourceID, version string) string {
	return "/api/media/faces/" + url.PathEscape(sourceID) + "/thumbnail?v=" + url.QueryEscape(version)
}

// AvatarURL is the private browser URL for a Person's avatar. The version
// carries both the chosen face and that face's Immich version.
func AvatarURL(personID, faceID, version string) string {
	return "/api/media/people/" + url.PathEscape(personID) + "/avatar?v=" + url.QueryEscape(avatarVersion(faceID, version))
}

func avatarVersion(faceID, version string) string {
	return faceID + ":" + version
}

// PlaybackRevision ends every playback URL. Playback was once cached for a
// year under the content version alone, and Immich can transcode a video again
// without that version changing, so a new revision keeps browsers from
// replaying a stream they cached before. Nothing reads it on the server.
const PlaybackRevision = "&revision=2"
