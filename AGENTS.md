# Memento

Memento is a simple self-hosted portal that fronts an Immich library. The
primary goal of Memento is to provide a straightforward way for a photographer
in the family (i.e. someone that brings their camera around and shoots photo
and video) to add assets to their own Immich library, and then share out to
friends and family the specific photos that they care about in an organization
that makes sense for them.

## What makes Memento special?

As we build this product, it's important to keep in mind some of the things
that makes this project unique so these values aren't compromised.

### 1. Simple and easy to use

From the end-user perspective, this needs to be as easy and as streamlined as
possible. The main users will be non-tech-savvy friends and famliy, so any
complexity needs to be throughly considered before being added, and if it's
deemed necessary, find a way to hide it from them. When in doubt, try to mimic
the experience and UI of Google Photos or Immich since that's what they'll be
most familiar with.

### 2. Granular permissions

The main limitation with existing solutions is the lack of granular-enough
permissions. Everything is shared on an album level in alternative products,
but for Memento, it should be granular enough to hide access of a specific
asset within an album from a specific subset of users. But since managing this
on an individual asset basis would be difficult and cumbersome, there should be
constructs to be able to make sweeping access changes as well (e.g. instead of
assigning access to each photo, assign access to the album, and then add
overrides to individual items).

### 3. Self-hosted

Memento is a single Docker image that can be deployed easily into an existing
self-hosted stack that is already running Immich. Decisions sometimes differ
depending on if the app will be deployed in the cloud vs on someone's machine,
so it's important to keep this in mind when developing all features.

### 4. Curator's final call

Very few things that change the access of assets should be done automatically.
Instead, it should be recommended to the Curator, and they can make the final
change. In the same vein, everything should be editable by the Curator.

## A note from Robin

I like ambitious ideas, simple systems, and software that feels obvious. Do not
preserve complexity just because it already exists. Do not introduce machinery
because it looks architecturally impressive. Understand the real constraint,
then fight for the smallest model that makes the correct behavior unsurprising.

Channel both "measure twice, cut once" and "yagni". Fight scope creep. Try to
honor the dev's intent in both a minimal and realistic fashion.

The rest of this document is meant to help you navigate the codebase and make
changes effectively. Think of these instructions less as "hard rules", more as
"good defaults". The developer's preferences should be able to override
anything here.

## Plans and work artifacts

- Do not commit implementation plans, research notes, or agent scratch files.
  Keep temporary working material in `./tmp` since that's gitignored.
- A merged PR is the implementation record. Close or update its tracking item
  when the work lands; do not preserve a second checklist in the repository.

## Taste

- Complexity belongs at the adapter boundary. Orchestration stays pure, UI
  stays dumb.
- Inferred types over annotations. `any` is the enemy.
- Comments describe how a thing is used, and move when the code moves. To be
  used mostly to describe functions, not to annotate every line of behavior.
- Our users are not tech-savvy, so they'll be confused by a blank section, a
  lying spinner, and a stale label. No continuously repainting animations; they
  peg the GPU on high-refresh displays.
- If a rule here fights the task in front of you, say so loudly and get a human
  sign-off before breaking it.

## Additional tips

- Security is important, but should not be over-indexed on, especially for dev
  mode/maintainer-only features.
