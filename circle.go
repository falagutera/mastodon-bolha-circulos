package main

import (
	"bytes"
	"context"
	"errors"
	"image/png"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

var (
	errOtherInstance = errors.New("account from another instance")
	errBadHandle     = errors.New("invalid handle")
)

var usernameRe = regexp.MustCompile(`^[A-Za-z0-9_]+([A-Za-z0-9_.-]*[A-Za-z0-9_])?$`)

// parseHandle accepts guto, @guto and @guto@bolha.us. Only accounts of this
// instance get a circle.
func parseHandle(in, domain string) (string, error) {
	in = strings.TrimPrefix(strings.TrimSpace(in), "@")
	user, host, remote := strings.Cut(in, "@")
	if remote && !strings.EqualFold(host, domain) {
		return "", errOtherInstance
	}
	if len(user) > 64 || !usernameRe.MatchString(user) {
		return "", errBadHandle
	}
	return user, nil
}

type ring struct {
	Name   string
	People []person
}

// circle is everything a result page needs.
type circle struct {
	Self  person
	Rings []ring
	PNG   []byte
}

// splitRings fills the rings from the closest person out.
func splitRings(people []person) [3][]person {
	var rings [3][]person
	for i, size := range ringSizes {
		n := min(size, len(people))
		rings[i], people = people[:n], people[n:]
	}
	return rings
}

type database interface {
	accountID(ctx context.Context, username string) (int64, error)
	ranking(ctx context.Context, accountID int64) ([]int64, error)
}

type directory interface {
	people(ctx context.Context, ids []int64) ([]person, error)
}

type entry struct {
	done    chan struct{}
	result  *circle
	err     error
	created time.Time
}

// builder caches circles in memory, so a popular link costs one build and
// concurrent requests for the same person share it.
type builder struct {
	db      database
	dir     directory
	fetch   func(ctx context.Context, url string) *avatar
	host    string
	ttl     time.Duration
	slots   chan struct{}
	maxKeep int

	mu    sync.Mutex
	cache map[string]*entry
}

func newBuilder(db database, dir directory, c config) *builder {
	client := &http.Client{Timeout: 5 * time.Second}
	return &builder{
		db:      db,
		dir:     dir,
		fetch:   func(ctx context.Context, url string) *avatar { return fetchAvatar(ctx, client, url) },
		host:    c.domain,
		ttl:     c.cacheTTL,
		slots:   make(chan struct{}, 2),
		maxKeep: 64,
		cache:   map[string]*entry{},
	}
}

func (b *builder) get(ctx context.Context, username string) (*circle, error) {
	key := strings.ToLower(username)
	b.mu.Lock()
	e, ok := b.cache[key]
	if ok && time.Since(e.created) > b.ttl {
		select {
		case <-e.done:
			ok = false
		default:
		}
	}
	if !ok {
		e = &entry{done: make(chan struct{}), created: time.Now()}
		b.evict()
		b.cache[key] = e
		b.mu.Unlock()
		go b.build(key, e)
	} else {
		b.mu.Unlock()
	}
	select {
	case <-e.done:
		return e.result, e.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// evict makes room for one more entry, dropping the oldest finished one.
func (b *builder) evict() {
	if len(b.cache) < b.maxKeep {
		return
	}
	oldest := ""
	for k, e := range b.cache {
		select {
		case <-e.done:
			if oldest == "" || e.created.Before(b.cache[oldest].created) {
				oldest = k
			}
		default:
		}
	}
	if oldest != "" {
		delete(b.cache, oldest)
	}
}

func (b *builder) build(key string, e *entry) {
	defer close(e.done)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	b.slots <- struct{}{}
	defer func() { <-b.slots }()
	e.result, e.err = b.compose(ctx, key)
	if e.err != nil {
		b.mu.Lock()
		delete(b.cache, key) // only successful circles stay
		b.mu.Unlock()
	}
}

func (b *builder) compose(ctx context.Context, username string) (*circle, error) {
	id, err := b.db.accountID(ctx, username)
	if err != nil {
		return nil, err
	}
	ids, err := b.db.ranking(ctx, id)
	if err != nil {
		return nil, err
	}
	people, err := b.dir.people(ctx, append([]int64{id}, ids...))
	if err != nil {
		return nil, err
	}
	if len(people) == 0 {
		return nil, errNoAccount
	}
	self, rest := people[0], people[1:]
	rings := splitRings(rest)
	img := drawCircle(self, rings, b.loadAvatars(ctx, append([]person{self}, rest...)), b.host)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return &circle{
		Self:  self,
		Rings: []ring{{"inner", rings[0]}, {"middle", rings[1]}, {"outer", rings[2]}},
		PNG:   buf.Bytes(),
	}, nil
}

func (b *builder) loadAvatars(ctx context.Context, people []person) map[string]*avatar {
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		out   = make(map[string]*avatar, len(people))
		limit = make(chan struct{}, 8)
	)
	for _, p := range people {
		wg.Add(1)
		go func() {
			defer wg.Done()
			limit <- struct{}{}
			defer func() { <-limit }()
			a := b.fetch(ctx, p.Avatar)
			mu.Lock()
			out[p.ID] = a
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}
