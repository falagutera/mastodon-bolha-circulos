# circles

Draws the people closest to an account of a Mastodon instance: **circles.bolha.us**.

A person signs in with their account and gets three rings of the people who interacted the most with their latest public posts, as a PNG they can share, plus a list with links to each profile. Each person only sees their own circle. The people in it can be from anywhere, but only accounts of the instance can sign in. To offer it on your instance, run your own.

It's a rewrite in Go of [Fedi Circle Creator](https://github.com/AMNatty/Mastodon-Circles), which itself comes from [andigandhi/Mastodon-Circles](https://github.com/andigandhi/Mastodon-Circles). The original runs in the browser and makes around 200 API calls per circle. This one asks the database once.

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
       image: registry.gutocarvalho.net/open/circles:latest
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

To update: `docker compose pull && docker compose up -d`. For a fixed version, swap `latest` for a tag such as `0.6.0-808`.

To build it yourself instead, clone this repository, copy `.env.example` to `.env` and run `docker compose up -d --build`.

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

AGPL-3.0, as is the project it comes from. Fonts are IBM Plex (OFL) and Go fonts (BSD).
