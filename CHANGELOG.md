# Changelog

## Planned for 2.0

- WebDAV, so the pool can be mounted as a drive or used with rclone.

## Unreleased

### Fixed

- Anything to or from Google that took over 30 seconds was cut off, which broke large downloads, zips and moves.

### Changed

- Uploads are now limited to 3 per account rather than 3 in total, so a slow account only holds up its own files. Up to 4 run at once over plain HTTP, and up to 12 behind a proxy with HTTP/2.
- Round robin passes over an account that already has 3 uploads going, while another has room.

### Added

- An optional nginx service for HTTPS with HTTP/2, in the `https` Compose profile, with `make cert` for a local certificate.
- Encrypted folders: new files in a folder you choose are encrypted before they reach Google, and everything but thumbnails and Google links keeps working. Back up `ENCRYPTION_KEYS`, because these files can't be recovered without it.
- File checks: Tributary reads files back in the background and reports any that are damaged, changed or gone, on the Storage page and in a banner. It downloads about 1 GB an hour, and the admin can turn it off in Server settings.
- A Duplicates page that finds files with the same content by their hash.
- Text and code files now open in the viewer.
- Search takes Gmail-style filters, such as `type:image`, `size:>10mb`, `after:2026-01-31`, `owner:name`, `in:folder`, `is:starred` and `-word`, with menus for the common ones, and results sort properly however many there are.

## 0.1.2 (7 November 2026)

### Added

- Moving something to the trash now asks first, from the menu, the toolbar, the Delete key and the viewer, and says if a Google link will end.
- File icons take their colour from the kind of file, and the app has more life: tiles respond to the pointer, buttons to a press, and dialogs and pages animate in. All of it stops for anyone who asks for less motion.

### Fixed

- The memory test no longer fails at random on a busy machine.
- The website's home page works properly on phones and tablets.
- In the demo, the banner no longer sits over the Undo button.

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
