# circles

Draws the people closest to an account of a Mastodon instance: **circles.bolha.us**.

A person signs in with their account and gets three rings of the people who interacted the most with their latest public posts, as a PNG they can share, plus a list with links to each profile. Each person only sees their own circle. The people in it can be from anywhere, but only accounts of the instance can sign in. To offer it on your instance, run your own.

It's a rewrite in Go of [Fedi Circle Creator](https://github.com/AMNatty/Mastodon-Circles), which itself comes from [andigandhi/Mastodon-Circles](https://github.com/andigandhi/Mastodon-Circles). The original runs in the browser and makes around 200 API calls per circle. This one asks the database once.

## Testing and security

circles reads the Mastodon database, which also holds private keys, e-mails and direct messages, and fetches avatars from other servers. So it is tested against what would hurt: showing someone else's circle, counting posts an anonymous visitor can't see, needing more than the column grants below, and being used to reach internal addresses. What is done today (2026-11-08):

**Every pipeline.** GitLab CI ([`.gitlab-ci.yml`](.gitlab-ci.yml), plus the shared security stage from `open/gcn/ci-templates`, file `security.yml`). The test job runs on every pipeline; the security stage runs on every merge request and every push to `main`; the image is built and published only from `main`.

- Blocking (the pipeline fails and no image is published):
  - `go vet`, a `gofmt` check, and the full test suite, with a real PostgreSQL 17 for the ranking query;
  - `govulncheck`: known vulnerabilities in Go code paths circles actually reaches (in the test job and in the security stage);
  - `gitleaks`: secrets committed to the repository;
  - `trivy`: critical vulnerabilities that have a fix, secrets and misconfigurations in the repository.
- Informative (reported, not blocking): `semgrep`, `gosec`, `hadolint` (Dockerfile), `yamllint`, and `poutine` (security of the CI pipeline itself).

**The test suite.** 25 tests (`go test -list`). The page tests run the real HTTP handlers against a fake database and a fake instance directory; one test runs the real ranking query on PostgreSQL (it skips without `CIRCLES_TEST_DB_HOST`; CI sets it). They cover:

- privacy: anonymous visitors only get the sign-in page and never reach the database, forged or wrong-purpose cookies are refused, each person gets only their own circle, the person in the center is never replaced by someone else;
- the ranking, on PostgreSQL: only public and unlisted posts count (no followers-only, direct, local-only, expiring or deleted post, no private reply), bots, suspended and limited accounts never appear, and the app starts and ranks as a role that has only the grants in step 1 of *Install*;
- sign-in and sharing: PKCE and state, the sign-in token is revoked, nothing is posted before the person approves sharing, sign-out refuses other sites;
- fetching avatars: private, loopback, link-local and CGNAT addresses are refused, broken or huge images are rejected or scaled down;
- bounded work: concurrent builds, a panic costs one circle or one avatar and not the server;
- the image and the page: ring sizes and positions, text fitting, both languages (the image carries text too), the release notes.

**Reviews.** No penetration test or outside review has been run on circles yet. Hardening done by the project: on 2026-10-31, avatar fetching (https and public addresses only), builds, and how the database grants are checked; in 0.6.4, the findings of the updated linters (`gosec`, `semgrep`, `hadolint`).

**Reporting a vulnerability.** Write to contato@gutocarvalho.net. Please don't open a public issue for it.

## Architecture

**Why it exists.** "Circle" images (who interacts with you the most) are popular on Mastodon, but the tools that make them run in the browser and make around 200 API calls per circle, against the instance and many others. When the instance runs its own copy, one database query does the same work, and the person's token isn't handed to a third-party site.

**In plain words.**

1. the person signs in with their account on the instance (OAuth, `profile` scope only); the token is used once to read the username, then revoked;
2. circles runs one read-only query on the Mastodon database: who favourited, replied to or boosted the person's latest 70 public posts, and how often;
3. it asks the instance's public API for the names and avatars of the closest 49 people, fetches the avatars, and draws the PNG;
4. if the person wants to post it, the instance asks them to approve a second, short-lived token that publishes one post and is then revoked.

**Components.**

```
            browser
               |
         circles (this app, no JavaScript)
          |        |          \
          |        |           +--> avatar servers (https, public addresses only)
          |        |
          |        +--> Mastodon PostgreSQL  (read-only role, column grants only, 5 s
          |                                   statement timeout; pgbouncer works)
          |
          +--> the Mastodon instance
                - OAuth sign-in (token revoked right away)
                - public API: names and avatars
                - optional: one post with the circle, after a separate approval
```

- Nothing is stored by circles: the session is a signed cookie (4 hours), and finished circles stay in memory for `CIRCLES_CACHE_TTL` (1 hour by default, at most 64 at once). No disk, no history.
- The database role can read only the columns listed in step 1 of *Install*: no post text, e-mails, passwords, tokens or private keys.
- Work is bounded: 2 circles built at once, 45 seconds each, 8 avatar fetches at once, 3 database connections.

## Install

You need Docker, and network access from it to your Mastodon PostgreSQL.

1. Create a read-only database user:

   ```sql
   CREATE ROLE circles LOGIN PASSWORD 'change-me';
   ALTER ROLE circles SET statement_timeout = '5s';
   ALTER ROLE circles SET default_transaction_read_only = on;
   GRANT CONNECT ON DATABASE mastodon_production TO circles;
   GRANT USAGE ON SCHEMA public TO circles;
   GRANT SELECT (id, username, domain, suspended_at, silenced_at, actor_type) ON accounts TO circles;
   GRANT SELECT (id, account_id, reblog_of_id, in_reply_to_id, reply, visibility, deleted_at) ON statuses TO circles;
   GRANT SELECT (account_id, status_id) ON favourites TO circles;
   ```

   Those tables also hold private keys and direct messages, so grant columns, never whole tables.
   Forks that restrict posts further need those columns too, or circles refuses to start. On
   bolha.us: `GRANT SELECT (local_only, expires_at) ON statuses TO circles;`.

   Allow it in `pg_hba.conf` (and in pgbouncer's user list if you use one; transaction mode works).

2. Register the OAuth application:

   ```sh
   curl -F client_name=circles -F "scopes=profile write:media write:statuses" \
     -F redirect_uris=https://circles.example.social/auth/callback \
     https://example.social/api/v1/apps
   ```

3. Create a folder with these two files. The image is ready to pull, so you don't need this repository.

   `.env`:

   ```ini
   CIRCLES_INSTANCE=https://example.social
   CIRCLES_BASE_URL=https://circles.example.social
   CIRCLES_CLIENT_ID=the client_id from step 2
   CIRCLES_CLIENT_SECRET=the client_secret from step 2
   CIRCLES_SECRET=the output of: openssl rand -hex 32
   CIRCLES_DB_HOST=10.0.0.10
   CIRCLES_DB_USER=circles
   CIRCLES_DB_PASSWORD=change-me
   ```

   `docker-compose.yml`:

   ```yaml
   services:
     circles:
       image: registry.gutocarvalho.net/images/circles:latest
       container_name: circles
       restart: unless-stopped
       env_file: .env
       read_only: true
       cap_drop: [ALL]
       security_opt: ["no-new-privileges:true"]
       mem_limit: 256m
       ports:
         - "127.0.0.1:8080:8080"
   ```

4. Start it: `docker compose up -d`.

5. Point your reverse proxy at `127.0.0.1:8080`. It must serve HTTPS: the session cookie needs it.

To update: `docker compose pull && docker compose up -d`. For a fixed version, swap `latest` for a tag such as `0.6.1-848`.

To build it yourself instead, clone this repository, copy `.env.example` to `.env` and run `docker compose up -d --build`.

### Versions

Versions look like `X.Y.Z-BUILD`. `X.Y.Z` is in the `VERSION` file and follows SemVer, and `BUILD` is the GitLab pipeline number. A local build shows `X.Y.Z-dev`. The page footer shows the version and links to the [container registry](https://gitlab.gutocarvalho.net/images/circles/container_registry).

What changed in each version is in [CHANGELOG.md](CHANGELOG.md), which the app also serves as its release notes page (`/release-notes`, linked at the top of the page) and as `/release-notes.json`. Add an entry when you bump `VERSION`: a test fails without it.

| Image tag | Moves? |
|---|---|
| `X.Y.Z-BUILD` | Never. Pin this one in production. |
| `X.Y.Z` | On each build of that version. |
| `latest` | On each build. |

### Settings

| Variable | Required | Default | What it is |
|---|---|---|---|
| `CIRCLES_INSTANCE` | yes |  | Your instance. Only its accounts can draw a circle. |
| `CIRCLES_BASE_URL` | yes |  | Where this app is served, always https. |
| `CIRCLES_CLIENT_ID` | yes |  | OAuth application. |
| `CIRCLES_CLIENT_SECRET` | yes |  | OAuth application. |
| `CIRCLES_SECRET` | yes |  | Signs the session cookie. |
| `CIRCLES_DB_HOST` | yes |  | Mastodon PostgreSQL, reachable from the container. |
| `CIRCLES_DB_USER` | yes |  | Read-only user from step 1. |
| `CIRCLES_DB_PASSWORD` | yes |  | Its password. |
| `CIRCLES_DB_PORT` | no | `5432` | Use your pgbouncer port if you have one. |
| `CIRCLES_DB_NAME` | no | `mastodon_production` | Database name. |
| `CIRCLES_DB_SSLMODE` | no | `prefer` | PostgreSQL `sslmode`. |
| `CIRCLES_LANG` | no | `en` | `en` or `pt`, when the browser asks for neither. |
| `CIRCLES_CACHE_TTL` | no | `1h` | How long a circle is kept in memory. |
| `CIRCLES_LISTEN` | no | `:8080` | Address inside the container. |

## How it works

- The score is the sum of 1.0 per favourite, 1.1 per reply and 1.3 per boost on the latest 70 public posts, ignoring bots, suspended and limited accounts. The closest 49 people fill rings of 8, 15 and 26, and the closest 8 are named on the card.
- It only counts what an anonymous visitor can see: public and unlisted posts. If the schema has `local_only` or `expires_at` (forks like ours add them) those posts are left out too. The columns are detected on the first circle, so restart after an upgrade that adds one.
- Avatars and display names come from the instance's public API, in two calls. An avatar that can't be used (AVIF, too big, offline) becomes a disc with the person's initial.
- Sign-in is OAuth with PKCE and the minimal `profile` scope. The token is used once to read the username, then revoked. The session is a signed cookie that lasts 4 hours. Posting a circle is a separate approval on the instance, for `write:media` and `write:statuses`: that token publishes one public post and is revoked as well.
- Finished circles stay in memory for an hour (`CIRCLES_CACHE_TTL`). Nothing is written to disk and no history is kept.
- On the page the image sits in an SVG: hovering an avatar shows the full address, clicking opens the profile. This needs no JavaScript either.
- English and Brazilian Portuguese, including the image. It follows the browser, with a selector at the top to switch (kept in a cookie).
- No JavaScript, one small container, and the database user can't read e-mails or passwords.

## Development

    go test ./...

Changing the code, with or without a coding agent: [`AGENTS.md`](AGENTS.md) has the commands
(including the ranking test against a throwaway PostgreSQL), a map of the files and the rules a
change must keep.

AGPL-3.0, as is the project it comes from. The PNG is drawn with IBM Plex (OFL) and Go fonts (BSD); the page uses system fonts.
