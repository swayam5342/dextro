# dextro

Self-hosted file storage gateway. Users connect their own Google Drive and S3-compatible accounts, then upload, organize, preview and share files from one web app. One Go binary embeds the React frontend and the migrations; PostgreSQL is the only other dependency.

## Commands

| Command | What it does |
|---|---|
| `make tools` | Install pinned Go tools into `bin/` (the first `sqlc` build takes about 10 minutes on Windows) |
| `make generate` | Regenerate sqlc and oapi-codegen output |
| `make check-generated` | Fail if generated code is out of date |
| `make build` | Build the frontend, then the server into `bin/` |
| `make lint` | golangci-lint, oxlint and `tsc` |
| `make test` | Go tests; Docker must be running for the Postgres tests |
| `make env` | Create `.env` with random secrets |
| `make up` / `make down` | Start or stop the stack with Docker Compose |
| `make dev-web` | Vite dev server, proxying the API to `localhost:8080` |
| `make migration name=add_users` | Create a goose migration |

On Windows, use `make up` rather than plain `docker compose up`. If the terminal opened at `d:` instead of `D:`, plain Compose builds fail with `additional privileges requested: pass "--allow=fs.read=Dockerfile"`. Docker compares the paths case-sensitively; `make up` corrects the path first.

The app is at <http://127.0.0.1:8080>. S3 (SeaweedFS) is at <http://127.0.0.1:8333>.

Environment: `DEXTRO_DATABASE_URL` (required), `DEXTRO_LISTEN_ADDR` (default `:8080`), `DEXTRO_LOG_LEVEL` (default `info`).

## How the repo works

- Organized by feature. Each backend feature has `handler.go` (HTTP only), `service.go` (business rules and access checks) and `store.go` (database access). Handlers never touch the database directly.
- `cmd/server/main.go` is wiring only.
- `api/openapi.yaml` is the contract. Change it first, run `make generate`, then implement. Paths are written in full, including `/api/v1`.
- The Go server code is generated into `internal/httpx/apigen`. `httpx.api` implements its `ServerInterface`.
- **The TypeScript API types are hand-written.** `web/src/api/schema.ts` mirrors `openapi.yaml` in the shape `openapi-fetch` expects. Update it in the same commit as the contract. Nothing checks that they match. `openapi-typescript` isn't used because it requires TypeScript 5 and this project is on TypeScript 7.
- Frontend linting uses `oxlint`, because `typescript-eslint` doesn't support TypeScript 7.
- SQL lives in `internal/db/queries`, generating into `internal/db/dbgen`. Migrations live in `internal/db/migrations` and run automatically at startup under an advisory lock.
- Generated code is committed. Never edit it by hand.
- Frontend: server data goes through TanStack Query. Routes are code-based: each feature exports its routes from `routes.tsx`, and `web/src/app/router.tsx` assembles them.
- `web/dist/.gitkeep` lets Go compile before the frontend is built. `web/public/.gitkeep` restores it after each Vite build.
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
