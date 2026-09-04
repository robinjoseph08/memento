# Test Immich as a versioned black box

Memento will test its adapter with fast local HTTP fakes and a separate compatibility suite against exact official Immich release images. The live suite will create fixtures only through supported Immich APIs, exercise Memento with a least-privilege read key, and verify observable albums, people, thumbnails, ranges, playback, and chapter extraction without reading Immich tables or queue internals. CI will test the latest and oldest supported Immich minors, detect new stable releases, and keep machine-learning recognition as a slower nightly smoke test.
