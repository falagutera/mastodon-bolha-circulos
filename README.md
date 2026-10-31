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
   GRANT SELECT ON accounts, statuses, favourites TO circles;
   ```

   Allow it in `pg_hba.conf` (and in pgbouncer's user list if you use one; transaction mode works).

2. Register the OAuth application:

   ```sh
   curl -F client_name=circles -F scopes=profile \
     -F redirect_uris=https://circles.example.social/auth/callback \
     https://example.social/api/v1/apps
   ```

3. Configure and start it:

   ```sh
   cp .env.example .env   # URLs, OAuth application, secret, database host, user and password
   docker compose up -d
   ```

4. Point your reverse proxy at `127.0.0.1:8080`. It must serve HTTPS: the session cookie needs it.

## How it works

- The score is the sum of 1.0 per favourite, 1.1 per reply and 1.3 per boost on the latest 70 public posts, ignoring bots, suspended and limited accounts. The closest 49 people fill rings of 8, 15 and 26.
- It only counts what an anonymous visitor can see: public and unlisted posts. If the schema has `local_only` or `expires_at` (forks like ours add them) those posts are left out too. The columns are detected on the first circle, so restart after an upgrade that adds one.
- Avatars and display names come from the instance's public API, in two calls. An avatar that can't be used (AVIF, too big, offline) becomes a disc with the person's initial.
- Sign-in is OAuth with PKCE and the minimal `profile` scope. The token is used once to read the username, then revoked. The session is a signed cookie that lasts 4 hours.
- Finished circles stay in memory for an hour (`CIRCLES_CACHE_TTL`). Nothing is written to disk and no history is kept.
- No JavaScript, one small container, and the database user can't read e-mails or passwords.

## Development

    go test ./...

AGPL-3.0, as is the project it comes from. Fonts are IBM Plex (OFL) and Go fonts (BSD).
