# dextro

Self-hosted file storage gateway. Users link several Google Drive accounts, which work as one pool of storage, then upload, organize, preview and share files from one web app. Google Drive is the only storage: S3 was removed on purpose. It runs as two processes plus PostgreSQL: a Go API server with the migrations embedded, and a Node web server that serves the React build and proxies the API.

## Commands

| Command | What it does |
|---|---|
| `make tools` | Install pinned Go tools into `bin/` (the first `sqlc` build takes about 10 minutes on Windows) |
| `make generate` | Regenerate sqlc and oapi-codegen output |
| `make check-generated` | Fail if generated code is out of date |
| `make build` | Build the frontend into `web/dist` and the Go server into `bin/` |
| `make lint` | golangci-lint, oxlint and `tsc` |
| `make test` | Go and web server tests; Docker must be running for the Postgres tests |
| `make env` | Create `.env` with random secrets |
| `make up` / `make down` | Start or stop the stack with Docker Compose |
| `make run` / `make run-web` | Run the Go server or the Node web server locally |
| `make dev-web` | Vite dev server, proxying the API to `localhost:8080` |
| `make migration name=add_users` | Create a goose migration |

On Windows, use `make up` rather than plain `docker compose up`. If the terminal opened at `d:` instead of `D:`, plain Compose builds fail with `additional privileges requested: pass "--allow=fs.read=Dockerfile"`. Docker compares the paths case-sensitively; `make up` corrects the path first.

The app is at <http://127.0.0.1:3000>. The Go API is also directly on <http://127.0.0.1:8080>.

Go server environment: `DEXTRO_DATABASE_URL` (required), `DEXTRO_PUBLIC_URL` (required; the address browsers use, and the only `Origin` allowed to change data, so opening the app on `localhost` when this says `127.0.0.1` fails), `DEXTRO_LISTEN_ADDR` (default `:8080`), `DEXTRO_LOG_LEVEL` (default `info`), `DEXTRO_TRUSTED_PROXIES` (addresses or CIDR ranges whose `X-Forwarded-For` is believed; default none), `DEXTRO_GOOGLE_CLIENT_ID` and `DEXTRO_GOOGLE_CLIENT_SECRET` (set both or neither; without them Google sign-in and Drive are off), `DEXTRO_ENCRYPTION_KEYS` (required; comma-separated `id:base64` entries of 32 random bytes), `DEXTRO_ENCRYPTION_KEY_ID` (which key new values use; required with several keys). With `make dev-web`, set `DEXTRO_PUBLIC_URL` to the Vite address.

Compose reads `PUBLIC_URL`, `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, `ENCRYPTION_KEYS` and `ENCRYPTION_KEY_ID` from `.env`. It pins the `web` service to `172.30.0.250` so that only it is a trusted proxy. If an old `dextro_default` network exists, run `make down` first so Compose can recreate it.

`make env` adds `ENCRYPTION_KEYS` to an existing `.env` if it's missing. Back that value up: without it, every storage connection has to be set up again. To rotate, add a new key after a comma and point `ENCRYPTION_KEY_ID` at it. Keep the old key listed, because existing values still need it.

Google's redirect URIs are `<PUBLIC_URL>/api/v1/auth/google/callback` (sign-in) and `<PUBLIC_URL>/api/v1/storage/google/callback` (Drive). The project also needs the Drive API enabled and the `drive.file` scope on its consent screen. While that screen is in "Testing" status, every linked account must be a test user and its token expires after 7 days.

To test against a throwaway stack while the dev stack is up, use a Compose override with its own ports and subnet, and check it's really running before sending it anything. Registering on the dev stack by accident creates a real account there.

There is one admin. Move the role with `docker compose exec api /dextro transfer-admin <email>`. If someone is locked out, the admin included, `docker compose exec api /dextro reset-password <email>` prints a temporary password. In Git Bash, prefix either with `MSYS_NO_PATHCONV=1`, or `/dextro` gets rewritten into a Windows path.

Session cookies are always `Secure`. Chrome accepts them on `http://127.0.0.1`, but anywhere else, such as a LAN address, needs HTTPS or login won't stick.

Node web server environment: `DEXTRO_API_URL` (default `http://localhost:8080`), `PORT` (default `3000`).

## How the repo works

- Organized by feature. Each backend feature has `handler.go` (HTTP only), `service.go` (business rules and access checks) and `store.go` (database access). Handlers never touch the database directly.
- `cmd/server/main.go` is wiring only.
- `api/openapi.yaml` is the contract. Change it first, run `make generate`, then implement. Paths are written in full, including `/api/v1`.
- The Go server code is generated into `internal/httpx/apigen`. `httpx.api` implements its `ServerInterface` by embedding each feature's handler. Features import `httpx`, so `httpx` names them by interface (`AuthHandlers`, `AdminHandlers`) and `main.go` passes them in.
- Access rules live in `internal/httpx/access.go` and wrap every generated route. Everything needs a session unless it's in `publicRoutes`, and everything under `/api/v1/admin/` goes through `requireAdmin`. oapi-codegen ignores `security` in `openapi.yaml`, so that block is documentation only. `access_test.go` walks the real router, so new routes are covered automatically.
- `internal/provider` is the contract the storage code meets, implemented by `gdrive`. `providertest.Run` is the shared suite, which also runs against the in-memory `providertest.Fake` that service tests use. The adapter stays inside a Drive folder named `dextro`.
- `internal/storage` is the feature that manages linked accounts. `internal/secrets` seals tokens, bound to the connection's ID so a value can't move between rows.
- `internal/storage/pool.go` is the pool. An upload goes to the account with the most free space, or to the account the user picked, which is used or refused and never swapped for another. Accounts that can't be reached are passed over, so one expired token doesn't stop uploads. A file is stored whole in one account, so free space adds up but a file must fit in one account. Free space is read from Google on each upload. Unfinished uploads hold their full size in `upload_sessions`, and that counts as used until they finish, so two uploads can't both count on the same room. `Reserve` asks Google first and only takes turns for the choosing, because holding a lock across a network call would use up the connection pool.
- `gdrive.Client` keeps one token source per refresh token, in memory. Every request opens its account afresh, and without this each one would fetch a new access token, a full trip to Google on every file and thumbnail.
- Drive uses the `drive.file` scope: dextro sees only files it created. Don't widen it. The consent prompt is `select_account consent`: `select_account` is what lets someone link a second account.
- `internal/files` is the feature for files, folders and the trash. Folders exist only in Postgres; objects sit flat in the provider, so renaming and moving never touch it. Trashing a folder marks that folder alone, and its contents travel with it. The trash never empties itself, because rule 14 makes permanent deletion an explicit action. Removing a connection cascades away its file records, but the objects stay in the account.
- `internal/files/sync.go` makes dextro match each account. Files in the `dextro` folder with no record come back at the top level, which is how a lost database recovers, and records of files gone from Drive are removed. It runs when an account is linked, every 5 minutes as a job, and from the Sync button. `drive.file` means it only ever sees files dextro created, so something added by hand in Drive stays invisible, and folders and the app's trash don't come back because they exist only in Postgres.
- Removing is the dangerous half of a sync, because a failure can look like an empty account. It removes nothing unless `Check` passes (a trashed folder lists as empty), the listing reaches the end, and `Exists` confirms that exact object is gone, because a listing can lag behind a new file. Adding is the other way round: `syncGrace` skips objects newer than 10 minutes, because an upload stores its object just before saving its record. Importing in that moment would make the upload's own save fail, and it would then delete the object.
- The browser uploads through `internal/files/uploads.go`: it starts a session, then sends 8 MiB pieces that stream through to Drive. `received` comes from the account, not from what was sent, because Drive can keep less, and each piece has to start exactly there. A piece that doesn't gets 409, and `GET /api/v1/uploads/{id}` asks Drive where things really are. A finished session keeps its row with `file_id` for a day, so a browser that lost the last answer can still find its file. Empty files and API keys use the one-request `POST /api/v1/files`.
- A Drive session address grants access to the upload, so it's sealed like a token, bound to its row and dropped when the upload finishes. Go puts the URL in HTTP client errors, so `plainError` in `gdrive/resumable.go` strips it before anything can reach a log.
- Unfinished uploads are listed by the server, not IndexedDB, so they survive a reload and show on any device. A browser can't keep access to a file, so the user picks it again and the name, size and modified time have to match. A job clears uploads idle for 24 hours.
- Permanent deletes remove the object first and the record second, and treat an object that's already gone as success. That order is what makes a failed delete safe to retry.
- Inline display is an allowlist of exact types in `internal/files/handler.go`, not `image/*`, because an SVG is an image that can run scripts. Everything else is served as `application/octet-stream` with `attachment`.
- API keys start with `dxt_` and go in `Authorization: Bearer`. They reach only the routes in `keyRoutes` in `access.go`, which is closed by default. Permanent deletes aren't on that list on purpose, so a leaked key can trash files but can't destroy them. `apikey_access_test.go` holds a separate copy of the list, so widening access takes two changes.
- oapi-codegen checks required query parameters before the access middleware runs, so an upload's parameters aren't marked required in `openapi.yaml`: that would answer anonymous requests with 400 instead of 401. The handler checks them.
- To write to two tables atomically, use a store's `InTx`. Its `Audit()` and `Settings()` run on the same transaction.
- Google sign-in never links to an existing account, even with a verified, matching email: password accounts never prove they own their address. Linking happens only from the account page, by a signed-in user. Don't "improve" this into auto-linking.
- The admin manages accounts through `internal/auth/users.go`, whose routes sit under `/api/v1/admin/users`. They live in `auth` because they need its password hashing and sessions. The admin sees each account's email and how many accounts it has linked, never its files, and can't delete or reset their own account there.
- A password the admin chose must be replaced. `must_change_password` is set when the admin creates a user or sets a password, and when `reset-password` runs. `requirePasswordChange` in `access.go` then refuses everything except `passwordChangeRoutes`, for API keys too, so it's closed by default like the other route lists. Only changing the password clears it, and the new one can't match the old.
- Deleting a user removes their sessions, keys, linked accounts and file records through foreign keys. The files stay in their Google Drive. Their email is written onto their audit events first, because the delete blanks `actor_id`.
- `internal/jobs` runs background work on `river`, inside the Go server, which also applies river's own migrations at startup. The quota sync and upload cleanup run hourly, session cleanup daily and the file sync every 5 minutes, and all of them run at startup. Each finish is saved in `job_runs`, because river clears finished jobs on its own schedule. A job stopped by shutdown isn't recorded.
- The quota sync saves `checked_at`, `check_ok` and `used_bytes` on each connection for the admin's status page. Uploads and the Storage page still ask Google directly, so don't point them at the saved values. A shutdown cuts Google calls off, so `SyncQuotas` stops rather than recording accounts as broken.
- `internal/admin` imports `storage` for the status page, and `auth` no longer imports `admin`.
- The audit log's `before` parameter is a string in `openapi.yaml`, because the generated code rejects a bad number before the access checks run.
- **The TypeScript API types are hand-written.** `web/src/api/schema.ts` mirrors `openapi.yaml` in the shape `openapi-fetch` expects. Update it in the same commit as the contract. Nothing checks that they match. `openapi-typescript` isn't used because it requires TypeScript 5 and this project is on TypeScript 7.
- Frontend linting uses `oxlint`, because `typescript-eslint` doesn't support TypeScript 7.
- SQL lives in `internal/db/queries`, generating into `internal/db/dbgen`. Migrations live in `internal/db/migrations` and run automatically at startup under an advisory lock.
- Generated code is committed. Never edit it by hand.
- Frontend: server data goes through TanStack Query. Routes are code-based: each feature exports its routes from `routes.tsx`, and `web/src/app/router.tsx` assembles them.
- `web/src/app/app-shell.tsx` is the frame around every signed-in page: sidebar with the pool meter, top bar with search and the upload button, and tabs on a phone. `UploadsProvider` lives inside it so uploads keep running between pages. Sign-in and error pages use `centered-page.tsx`.
- There's one dark theme, with its tokens in `web/src/index.css`. The ground is true black, elevation is a step in lightness, and colour goes only on files and storage levels, never on the chrome. Instrument Sans is bundled through fontsource because the CSP blocks font CDNs.
- The grid uses thumbnails from `/api/v1/files/{id}/thumbnail`, which `provider.Thumbnail` fetches from Drive. Only files with a preview ask for one, and a tile falls back to an icon if it fails. Tiles take their shape from the picture once it loads.
- On touch screens the select circles stay hidden until selecting starts, which happens from a tile's menu.
- Radix's scroll lock adds a `<style>` element that the CSP blocks, so opening a dialog or menu logs a console error. It's harmless: the page scrolls inside `main`, not on the body.
- `web/server` is the production web server, using only Node's standard library. Browsers talk only to it, and it proxies `/api` and `/health` to Go, so everything stays on one origin: no CORS, and session cookies stay first-party. Don't point the browser straight at the Go server. The proxy streams request and response bodies and has no request timeout, because uploads pass through it.
- The proxy overwrites `X-Forwarded-For` with the real client address. Only trust that header on the Go side from the proxy.
- The UI's Content-Security-Policy lives in `web/server/app.mjs`. Go sets a stricter one on API responses.
- After `shadcn add`, check the new components: the CLI once imported `cn` from a bogus npm package of that name and added it to `package.json`. `cn` is our helper in `@/lib/utils`.

## Conventions

- Keep it simple: no speculative abstractions, no features outside the current milestone, and a new dependency has to earn its place.
- Tests go alongside the code: service tests with the fake provider, store tests against real Postgres (`dbtest.Start`), handler tests with `httptest`. Handler tests include that users can't reach each other's data and that non-admins get 403.
- Work one milestone at a time on a branch per milestone, with small commits. Don't push unless asked.
- Keep this file current.

## Security rules (non-negotiable)

1. Store functions that load user data take the user ID, e.g. `GetFileForOwner(ctx, userID, fileID)`. Never write an unscoped `GetFile(id)`. A public share-link lookup is a separate, clearly named function that goes by hashed token.
2. Instance-wide settings and maintenance routes sit in one route group behind `requireAdmin`. A logged-in user is not automatically an admin.
3. The first account on an empty instance becomes admin, safely under concurrent sign-ups (use a transaction). There is exactly one admin, enforced by a unique index. No other account is ever promoted automatically, and `transfer-admin` on the command line is the only way the role moves.
4. Only the first account can sign up, by password or through Google. After that the admin creates accounts. Sign-up is refused before the email is looked at, so it can't show which addresses have accounts.
5. Google sign-in may link to an existing account only when Google reports the email as verified. Email/password sign-up must not let someone pre-claim an address and capture that person's Google sign-in later.
6. Never set public or "anyone with the link" permissions on a provider. Sharing happens only through this app's own share links: random token, stored hashed, optional expiry, view-only by default, revocable.
7. Uploads stream end to end through `io.Reader`, with memory use that doesn't grow with file size. Never buffer a whole file. Enforce size limits while streaming.
8. Browser auth uses opaque session tokens in `HttpOnly`, `Secure`, `SameSite=Lax` cookies, stored hashed in the database. No tokens in `localStorage`. Requests that change data must pass an `Origin` check. Sessions can be revoked. API keys are separate, stored hashed, with scopes.
9. Serve user content with `X-Content-Type-Options: nosniff` and `Content-Security-Policy: sandbox`. Only images, video and PDF may display inline; everything else downloads. Sanitize filenames in `Content-Disposition`.
10. Encrypt stored OAuth tokens with AES-256-GCM, saving a key ID with each value so keys can be rotated. Refuse to start if secrets are missing or are placeholder values.
11. Retired with S3, which was the only way to supply an address for the server to call. If that ever comes back, check it against SSRF, including after DNS resolution.
12. Rate-limit login, registration and share-link endpoints.
13. Clients get a stable error code and safe message only. Full details go to the logs, with a request ID. Never log secrets or tokens.
14. Deleting a file moves it to the trash. Permanent deletion is a separate, explicit action.
15. Record security-relevant actions in `audit_events`: logins, role changes, settings changes, shares, permanent deletes.
