package main

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
)

// testDB opens the database named by CIRCLES_TEST_DB_* and rebuilds the slice of
// Mastodon's schema circles reads. It drops the public schema, so it refuses a
// database whose name does not end in _test. Without CIRCLES_TEST_DB_HOST the
// test is skipped.
func testDB(t *testing.T) (*store, config) {
	t.Helper()
	host := os.Getenv("CIRCLES_TEST_DB_HOST")
	if host == "" {
		t.Skip("CIRCLES_TEST_DB_HOST not set")
	}
	cfg := config{dbHost: host, dbPort: env("CIRCLES_TEST_DB_PORT", "5432"), dbName: env("CIRCLES_TEST_DB_NAME", "circles_test"),
		dbUser: env("CIRCLES_TEST_DB_USER", "postgres"), dbPassword: env("CIRCLES_TEST_DB_PASSWORD", "pg"), dbSSLMode: "disable"}
	if !strings.HasSuffix(cfg.dbName, "_test") {
		t.Fatalf("refusing to drop the schema of %q: the name must end in _test", cfg.dbName)
	}
	ctx := context.Background()
	s, err := openStore(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.pool.Close)
	if _, err := s.pool.Exec(ctx, testSchema); err != nil {
		t.Fatal(err)
	}
	return s, cfg
}

// testSchema has only the columns circles reads, plus the two a fork adds.
const testSchema = `
DROP SCHEMA IF EXISTS public CASCADE;
CREATE SCHEMA public;
CREATE TABLE accounts (id bigint PRIMARY KEY, username text NOT NULL, domain text, suspended_at timestamp,
  silenced_at timestamp, actor_type text, private_key text);
CREATE TABLE statuses (id bigint PRIMARY KEY, account_id bigint NOT NULL, reblog_of_id bigint, in_reply_to_id bigint,
  reply boolean NOT NULL DEFAULT false, visibility integer NOT NULL DEFAULT 0, deleted_at timestamp,
  local_only boolean, expires_at timestamp, text text);
CREATE TABLE favourites (account_id bigint NOT NULL, status_id bigint NOT NULL);`

// fixtures: guto (1) has one post of each kind; each other account interacts
// with exactly one of them, so the expected ranking names who must count.
const fixtures = `
INSERT INTO accounts (id, username, domain, suspended_at, silenced_at, actor_type) VALUES
  (1, 'guto', NULL, NULL, NULL, NULL), (2, 'fan', NULL, NULL, NULL, 'Person'), (3, 'dm', NULL, NULL, NULL, NULL),
  (4, 'bot', NULL, NULL, NULL, 'Service'), (5, 'gone', NULL, now(), NULL, NULL), (6, 'limited', NULL, NULL, now(), NULL),
  (7, 'replier', 'far.example', NULL, NULL, NULL), (8, 'dmreply', NULL, NULL, NULL, NULL), (9, 'booster', NULL, NULL, NULL, NULL),
  (10, 'localonly', NULL, NULL, NULL, NULL), (11, 'expiring', NULL, NULL, NULL, NULL), (12, 'deleted', NULL, NULL, NULL, NULL),
  (13, 'followers', NULL, NULL, NULL, NULL), (14, 'unlisted', NULL, NULL, NULL, NULL), (15, 'guto', 'far.example', NULL, NULL, NULL),
  (16, 'app', NULL, NULL, NULL, 'Application');
INSERT INTO statuses (id, account_id, visibility, local_only, expires_at, deleted_at) VALUES
  (100, 1, 0, NULL, NULL, NULL), (101, 1, 1, NULL, NULL, NULL), (102, 1, 2, NULL, NULL, NULL), (103, 1, 3, NULL, NULL, NULL),
  (104, 1, 0, true, NULL, NULL), (105, 1, 0, NULL, now() + interval '1 day', NULL), (106, 1, 0, NULL, NULL, now());
INSERT INTO statuses (id, account_id, in_reply_to_id, reply, visibility) VALUES (200, 7, 100, true, 0), (201, 8, 100, true, 3);
INSERT INTO statuses (id, account_id, reblog_of_id, visibility) VALUES (300, 9, 100, 0);
INSERT INTO favourites (account_id, status_id) VALUES
  (1, 100), (2, 100), (3, 103), (4, 100), (5, 100), (6, 100), (10, 104), (11, 105), (12, 106), (13, 102), (14, 101), (16, 100);`

// appGrants are the grants of the README (step 1, with the fork's columns): the
// app must start and rank with nothing more.
const appGrants = `
DO $$ BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'circles_app') THEN CREATE ROLE circles_app LOGIN PASSWORD 'app'; END IF;
END $$;
GRANT USAGE ON SCHEMA public TO circles_app;
GRANT SELECT (id, username, domain, suspended_at, silenced_at, actor_type) ON accounts TO circles_app;
GRANT SELECT (id, account_id, reblog_of_id, in_reply_to_id, reply, visibility, deleted_at) ON statuses TO circles_app;
GRANT SELECT (account_id, status_id) ON favourites TO circles_app;
GRANT SELECT (local_only, expires_at) ON statuses TO circles_app;`

// TestRankingAgainstPostgres runs the real query: only interactions with public
// and unlisted posts count (no followers-only, DM, local-only, expiring or
// deleted post, no reply that is itself private), and bots, suspended and
// limited accounts never appear.
func TestRankingAgainstPostgres(t *testing.T) {
	s, cfg := testDB(t)
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx, fixtures); err != nil {
		t.Fatal(err)
	}
	if id, err := s.accountID(ctx, "GUTO"); err != nil || id != 1 {
		t.Fatalf("accountID(GUTO) = %d, %v", id, err)
	}
	for _, name := range []string{"gone", "limited", "nobody"} {
		if _, err := s.accountID(ctx, name); !errors.Is(err, errNoAccount) {
			t.Errorf("accountID(%s): %v", name, err)
		}
	}
	got, err := s.ranking(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	// booster 1.3, replier 1.1, then fan and unlisted at 1.0 (ties by id).
	if want := []int64{9, 7, 2, 14}; !slices.Equal(got, want) {
		t.Fatalf("ranking = %v, want %v", got, want)
	}

	// The same, as a role with only the README's grants.
	if _, err := s.pool.Exec(ctx, appGrants); err != nil {
		t.Fatal(err)
	}
	app := cfg
	app.dbUser, app.dbPassword = "circles_app", "app"
	as, err := openStore(ctx, app)
	if err != nil {
		t.Fatal(err)
	}
	defer as.pool.Close()
	if err := as.check(ctx); err != nil {
		t.Fatalf("the README grants are not enough: %v", err)
	}
	if got, err := as.ranking(ctx, 1); err != nil || !slices.Equal(got, []int64{9, 7, 2, 14}) {
		t.Fatalf("as the app: %v, %v", got, err)
	}
}
