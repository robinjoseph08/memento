# Google sign-in

Memento uses Google only to verify identity. It requests `openid profile email`,
not access to Google Photos, Drive, or Gmail. It does not retain Google's access
or refresh tokens. Browser sessions belong to Memento and live in PostgreSQL.

## Create a Google client

1. Open [Google Auth Platform](https://console.cloud.google.com/auth/overview)
   and select or create a project.
2. Complete Branding with an app name, support email, and developer contact.
   Choose an External audience for friends and family outside your Workspace.
   While testing, add the Google accounts you will use under Audience's test
   users. Google's basic-sign-in scopes have exceptions to some testing limits;
   adding the accounts also avoids surprises if project settings change.
3. Under Data Access, use only the basic OpenID, email, and profile scopes. No
   Google Photos API, sensitive scopes, or offline access are needed.
4. Under Clients, create a **Web application** client. Add the exact authorized
   redirect URI, for example
   `https://photos.example.com/api/identity/google/callback`. For local testing,
   add `http://localhost:3579/api/identity/google/callback` as a separate URI.
   This server flow does not need an authorized JavaScript origin.
5. Copy the client ID and client secret into Memento's configuration. Keep the
   secret out of source control. Review Audience and Branding before publishing
   the client for your intended users.

Google requires an exact redirect URI match, including scheme, port, path, and
trailing slash. Memento also requires `GOOGLE_CALLBACK_URL` to equal `PUBLIC_URL`
plus `/api/identity/google/callback`. Never derive either value from a proxy's
forwarded host header.

## Production

Set these alongside `DATABASE_URL`, `IMMICH_URL`, and `IMMICH_API_KEY` from
`app.example.yaml`:

```sh
export APP_ENV=production
export AUTH_MODE=google
export PUBLIC_URL=https://photos.example.com
export GOOGLE_CLIENT_ID='your-client-id.apps.googleusercontent.com'
export GOOGLE_CLIENT_SECRET='your-client-secret'
export GOOGLE_CALLBACK_URL="$PUBLIC_URL/api/identity/google/callback"
```

Use HTTPS at the browser-facing reverse proxy. Session and login-state cookies
are Secure, HttpOnly, and SameSite=Lax. Discovery happens on the first sign-in,
not at startup. A Google outage does not prevent database health checks or use
of existing Memento sessions.

The first successful Google sign-in claims an empty installation and creates its
first Curator. Keep a new installation private until you have claimed it.

## Manual localhost check

This is a procedure to run with your own credentials, not a claim that the real
Google flow has been tested. Automated tests use a local OIDC server.

Use a disposable, empty Memento database and role, separate from Immich. Do not
reset a database that contains photos, access decisions, or people you need.
Build the single-process application so the browser and callback use one fixed
port. Unlike `mise start`, running the binary does not select a different port
or replace your database URL.

```sh
mise build
export CONFIG_FILE="$PWD/app.example.yaml"
export APP_ENV=development
export AUTH_MODE=google
export SERVER_HOST=127.0.0.1
export SERVER_PORT=3579
export PUBLIC_URL=http://localhost:3579
export GOOGLE_CALLBACK_URL="$PUBLIC_URL/api/identity/google/callback"
export GOOGLE_CLIENT_ID='your-client-id.apps.googleusercontent.com'
export GOOGLE_CLIENT_SECRET='your-client-secret'
export DATABASE_URL='postgres://memento:YOUR_PASSWORD@localhost:5432/memento_google_manual?sslmode=disable'
export IMMICH_URL='http://localhost:2283'
export IMMICH_API_KEY='your-read-only-immich-key'
export FILES_PATH="$PWD/tmp/google-manual-files"
./build/api/api
```

The database must already exist; startup applies its migrations. Open
`http://localhost:3579`, not `127.0.0.1`, and continue with Google using the
account intended to become the first Curator. HTTP is permitted only for the
exact `localhost` hostname in development or test. No public tunnel is needed.

Check that the browser returns to Memento signed in as Curator, then sign out
and sign back in with the same Google account. In browser storage, confirm the
Memento session cookie is HttpOnly and SameSite=Lax. It is deliberately not
Secure for this HTTP-only localhost check. Repeat over your production HTTPS
origin to verify Secure cookies.

## A later person's first sign-in

As Curator, create the Person and preauthorize the exact email Google reports
for that account. Use a separate browser profile or private window to sign in
with that Google account. The existing Person should gain a linked Google
identity, without receiving Curator status unless you granted it.

An unknown account must not gain access just because it has a verified Google
email. It receives a neutral no-access result. A Curator must preauthorize its
exact email before it can sign in. Reviewable Access Requests are not available
yet. Google's Audience test users and Memento's preauthorizations are separate
controls; a Google test-user entry does not grant Memento access.

## Troubleshooting

- `redirect_uri_mismatch`: compare the configured callback with the Google
  client's authorized redirect URI. Check the port and remove any extra slash.
- Expired or invalid sign-in: start again from Memento. Login transactions expire
  after ten minutes and can be used only once. Restarting Memento or starting a
  newer login in the same browser cancels the previous pending login.
- Google unavailable: retry later. Discovery and token requests have a timeout;
  a later attempt retries failed discovery.
- Access denied: use the preauthorized Google account or ask a Curator to
  preauthorize its exact email. Do not switch production to fake authentication.

The temporary login transaction stays in one server process. Memento's normal
single-process deployment needs no shared login-state store. Avoid load-balancing
one sign-in across multiple processes without sticky routing.

## References

- [Google OpenID Connect](https://developers.google.com/identity/openid-connect/openid-connect)
- [Google web-server OAuth flow and redirect URI rules](https://developers.google.com/identity/protocols/oauth2/web-server)
