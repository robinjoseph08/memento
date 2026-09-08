# People and identity QA

## Start a disposable installation

```sh
mise start:qa
```

Open the URL printed by the command. Keep the command running. It uses fake
sign-in, a controlled Immich server, and a temporary PostgreSQL schema. Stop it
with Ctrl-C and run it again to reset. It does not erase your development data.
The fixture starts with Immich offline so sign-in can be checked independently.

Use two separate browser profiles or two different browsers, not two ordinary
tabs. Ordinary tabs share the same session cookie. Use a third profile when
checking a second identity without replacing the first identity's session.

## Claim and manage People

- In browser A, claim the installation as `curator@example.test`, display name
  `Curator`. Confirm you reach the Curator page even while Immich is offline.
- Open People. Create `Alex` with only a display name. Confirm there is no
  required email or avatar field.
- Search for `alex` with Enter and with the Search button. The input must keep
  focus after both submissions. Open the Person and rename them. Clear the
  search and confirm the change remains after refreshing.
- Try saving a blank name. Confirm the input gets an actionable error and keeps
  the entered value. Try navigating away with an unsaved name and cancel.
- Add `alex@example.test` as a preauthorization. Confirm it is unused and has no
  expiry. Add the same address again, then try adding it to a different Person.
  Both attempts must fail without replacing the original approval.
- Add a second unused address, revoke it, then try signing in with that address
  in browser B. Access must stay denied.

## Sign in and link identities

- In browser B, sign in as `unknown@example.test` using `Alex` as the display
  name. Access must stay denied. Matching a name grants nothing.
- Before consuming Alex's approval, try `Alex@example.test` with a capital A.
  Access must stay denied. Then use the exact `alex@example.test` address.
- Confirm the existing Person's saved name is used, not the sign-in form's
  display name. Confirm the landing page is available and does not pretend
  onboarding has completed or send you to a missing onboarding route.
- As Curator, inspect Alex. The approval should now be consumed and the identity
  linked. Consumed and revoked approvals move out of the active table and into
  the collapsed Previous emails section. Linked accounts appear above approvals.
- Add `alex.second@example.test` to Alex. Sign in with that address in browser C.
  Confirm it is the same Person and both identities appear under Profile.
- Reload and restart the API with the fixture's printed Restart command. Both
  sessions should survive. Changing the fake sign-in display name must not
  rename the Person.

## Profile and sessions

- Open Profile as Alex. Change the display name and save. Refresh and confirm the
  name appears in the account menu and the Curator's People list.
- On first sign-in, confirm the first linked email is already selected for
  updates and email updates are enabled. Select the second linked email and save.
  Refresh and confirm both settings persist. Disable email updates and confirm
  that preference persists too. This ticket stores preferences; it sends no mail.
- Confirm the destination control offers only linked verified email addresses,
  not an arbitrary address field. Confirm there is no Google avatar or avatar
  picker, only initials.
- Inspect sessions. Browsers B and C should have separate table rows, device
  labels, email addresses, dates, and exactly one current-session label in each
  browser. Members should not see expiration dates. Curators should see them.
- As Curator, inspect Alex's notification address, subscription state, and
  sessions. These details are read-only. Other members cannot inspect them.
- With only one linked account, Alex's Unlink action must be disabled. A Curator
  can still remove all of another Person's accounts.
- From browser A, unlink Alex's second identity. Browser C must lose access on
  its next request or refresh. Browser B must remain signed in. The selected
  update address must clear and email updates must turn off.
- Signing in again with the unlinked identity must fail. Add a fresh approval
  for the same Person, sign in again, and confirm it relinks without creating a
  second Person.
- Sign out the current session in browser C. Browser B must remain signed in.
  Sign in again in C, then choose Sign out everywhere in B. Both browsers must
  lose access, while Curator browser A stays signed in.

## Roles and deactivation

- Inspect your own Person as Curator. Curator and Deactivate controls must be
  disabled with an explanation, even when another Curator exists. Your last
  linked account cannot be removed either. Direct API requests must enforce the
  same restrictions.
- Promote Alex, sign in as Alex, and confirm both Curators can manage People.
  Have Alex remove the original Curator's role and restore it. A Curator can
  change another Curator, but cannot demote or deactivate themselves. There is no
  privileged owner role after the installation is claimed.
- With both promoted, open each Person in separate browser profiles and attempt
  to demote or deactivate each other near-simultaneously. At least one active Curator
  must remain. The automated race tests below check this deterministically.
- Ensure browser A is an active Curator. Sign Alex into B and C, then deactivate
  Alex from A. Existing sessions and fresh sign-in must fail. Alex's record,
  role, approvals, and linked identities must remain visible to the Curator.
- Reactivate Alex. Old sessions must remain invalid; a fresh sign-in should
  work. Role and deactivation are independent controls.

## Private browser state and responsive behavior

- In one browser, visit a Curator's People pages and Profile, sign out, and sign
  in as a different non-Curator. Navigate back, forward, and to `/profile` and
  `/curator/people`. No previous Person's private data should appear.
- Repeat with two tabs sharing a browser session. Sign out or switch identities
  in one tab, then focus the other. It must refresh its identity and clear the
  old private view. A previously started private request must not restore it.
- Check a narrow mobile viewport and dark mode. People navigation, the signed-in
  name and role, Profile, theme controls, and Sign out must remain reachable.
  Mobile navigation must use a single-row header and a side drawer. Check
  Escape, outside-click dismissal, focus restoration, and navigation. Confirm
  table actions stay visible on narrow screens and button text looks centered.
- Use Tab, Enter, and Escape for forms, menus, and unlink confirmations. Closing
  a dialog should restore focus. Failed saves should preserve edits.

## Claims, races, and real Google

Fake browser sign-in intentionally cannot submit a subject override or an
unverified provider claim. Unexpected fields are rejected. Use the module and
local-provider tests for those hostile claims and session expiry rather than
changing database rows or the system clock:

```sh
mise exec -- go test -race ./pkg/identity -count=1
```

Follow [Google sign-in](google-sign-in.md) for real Google credentials, the
exact localhost callback, an empty-install claim, and a preauthorized account.
Check canceling Google consent, an unknown Google account, and a normal return
sign-in. A provider failure must leave `/health` healthy while PostgreSQL is
available. Google protocol tests exercise invalid state, nonce, and ID tokens
against a local provider substitute; they do not use your Google credentials.
