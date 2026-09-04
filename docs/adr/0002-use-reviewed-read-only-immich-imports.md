# Use reviewed, read-only Immich imports

Memento will treat Immich as the source of media files and initial album organization without writing changes back to it. Each Memento Album imports one Immich album, stores its own reviewed membership and presentation, and changes only through a Curator-initiated synchronization. This matches the expected workflow of finishing an album in Immich before publishing it through Memento, prevents later Immich changes from granting access automatically, and avoids scheduled synchronization or a second media-management interface.
