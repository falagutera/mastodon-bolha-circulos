# Changelog

All notable changes to circulos. Versions follow [SemVer](https://semver.org/); images are
published as `registry.gutocarvalho.net/images/circles:<version>-<build>`.

## 0.8.1 — 2026-11-13

- **The repository is now `open/bolha/circulos`** (the old path `open/bolha/circles` redirects), and the app calls itself circulos in the footer, the release notes and the docs. Settings (`CIRCLES_*`), the image (`images/circles`) and the cookies keep their names.

## 0.8.0 — 2026-11-13

- **New address and name: circulos.bolha.us, "Círculos"** (same in PT and EN). The old address circles.bolha.us redirects there.

## 0.7.0 — 2026-11-08

The bolha.io look, shared with the other tools.

- The page follows the system's light or dark theme, with the bolha.io header, a PT · EN switch and the standard footer.
- The circle image sits in a card; the image itself is unchanged.
- The page no longer loads web fonts (system fonts only); the image keeps its own.

## 0.6.4 — 2026-11-07

Hardening from the updated linters.

- A circle being built keeps the request's values but not its cancellation, and the cache lock is released in one place.
- The image declares its non-root user (65532) explicitly.

## 0.6.3 — 2026-11-06

- Release notes page, linked from the top of every page, and the same data at `/release-notes.json`.

## 0.6.2 — 2026-10-31

First tagged version on circles.bolha.us.

- Sign in with the instance and see your own circle: the accounts you interact with most, read from the instance database.
- New card: rings, IBM Plex, one background; hover an avatar for the full address.
- "Post on Mastodon" button to share the circle.
- English and Brazilian Portuguese, per visitor.
- Safer avatar fetching and read-only database access.
- Version in the footer.
