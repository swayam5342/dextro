# dextro

A self-hosted file manager that turns several Google Drive accounts into one pool of storage.

Link your Drive accounts, then upload, organize, preview and share files from one web app. dextro keeps your files in your own Drive accounts, not on its own server.

## What it does

- **One pool from many accounts.** Uploads go to the account with the most free space, or to the one you pick. An account that can't be reached is skipped.
- **Real folders.** Folders appear in every linked account, inside a Drive folder named `dextro`.
- **Large uploads that survive problems.** Files go up in pieces, so an upload can pause, resume and recover from a lost connection. You can upload a whole folder, or drag one onto the page.
- **File management.** Sort, search, drag files onto folders, and move things to a trash with Undo. Images, video and PDFs preview in the app.
- **Zip downloads.** Download a folder or a selection as one zip.
- **Moving between accounts.** Move files to rebalance your accounts, or empty one before unlinking it. Each file is copied and checked before the old copy is removed.
- **Google links.** Share one file with a public link, choosing whether people can view, comment or edit, with an optional expiry. These are off until the admin turns them on.
- **Add from Drive.** Bring in files you already have in Drive, using Google's picker.
- **Accounts for other people.** One admin creates users, each with their own files and linked accounts.
- **API keys.** Scripts can upload and read files with a scoped key.
- **Light and dark themes.** Follows your device, or choose one.

## How it works

dextro runs as three parts:

| Part | What it does |
|---|---|
| Go server | The API, the background jobs and the database migrations |
| Node web server | Serves the React app and forwards API requests to the Go server |
| PostgreSQL | Keeps accounts, folders, file records and the audit log |

File contents pass through the Go server as a stream on their way to and from Drive. They're never saved on the server, and memory use doesn't grow with file size. Your server's bandwidth is the limit, and a move between accounts uses it twice, once in and once out.

Add from Drive moves no file contents, and neither does the sync. Google serves files opened through a Google link, so that traffic never reaches your server either.

dextro uses the `drive.file` scope, so it can only see files it created or that you chose in the picker. Anything else in your Drive stays invisible to it.

## Getting started

You need Docker, `make`, and a Google Cloud project.

### 1. Set up Google

In the [Google Cloud console](https://console.cloud.google.com/):

1. Enable the **Google Drive API**.
2. Configure the OAuth consent screen, and add the `drive.file` scope.
3. Create an **OAuth client ID** of type "Web application", with these authorized redirect URIs:

   ```
   http://127.0.0.1:3000/api/v1/auth/google/callback
   http://127.0.0.1:3000/api/v1/storage/google/callback
   ```

While the consent screen is in "Testing" status, every account you link has to be added as a test user, and its token expires after 7 days.

**For Add from Drive**, also enable the **Google Picker API** and create an API key limited to that API. Its allowed sites must include your app's address and `https://docs.google.com/*`.

### 2. Configure

```sh
make env
```

This creates `.env` with random secrets. Then add your Google values to it:

```sh
GOOGLE_CLIENT_ID=your-client-id
GOOGLE_CLIENT_SECRET=your-client-secret
GOOGLE_PICKER_API_KEY=your-key   # optional, turns on Add from Drive
```

> **Back up `ENCRYPTION_KEYS`.** It encrypts your stored Google tokens. Without it, every linked account has to be connected again.

### 3. Start

```sh
make up
```

Open <http://127.0.0.1:3000>. The first account you create becomes the admin, and sign-up closes after that. Then open **Storage** and link a Google Drive account.

`make up` stays in the foreground. To stop the stack and keep its data, press Ctrl+C or run `make down`.

## Settings

Docker Compose reads these from `.env`:

| Setting | Required | What it does |
|---|---|---|
| `POSTGRES_PASSWORD` | Yes | The database password. `make env` sets it. |
| `ENCRYPTION_KEYS` | Yes | Encrypts stored Google tokens, as comma-separated `id:base64` entries of 32 random bytes. `make env` sets it. |
| `ENCRYPTION_KEY_ID` | With several keys | Which key new values use. |
| `PUBLIC_URL` | No | The address browsers use. Defaults to `http://127.0.0.1:3000`. Requests that change data are refused from anywhere else. |
| `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET` | To link accounts | Set both or neither. Without them, Google sign-in and Drive are off. |
| `GOOGLE_PICKER_API_KEY` | No | Turns on Add from Drive. |

To rotate the encryption key, add a new key after a comma and point `ENCRYPTION_KEY_ID` at it. Keep the old key listed, because existing values still need it.

The admin can change the upload limit and turn Google links on or off in **Server settings**.

### Running on a real address

Session cookies are always `Secure`. Chrome accepts them on `http://127.0.0.1`, but any other address needs HTTPS, or sign-in won't stick. If you change `PUBLIC_URL`, update the redirect URIs in Google to match.

## Admin tasks

There's one admin. These run on the server:

```sh
# Hand the admin role to someone else
docker compose exec api /dextro transfer-admin someone@example.org

# Print a temporary password for someone locked out, the admin included
docker compose exec api /dextro reset-password someone@example.org
```

In Git Bash, put `MSYS_NO_PATHCONV=1` in front of these.

**Server status** shows whether each background job and linked account is healthy, and the **Audit log** records sign-ins, settings changes, links and permanent deletes.

## Changing the look

Every colour in the app is a named token in one file, [`web/src/theme.css`](web/src/theme.css), with a value for each theme:

```css
:root,
[data-theme="dark"] {
  --background: #000;
  --card: #111;
  --foreground: #fff;
  --water: #4cb3f0; /* storage levels */
}

[data-theme="light"] {
  --background: #fff;
  --card: #f3f3f3;
  --foreground: #0a0a0a;
  --water: #0a6fb0;
}
```

Change a value there and it changes everywhere. The tokens are grouped by purpose: surfaces, text, the main button, lines, danger, storage levels, and shadows.

To add a new token:

1. Add it to both themes in `theme.css`.
2. Map it in the `@theme inline` block in `web/src/index.css`, for example `--color-brand: var(--brand);`.
3. Use it as a class, such as `bg-brand` or `text-brand`.

Components should use token names only, never a colour value. Keep text at a contrast of at least 4.5:1 against its background in both themes.

## Development

| Command | What it does |
|---|---|
| `make tools` | Install the pinned Go tools into `bin/`. The first `sqlc` build is slow on Windows. |
| `make generate` | Regenerate code from the SQL queries and `api/openapi.yaml` |
| `make check-generated` | Fail if generated code is out of date |
| `make build` | Build the frontend and the Go server |
| `make lint` | Run golangci-lint, oxlint and the TypeScript compiler |
| `make test` | Run the Go and web server tests. Docker must be running. |
| `make run` | Run the Go server locally |
| `make dev-web` | Run the Vite dev server, proxying the API to `localhost:8080` |
| `make migration name=add_users` | Create a database migration |

Requirements: Go 1.27, Node 22 and pnpm.

### Where things are

```
api/openapi.yaml     The API contract. Change this first, then run make generate.
cmd/server           Starts everything. Wiring only.
internal/files       Files, folders, the trash, uploads, zips, moves, links and sync
internal/storage     Linked accounts and the storage pool
internal/provider    What storage has to provide, and the Google Drive adapter
internal/auth        Sign-in, sessions, users and API keys
internal/jobs        Background work
internal/db          Migrations and SQL queries
web/src              The React app, organized by feature
web/server           The production web server
```

Each backend feature has a `handler.go` for HTTP, a `service.go` for rules and a `store.go` for the database. [`CLAUDE.md`](CLAUDE.md) explains the design in full, including the reasoning behind the less obvious decisions.

## Security

- Files are private. The only public access dextro ever creates is a Google link that a person chose to make, on one file, and only if the admin has turned links on. Every link is listed on one page, audited, and ended when its file goes to the trash.
- Google tokens are encrypted at rest with AES-256-GCM, and the server refuses to start with placeholder secrets.
- Sessions use `HttpOnly`, `Secure` cookies stored hashed, and API keys are stored hashed. Keys can't delete anything permanently or touch links.
- Uploaded content is served with a sandbox policy. Only images, video and PDFs display in the browser, and everything else downloads.
- Every query that loads user data is scoped to that user.

## Good to know

- **A file lives whole in one account.** Free space adds up across accounts, but the largest file you can upload is limited by the account with the most room.
- **Changes made by hand in Drive mostly aren't copied back.** Deletions are noticed, but renames and moves aren't.
- **The trash is dextro's own.** It doesn't appear in Drive, and it only empties when you tell it to.
- **Link expiry needs the server running.** Google has no expiry for public links, so dextro removes them itself. If the server is off at that moment, the link keeps working until it's back.
- **Removing an account or user leaves their files in Drive.** dextro only forgets them. Deleting a user also leaves any Google links they made working.
- **Moves are one at a time.** Each file costs several Google requests, so moving thousands of small files takes hours. They run in the background, so you can close the browser.
- **Zips take time for many files.** Google takes about a second to start sending each one.
