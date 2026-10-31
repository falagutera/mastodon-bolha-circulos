package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// How many people fit in the three rings.
const (
	maxPosts  = 70
	maxPeople = 8 + 15 + 26
)

var errNoAccount = errors.New("account not found")

// store reads the Mastodon database. The README lists the columns it needs.
type store struct {
	pool *pgxpool.Pool

	mu    sync.Mutex
	query string // ranking query, built once the schema is known
}

func openStore(ctx context.Context, c config) (*store, error) {
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(c.dbUser, c.dbPassword),
		Host:     net.JoinHostPort(c.dbHost, c.dbPort),
		Path:     "/" + c.dbName,
		RawQuery: url.Values{"sslmode": {c.dbSSLMode}}.Encode(),
	}
	cfg, err := pgxpool.ParseConfig(u.String())
	if err != nil {
		return nil, fmt.Errorf("database settings: %w", err)
	}
	cfg.MaxConns = 3
	// Simple protocol so it also works behind pgbouncer in transaction mode.
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("database: %w", err)
	}
	return &store{pool: pool}, nil
}

// accountID finds a local account that can have a circle.
func (s *store) accountID(ctx context.Context, username string) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		SELECT id FROM accounts
		WHERE lower(username) = lower($1) AND COALESCE(lower(domain), '') = ''
		  AND suspended_at IS NULL AND silenced_at IS NULL`, username).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, errNoAccount
	}
	return id, err
}

// check runs both queries once, so a missing permission stops the start instead
// of failing on someone's circle.
func (s *store) check(ctx context.Context) error {
	if _, err := s.accountID(ctx, ""); err != nil && !errors.Is(err, errNoAccount) {
		return fmt.Errorf("reading accounts: %w", err)
	}
	if _, err := s.ranking(ctx, 0); err != nil {
		return fmt.Errorf("reading the ranking: %w", err)
	}
	return nil
}

// ranking returns the people closest to an account, closest first.
func (s *store) ranking(ctx context.Context, accountID int64) ([]int64, error) {
	q, err := s.rankingQuery(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, q, accountID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}

func (s *store) rankingQuery(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.query != "" {
		return s.query, nil
	}
	// The catalog lists every column. information_schema hides the ones the user
	// can't read, which would silently drop a filter below.
	rows, err := s.pool.Query(ctx, `
		SELECT attname::text FROM pg_attribute
		WHERE attrelid = 'public.statuses'::regclass AND attnum > 0 AND NOT attisdropped`)
	if err != nil {
		return "", err
	}
	columns, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return "", err
	}
	if len(columns) == 0 {
		return "", errors.New("the statuses table has no columns")
	}
	s.query = buildRankingQuery(columns)
	return s.query, nil
}

// buildRankingQuery follows what an anonymous visitor can see: public and
// unlisted posts only (visibility 0 and 1). Forks add ways to restrict a post
// further, so those columns are excluded when the schema has them.
func buildRankingQuery(columns []string) string {
	has := map[string]bool{}
	for _, c := range columns {
		has[c] = true
	}
	var extra strings.Builder
	if has["local_only"] {
		extra.WriteString(" AND s.local_only IS NOT TRUE")
	}
	if has["expires_at"] {
		extra.WriteString(" AND s.expires_at IS NULL")
	}
	visible := "s.deleted_at IS NULL AND s.visibility IN (0, 1)" + extra.String()
	return fmt.Sprintf(`
		WITH posts AS (
			SELECT s.id FROM statuses s
			WHERE s.account_id = $1 AND s.reblog_of_id IS NULL AND NOT s.reply AND %[1]s
			ORDER BY s.id DESC LIMIT %[2]d
		), interactions AS (
			SELECT f.account_id, 1.0::float8 AS weight
			FROM favourites f JOIN posts p ON p.id = f.status_id
			UNION ALL
			SELECT s.account_id, 1.1 FROM statuses s JOIN posts p ON p.id = s.in_reply_to_id WHERE %[1]s
			UNION ALL
			SELECT s.account_id, 1.3 FROM statuses s JOIN posts p ON p.id = s.reblog_of_id WHERE %[1]s
		)
		SELECT a.id
		FROM interactions i JOIN accounts a ON a.id = i.account_id
		WHERE a.id <> $1 AND a.suspended_at IS NULL AND a.silenced_at IS NULL
		  AND COALESCE(a.actor_type, 'Person') NOT IN ('Application', 'Service')
		GROUP BY a.id
		ORDER BY sum(i.weight) DESC, a.id
		LIMIT %[3]d`, visible, maxPosts, maxPeople)
}
