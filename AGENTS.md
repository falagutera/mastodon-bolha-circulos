# AGENTS.md

Notes for coding agents (and people) changing circulos. The README says what the app does and how
to install it; this file says how to change it without breaking the promises the README makes.

## What it is

One Go binary, no JavaScript, no ORM. A person signs in with OAuth on the instance and gets a
PNG of the people who interacted most with their latest public posts. The ranking is **one query**
on the Mastodon database (read-only, column-level grants); names and avatars come from the
instance's public API. Circles live in memory for `CIRCLES_CACHE_TTL`; nothing goes to disk.

## Commands

```sh
gofmt -l .            # must print nothing
go vet ./...
go test ./...         # without CIRCLES_TEST_DB_HOST the database test skips itself (CI sets it)
```

With a database (CI runs this one against a PostgreSQL 17 service); run it locally too whenever
you touch `store.go` or the grants. It **drops and recreates the `public` schema** of whatever it
reaches and refuses a database whose name does not end in `_test`: use a throwaway container on a
free port and chain with `&&` (`-h 127.0.0.1` waits for the real server, not the init one).

```sh
P=55651   # any free port
docker run -d --name circles-pg-$P -p $P:5432 -e POSTGRES_PASSWORD=pg -e POSTGRES_DB=circles_test postgres:17 &&
  until docker exec circles-pg-$P pg_isready -h 127.0.0.1 -U postgres -q; do sleep 1; done &&
  CIRCLES_TEST_DB_HOST=127.0.0.1 CIRCLES_TEST_DB_PORT=$P go test ./...
docker rm -f circles-pg-$P
```

Test variables: `CIRCLES_TEST_DB_HOST`, `_PORT` (5432), `_NAME` (circles_test), `_USER` (postgres),
`_PASSWORD` (pg). The test also creates a role `circles_app` with only the README's grants.

To look at a rendered circle (fake people, initials instead of avatars):
`D=$(mktemp -d) && CIRCLES_DUMP=$D go test -run TestOwnCircle ./... && echo $D`
writes `circle.html`, `circle.png` and `assets/` there. Running the app itself needs a Mastodon
database and an OAuth app (README, Install).

## Map

| File | What |
|---|---|
| `main.go` | config from `CIRCLES_*` env, wiring, HTTP server timeouts, `-healthcheck` |
| `server.go` | routes, `securityHeaders` (CSP), page handlers, error pages |
| `auth.go` | OAuth (state + PKCE), signed cookies (`seal`/`open`), `/share` (post the circle), token revoke |
| `store.go` | the database: `accountID`, `buildRankingQuery`, `check` (run at startup) |
| `circle.go` | `builder`: in-memory cache, build slots, timeouts, panic recovery, `compose` |
| `instance.go` | names and avatars from the public API (`/api/v1/accounts?id[]=`) |
| `fetch.go` | `mediaClient`: the only client for remote URLs (public addresses only) |
| `render.go` | the PNG card: ring sizes and positions, fonts, avatar decode limits, hover `spots` |
| `i18n.go` | every text, in `en` and `pt`, including the words drawn on the image |
| `templates/` | `layout.html` (head/foot), `index.html`, `circle.html` (SVG over the PNG), `notes.html` |
| `assets/` | `style.css` (copy from open/gcn/ui via its `sync.sh`, never edited here) and `circles.css` (the app's own rules, with the tokens); `fonts/` (repo root) are the TTFs drawn into the PNG only. The look is [VISUAL.md](VISUAL.md); check with `open/gcn/ui/check.sh --card-fonts .` |
| `internal/relnotes` | copy of the shared release-notes package (synced from its own repo, never edited here) |

Tests: `circles_test.go` (fake database `fakeDB` and directory `fakeDir`, `testServer`, `get`,
`sessionFor`; pages, sign-in, share, language, SSRF, panics), `store_test.go` (`testSchema`,
`fixtures`, `appGrants`; `TestRankingAgainstPostgres`), `i18n_test.go`, `changelog_test.go`,
`dump_test.go` (the `CIRCLES_DUMP` helper).

## Rules that must hold

1. **Each person sees only their own circle.** No route takes a username: `/` and `/circle.png`
   use the signed session's account; anonymous visitors get the login and never reach the database.
   Cookies are HMAC-signed with a purpose, so a state cookie can't pass as a session.
   `TestAnonymousSeesOnlyTheLogin`, `TestBadSessionsAreRejected`, `TestOwnCircle`. The person in the
   center is never replaced by someone else (`compose`, `TestMissingSelfIsNotReplaced`).
2. **Only what an anonymous visitor can see counts.** `buildRankingQuery` applies the same filter
   to the posts, the replies and the boosts: not deleted, visibility 0 or 1, and, when the schema
   has them, not `local_only` and no `expires_at`. Fork columns are detected from `pg_attribute`
   (not `information_schema`, which hides columns without a grant and would silently drop the
   filter). Bots (`actor_type` Application/Service), suspended and silenced accounts never appear.
   `TestRankingQueryFollowsSchema` (the filter appears 3 times), `TestRankingAgainstPostgres`
   (each excluded case has a fixture row).
3. **Read-only, least privilege.** The README's step 1 lists every column granted. A new column
   means: the grant in the README, the column in `testSchema`, the grant in `appGrants`, and a
   changelog line telling installs to re-grant. If the column adds an exclusion, also name it in
   the README's "How it works" and in rule 2's list above. Never grant `accounts.private_key`, e-mails,
   passwords, tokens or post text. `check()` runs both queries at startup so a missing grant stops
   the app instead of a circle. Keep `QueryExecModeSimpleProtocol` (pgbouncer transaction mode).
4. **Tokens never outlive the request.** Sign-in asks only `profile`; the token reads the
   username and is revoked (`defer s.revokeToken`). Posting is a separate approval with
   `shareScope`, publishes one public post with the state as `Idempotency-Key`, and redirects only
   to a URL on the instance. `TestLoginFlow`, `TestShareFlow` (nothing is posted before approval).
   Logout is a POST refused cross-site (`TestLogoutRefusesOtherSites`).
5. **Remote URLs only through `mediaClient`.** Avatars are https only, checked at dial time
   against private, loopback, link-local and CGNAT addresses (also after DNS and on redirects),
   at most 3 redirects, 5 MiB (`maxAvatar`) and 1024 px (`maxDimension`).
   `TestMediaClientRefusesPrivateAddresses`, `TestDecodeAvatarRejectsGarbageAndHuge`.
6. **No JavaScript, no inline style.** CSP in `securityHeaders` is `default-src 'none'` with
   `'self'` for script/style/img; CSS goes in `assets/circles.css`. (`script-src 'self'` exists
   only for a bar an edge proxy may inject.) Names from the instance are escaped by the templates.
7. **Both languages.** Every key in `translations` exists in `en` and `pt`
   (`TestTextsInBothLanguages`); the PNG carries text, so the cache key includes the language
   (`TestLanguage`).
8. **Bounded work.** 2 builds at once (`slots`), 45 s per build, 64 cached circles (`maxKeep`),
   8 avatar fetches at once, 3 database connections, `statement_timeout` on the role. A panic in
   a build or an avatar costs that circle or avatar, not the process
   (`TestPanicDoesNotStopTheServer`, `TestAvatarPanicCostsOneAvatar`, `TestConcurrentBuilds`).

## Changing the ranking (the usual change)

0. **Check it is not there already.** Read `buildRankingQuery` and the README's "How it works":
   weights (1.0 favourite, 1.1 reply, 1.3 boost), 70 posts (`maxPosts`), 49 people (`maxPeople`,
   which must equal the sum of `ringSizes` in `render.go`).
1. Change the SQL in `buildRankingQuery`; any new source of interactions also gets `%[1]s`. A new
   exclusion of accounts goes in the final `WHERE` on `accounts a`, and needs a fixture account
   that does interact, so the test proves it is left out.
2. **Only if it reads a column not granted yet:** rule 3. If only some forks have it, detect it
   with `has[...]` like `local_only`.
3. Fixture rows in `store_test.go` for what must count and for what must not (a DM, a
   followers-only post, a bot), and the expected order in `TestRankingAgainstPostgres`.
4. Texts in both languages if the page or the image says something new; update the README if a
   number or promise it states changed.
5. `gofmt`, `go vet`, `go test` with the database, and look at the dump.

## Releasing

- `VERSION` holds `X.Y.Z`; CI adds the build number (`X.Y.Z-BUILD`). Which part to bump: a fix or a
  small visual tweak is a patch; a new feature, option or detail is a minor; a change that needs a
  new grant or setting at install is a minor whose changelog entry starts with that step in **bold**;
  a breaking change is a major; docs, tests or CI only need no bump. Work not released yet can sit
  under `## Unreleased` in `CHANGELOG.md` (`internal/relnotes` skips that section).
- Every version needs its `CHANGELOG.md` entry (`TestChangelogHasVersion`); it is also the public
  `/release-notes` page and `/release-notes.json`, so write it for the people who use the site.
- Code, comments, commits and the README in English. Keep commits small with a message that says why.

## Security checks

CI runs `go vet`, `gofmt -l`, the tests (with PostgreSQL 17, so the ranking query and the app-role
grants are checked), govulncheck and the shared security stage: gitleaks,
govulncheck, trivy, gosec, semgrep, poutine, hadolint, shellcheck, yamllint. Before a release that
touches the query, sign-in, sharing or avatar fetching, also review who can see what and where
requests can go — the part scanners do not see.
