# Accessibility verification

Memento targets WCAG 2.2 AA for its responsive Curator and Recipient workflows.

## Automated browser checks

`tests/browser/accessibility.spec.ts` runs in every project configured by `playwright.config.ts`:

- Chromium desktop
- Firefox desktop
- Chromium mobile
- Firefox mobile

The suite scans Onboarding, Recipient navigation, account settings, archives, the Media viewer, Publication review, and Curator mobile drill-down with axe-core. It also checks focus movement and restoration, responsive overflow, light and dark contrast, route state, and reduced-motion date navigation.

Run the matrix with:

```sh
mise run test:browser
```

Automated checks supplement rather than replace assistive-technology testing.

## Assistive-technology release checks

Complete these checks before a release that changes the relevant workflow. Test at 200% browser zoom and at one supported mobile width as well as the normal desktop layout. Record the browser, assistive technology, operating-system version, result, and issue link for any failure in the release notes.

Recommended combinations:

- VoiceOver with current Safari on macOS and iOS
- NVDA with current Firefox on Windows
- TalkBack with current Chrome on Android

### Onboarding

1. Start from an accepted Invitation with Onboarding incomplete.
2. Navigate from the page heading through every acknowledgment, email preference, Session choice, Interest-list control, save action, and completion action using only the keyboard or screen-reader gestures.
3. Confirm every checkbox and radio control announces its label, state, required status, and disabled status when busy.
4. Save without completing and confirm the success message is announced without moving focus.
5. Trigger a validation or server error and confirm it is announced once with enough context to recover.
6. Resume Onboarding and confirm the saved choices are understandable without relying on color, position, or prior memory.

### Publication

1. Open a ready Event in the Curator workspace.
2. On desktop, confirm the Work, Event, and Inspect regions have understandable names and a logical reading order. On mobile, move through the three drill-down controls and confirm focus enters the selected region.
3. Review readiness, Audience summaries, Curator-only state, notification choice, and Preview as Recipient using only the keyboard or screen-reader gestures.
4. Confirm the Recipient preview is announced as read-only and that Comment, Favorite, Settings, and Download controls are disabled.
5. Trigger a validation error and an autosave error. Confirm both are announced, focus remains usable, and retry controls have unambiguous names.
6. Open the Publication confirmation and cancel it. Confirm focus returns to the control that opened it.

### Archives

1. Prepare an Event archive and a selected-Media archive.
2. Confirm the authorized item count, expiry, filenames, part sizes, and download actions are announced in a sensible order.
3. In a Public-computer Session, confirm the persistent-file warning is announced before each original, Event archive, and subset archive action.
4. Download one part and confirm its control changes to a disabled downloaded state that is announced.
5. Test an expired plan and a failed part. Confirm the status or error is announced without exposing a private token or source path.

### Media viewer

1. Open a photo and a video from Photos, an Event, Favorites, and Search.
2. Confirm focus starts on Close viewer, remains inside the modal while it is open, and returns to the exact gallery control after close or Escape.
3. Confirm the Media type, date, dimensions, original download, Favorite state, Comment controls, and unavailable state have useful names and reading order.
4. On mobile, confirm Media comes before the stacked interaction panel and all controls remain reachable without horizontal scrolling.
5. Add, edit, and delete a Comment and toggle Favorite. Confirm changed content and state are announced without duplicate notifications.
6. Remove access while the viewer is open. Confirm the unavailable announcement does not reveal a hidden count, Moment, Event placement, or inaccessible title.

## Visual and motion checks

- Verify focus remains visible in dark and light themes, including navigation, gallery controls, dialogs, date controls, and account settings.
- Verify text and non-text contrast at default, hover, focus, selected, disabled, warning, and error states.
- With reduced motion enabled at the operating-system level, verify date jumps and all state changes occur without smooth scrolling or decorative animation.
- With a coarse pointer or touch device, verify controls have at least a 44 by 44 CSS pixel target where layout permits and never fall below the WCAG 2.2 minimum target size and spacing.
- At 320 CSS pixels wide and at 400% zoom, verify no workflow requires two-dimensional scrolling except intentionally scrollable data tables.

## Privacy checks

For a Recipient with partial Event access, inspect the accessibility tree, keyboard order, and browser network log. Inaccessible Media must produce no placeholder, hidden count, misleading name, focus stop, prefetch, thumbnail request, or other browser request. Browser requests must remain same-origin Memento URLs and must never contact Immich or a font CDN directly.
