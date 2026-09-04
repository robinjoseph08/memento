# Use private, versioned browser media caching

Memento will give authorized thumbnails, previews, and playback responses long-lived private browser caching through URLs that include a media-content version. This avoids repeatedly proxying unchanged bytes from Immich while preventing shared proxies from caching private media. Revoking access cannot erase bytes already retained by a viewer's browser, and the product explicitly accepts that limitation rather than weakening normal browser caching for all viewers.
