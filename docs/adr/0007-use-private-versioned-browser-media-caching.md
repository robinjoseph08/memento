# Use private, versioned browser media caching

Memento will give authorized thumbnails and previews long-lived private browser caching through URLs that include a media-content version. This avoids repeatedly proxying unchanged bytes from Immich while preventing shared proxies from caching private media. Revoking access cannot erase bytes already retained by a viewer's browser, and the product explicitly accepts that limitation rather than weakening normal browser caching for all viewers.

Video playback is the exception and is never stored. It streams Immich's playback copy, which Immich can transcode again, for example at a higher resolution, while the original and so the content version stay the same. A cached stream would keep serving the old copy. Browsers also keep only the start of a long video, so caching playback saved little.
