# Changelog

## Planned for 2.0

- WebDAV, so the pool can be mounted as a drive or used with rclone.

## 0.1.1 (7 November 2026)

### Added

- Recent and Starred pages, with a star in each file's menu.
- Keyboard shortcuts: `/` to search, Delete to trash the selection, F2 to rename, and Ctrl+A to select everything.
- A banner when an account can't be reached or is nearly full, and a Reconnect button on broken accounts.
- Uploading a folder again now offers to skip files that are already there.
- Resumable uploads for API keys.
- A `backup` service in the Compose files, which copies the database once a day and keeps a week.
- The server has a version, shown on the status page and by `tributary-cli version`.
- Docker images for arm64 as well as amd64.

### Fixed

- Behind a reverse proxy, every visitor counted as the proxy, so they shared one login rate limit and the audit log showed the wrong address. Set `DEXTRO_TRUSTED_PROXIES` on the web service, or use the Compose file on the website, which does it for a proxy on the same machine.
- Pressing Escape in a dialog no longer clears your selection.
- The browser tab icon no longer shows the app's old logo.

### Upgrading

The database changes by itself when the server starts. If you use the Compose file from the website, download it again to get the `backup` service and the trusted proxy setting.

## 0.1.0 (6 November 2026)

The first published release: linked Google Drive accounts working as one pool, upload routing, resumable uploads, real folders in Drive, moves between accounts, public Google links, zip downloads, API keys and an admin CLI.
