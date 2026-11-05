# dextro

Self-hosted file storage gateway. Users connect their own Google Drive and S3-compatible accounts, then upload, organize, preview and share files from one web app. It runs as two processes plus PostgreSQL: a Go API server with the migrations embedded, and a Node web server that serves the React build and proxies the API.

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

The app is at <http://127.0.0.1:3000>. The Go API is also directly on <http://127.0.0.1:8080>. S3 (SeaweedFS) is at <http://127.0.0.1:8333>.

Go server environment: `DEXTRO_DATABASE_URL` (required), `DEXTRO_PUBLIC_URL` (required; the address browsers use, and the only `Origin` allowed to change data, so opening the app on `localhost` when this says `127.0.0.1` fails), `DEXTRO_LISTEN_ADDR` (default `:8080`), `DEXTRO_LOG_LEVEL` (default `info`), `DEXTRO_TRUSTED_PROXIES` (addresses or CIDR ranges whose `X-Forwarded-For` is believed; default none), `DEXTRO_GOOGLE_CLIENT_ID` and `DEXTRO_GOOGLE_CLIENT_SECRET` (set both or neither; without them Google sign-in and Drive are off), `DEXTRO_ENCRYPTION_KEYS` (required; comma-separated `id:base64` entries of 32 random bytes), `DEXTRO_ENCRYPTION_KEY_ID` (which key new values use; required with several keys), `DEXTRO_S3_ALLOWED_HOSTS` (hosts or `host:port` entries allowed to be on a private address, and to use plain HTTP; default none). With `make dev-web`, set `DEXTRO_PUBLIC_URL` to the Vite address.

Compose reads `PUBLIC_URL`, `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, `ENCRYPTION_KEYS`, `ENCRYPTION_KEY_ID` and `S3_ALLOWED_HOSTS` from `.env`. It pins the `web` service to `172.30.0.250` so that only it is a trusted proxy, and allows the dev `seaweedfs` host. If an old `dextro_default` network exists, run `make down` first so Compose can recreate it.

`make env` adds `ENCRYPTION_KEYS` to an existing `.env` if it's missing. Back that value up: without it, every storage connection has to be set up again. To rotate, add a new key after a comma and point `ENCRYPTION_KEY_ID` at it. Keep the old key listed, because existing values still need it.

Google's redirect URIs are `<PUBLIC_URL>/api/v1/auth/google/callback` (sign-in) and `<PUBLIC_URL>/api/v1/storage/google/callback` (Drive). The project also needs the Drive API enabled and the `drive.file` scope on its consent screen.

To test against a throwaway stack while the dev stack is up, use a Compose override with its own ports and subnet, and check it's really running before sending it anything. Registering on the dev stack by accident creates a real account there.

To connect the dev SeaweedFS, create a bucket with `echo "s3.bucket.create -name dev" | docker compose exec -T seaweedfs weed shell`, then use the endpoint `http://seaweedfs:8333` and the `S3_ACCESS_KEY` and `S3_SECRET_KEY` values from `.env`.

Roles change only from the command line: `docker compose exec api /dextro set-role <email> <user|admin>`. In Git Bash, prefix it with `MSYS_NO_PATHCONV=1`, or `/dextro` gets rewritten into a Windows path.

Session cookies are always `Secure`. Chrome accepts them on `http://127.0.0.1`, but anywhere else, such as a LAN address, needs HTTPS or login won't stick.

Node web server environment: `DEXTRO_API_URL` (default `http://localhost:8080`), `PORT` (default `3000`).

## How the repo works

- Organized by feature. Each backend feature has `handler.go` (HTTP only), `service.go` (business rules and access checks) and `store.go` (database access). Handlers never touch the database directly.
- `cmd/server/main.go` is wiring only.
- `api/openapi.yaml` is the contract. Change it first, run `make generate`, then implement. Paths are written in full, including `/api/v1`.
- The Go server code is generated into `internal/httpx/apigen`. `httpx.api` implements its `ServerInterface` by embedding each feature's handler. Features import `httpx`, so `httpx` names them by interface (`AuthHandlers`, `AdminHandlers`) and `main.go` passes them in.
- Access rules live in `internal/httpx/access.go` and wrap every generated route. Everything needs a session unless it's in `publicRoutes`, and everything under `/api/v1/admin/` goes through `requireAdmin`. oapi-codegen ignores `security` in `openapi.yaml`, so that block is documentation only. `access_test.go` walks the real router, so new routes are covered automatically.
- `internal/provider` is the contract every storage service meets, with the adapters in `s3` and `gdrive`. A new adapter must pass `providertest.Run`, which also runs against the in-memory `providertest.Fake` that service tests use. Adapters stay inside an area dextro owns: the `dextro/` prefix of a bucket, or a Drive folder named `dextro`. S3 uploads go up one 16 MiB part at a time, by hand: `feature/s3/manager` is deprecated and `transfermanager` is pre-1.0.
- `internal/storage` is the feature that manages connections. `internal/secrets` seals credentials, bound to the connection's ID so a value can't move between rows. `internal/netguard` checks user-supplied addresses; its `HTTPClient` checks the address at connect time, which is what stops DNS rebinding, so every request to a user-supplied endpoint must go through it.
- Drive uses the `drive.file` scope: dextro sees only files it created. Don't widen it.
- `internal/files` is the feature for files, folders and the trash. Folders exist only in Postgres; objects sit flat in the provider, so renaming and moving never touch it. dextro lists only what it uploaded. Trashing a folder marks that folder alone, and its contents travel with it. The trash never empties itself, because rule 14 makes permanent deletion an explicit action. Removing a connection cascades away its file records, but the objects stay in the account.
- Permanent deletes remove the object first and the record second, and treat an object that's already gone as success. That order is what makes a failed delete safe to retry.
- Inline display is an allowlist of exact types in `internal/files/handler.go`, not `image/*`, because an SVG is an image that can run scripts. Everything else is served as `application/octet-stream` with `attachment`.
- API keys start with `dxt_` and go in `Authorization: Bearer`. They reach only the routes in `keyRoutes` in `access.go`, which is closed by default. Permanent deletes aren't on that list on purpose, so a leaked key can trash files but can't destroy them. `apikey_access_test.go` holds a separate copy of the list, so widening access takes two changes.
- oapi-codegen checks required query parameters before the access middleware runs, so an upload's parameters aren't marked required in `openapi.yaml`: that would answer anonymous requests with 400 instead of 401. The handler checks them.
- To write to two tables atomically, use a store's `InTx`. Its `Audit()` and `Settings()` run on the same transaction.
- Google sign-in never links to an existing account, even with a verified, matching email: password accounts never prove they own their address. Linking happens only from the account page, by a signed-in user. Don't "improve" this into auto-linking.
- **The TypeScript API types are hand-written.** `web/src/api/schema.ts` mirrors `openapi.yaml` in the shape `openapi-fetch` expects. Update it in the same commit as the contract. Nothing checks that they match. `openapi-typescript` isn't used because it requires TypeScript 5 and this project is on TypeScript 7.
- Frontend linting uses `oxlint`, because `typescript-eslint` doesn't support TypeScript 7.
- SQL lives in `internal/db/queries`, generating into `internal/db/dbgen`. Migrations live in `internal/db/migrations` and run automatically at startup under an advisory lock.
- Generated code is committed. Never edit it by hand.
- Frontend: server data goes through TanStack Query. Routes are code-based: each feature exports its routes from `routes.tsx`, and `web/src/app/router.tsx` assembles them.
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
3. The first account on an empty instance becomes admin, safely under concurrent sign-ups (use a transaction). No other account is ever promoted automatically. A CLI command changes roles.
4. Registration can be turned off through an `allow_registration` setting that an admin can change. It must also block first-time Google sign-ins. The first account can always be created.
5. Google sign-in may link to an existing account only when Google reports the email as verified. Email/password sign-up must not let someone pre-claim an address and capture that person's Google sign-in later.
6. Never set public or "anyone with the link" permissions on a provider. Sharing happens only through this app's own share links: random token, stored hashed, optional expiry, view-only by default, revocable.
7. Uploads stream end to end through `io.Reader`, with memory use that doesn't grow with file size. Never buffer a whole file. Enforce size limits while streaming.
8. Browser auth uses opaque session tokens in `HttpOnly`, `Secure`, `SameSite=Lax` cookies, stored hashed in the database. No tokens in `localStorage`. Requests that change data must pass an `Origin` check. Sessions can be revoked. API keys are separate, stored hashed, with scopes.
9. Serve user content with `X-Content-Type-Options: nosniff` and `Content-Security-Policy: sandbox`. Only images, video and PDF may display inline; everything else downloads. Sanitize filenames in `Content-Disposition`.
10. Encrypt stored OAuth tokens and S3 credentials with AES-256-GCM, saving a key ID with each value so keys can be rotated. Refuse to start if secrets are missing or are placeholder values.
11. Check user-supplied S3 endpoints against SSRF: block private, loopback and link-local addresses, including after DNS resolution.
12. Rate-limit login, registration and share-link endpoints.
13. Clients get a stable error code and safe message only. Full details go to the logs, with a request ID. Never log secrets or tokens.
14. Deleting a file moves it to the trash. Permanent deletion is a separate, explicit action.
15. Record security-relevant actions in `audit_events`: logins, role changes, settings changes, shares, permanent deletes.
