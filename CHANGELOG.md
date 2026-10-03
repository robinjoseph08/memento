# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

## [0.4.0] - 2026-10-03

- [Deps] Bump the javascript-dependencies group across 1 directory with 16 updates (#96)
- [Backend] BREAKING: Require SMTP and remove auth_mode (#99)
- [Deps] Bump the go-dependencies group across 1 directory with 4 updates (#98)
- [Deps] Bump alpine from 3.24.1 to 3.24.2 (#83)
- [Feature] Sign in with an emailed Sign-in Code (#97)
- [Deps] Bump actions/github-script from 8 to 9 in the github-actions group across 1 directory (#85)
- [Backend] Identify People by verified email (#94)
- [Fix] Store dev PostgreSQL data in a Docker volume (#93)

## [0.3.0] - 2026-09-26

- [Mobile] Sign in from the Mobile App and see Albums (#88)
- [CI] Make the Mobile and Official Immich checks safe to require (#82)
- [Mobile] Run the Mobile App against a local Installation (#80)
- [Test] Match smoke downloads to authorized entries (#81)
- [Fix] Stop exposing photo filenames (#79)
- [Feature] Cast videos over AirPlay and photos and videos over Google Cast with signed media URLs (#78)
- [Docs] Add Mobile App, Push, and Installation to the glossary and record the viewer API contract (#77)

## [0.2.0] - 2026-09-16

- [Frontend] Ignore Immich albums on the import page and restore them from an ignored list (#65)
- [Feature] Show email and video chapter checks on Settings (#64)
- [Docs] Drop the 0.1.0 port upgrade note from the README (#63)
- [Feature] Document deployment in one place, listen on 3579, and drop files_path (#62)

## [0.1.0] - 2026-09-16

- [Fix] Record a chapter attempt's outcome even when its deadline hits early (#60)
- [Fix] Keep the search field on the URL when a clear is undone by Back (#59)
- [Frontend] Give Memento character with icons, prints, avatars, and a steadier timeline (#58)
- [Fix] Resolve final MVP verification findings (#57)
- [Test] Certify Immich releases and browser compatibility (#56)
- [Deps] Bump the javascript-dependencies group across 1 directory with 14 updates (#55)
- [Test] Update Vitest to v5 (#54)
- [Feature] Browse shared photos and videos in a viewer Library (#53)
- [Feature] Add photo zoom to the lightbox
- [Deps] Bump the go-dependencies group with 5 updates (#48)
- [Feature] Dismiss selected updates without notifying viewers (#51)
- [Backend] Compress API and frontend responses (#50)
- [Feature] Order Album covers and preview Viewing Groups (#46)
- [Test] Bound Go test parallelism to keep PostgreSQL responsive (#45)
- [Feature] Scrub the viewer album by month from a timeline (#43)
- [Feature] Synchronize an Album from Immich (#42)
- [Feature] Deliver update email and surface Curator work (#41)
- [Feature] Create and read Update Notifications (#40)
- [Feature] Invite People and resolve Access Requests (#39)
- [Feature] Play titled videos with chapters (#38)
- [Feature] Browse and download published photos (#37)
- [Feature] Grant access, preview, and publish an Album (#36)
- [Frontend] Match the Curator Album editor to the approved Outline prototype (#35)
- [Frontend] Replace native confirm prompts with in-app dialogs (#34)
- [Frontend] Add favicon and touch icon from the brand mark (#33)
- [Feature] Arrange Moments and review face recommendations (#31)
- [Feature] Import Immich albums with durable progress and Curator editing (#30)
- [Frontend] Add page titles and public link previews
- [Fix] Refine configuration and development isolation (#28)
- [Frontend] Use Montserrat for body text (#27)
- [Deps] Bump alpine from 3.22.2 to 3.24.1 (#1)
- [Feature] Manage People and Google identities (#23)
- [Deps] Bump actions/upload-artifact from 6 to 7 in the github-actions group (#25)
- [Deps] bump the go-dependencies group across 1 directory with 4 updates (#20)
- [Backend] Add creation-site error stacks
- [CI] Clarify jobs and strengthen Go validation (#21)
- [Feature] Make new installations claimable with fake sign-in (#19)
- [Docs] Record approved MVP lifecycle decisions (#18)
- [Docs] Record accessible Moment cover decision (#5)
- [Docs] Establish foundational context for the project
- [Init] Initialize project
- Initial commit

### Features

- [Feature] Deliver update email and surface Curator work
