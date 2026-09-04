# Probe video chapters over HTTP ranges

Memento will run bundled ffprobe against Immich's authenticated original-file endpoint and require byte-range seeking instead of downloading large camera originals to temporary storage. Chapter extraction is optional and may fail without blocking playback or publication. Compatibility tests will verify range behavior for every supported Immich release because original-file ranges work in current Immich implementations but are not part of its documented API contract.
