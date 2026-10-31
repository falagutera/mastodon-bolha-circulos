package main

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"
)

var errBuildFailed = errors.New("circle build failed")

type ring struct {
	Name   string
	People []person
}

// circle is everything a result page needs.
type circle struct {
	Self  person
	Rings []ring
	Spots []spot
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

// faces guards the font faces, which keep a glyph cache and can't be shared by
// two builds at once.
var faces sync.Mutex

func newBuilder(db database, dir directory, c config) *builder {
	client := mediaClient(5 * time.Second)
	return &builder{
		db:      db,
		dir:     dir,
		fetch:   func(ctx context.Context, url string) *avatar { return fetchAvatar(ctx, client, url) },
		host:    strings.TrimPrefix(strings.TrimPrefix(c.baseURL, "https://"), "http://"),
		ttl:     c.cacheTTL,
		slots:   make(chan struct{}, 2),
		maxKeep: 64,
		cache:   map[string]*entry{},
	}
}

func (b *builder) get(ctx context.Context, username, lang string) (*circle, error) {
	key := strings.ToLower(username) + "|" + lang // the image carries text
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
		go b.build(key, username, lang, e)
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

func (b *builder) build(key, username, lang string, e *entry) {
	defer close(e.done)
	defer func() {
		if r := recover(); r != nil {
			log.Printf("circle of %s: %v", username, r)
			e.result, e.err = nil, errBuildFailed
		}
		if e.err != nil {
			b.mu.Lock()
			delete(b.cache, key) // only successful circles stay
			b.mu.Unlock()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	b.slots <- struct{}{}
	defer func() { <-b.slots }()
	e.result, e.err = b.compose(ctx, username, lang)
}

func (b *builder) compose(ctx context.Context, username, lang string) (*circle, error) {
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
	if len(people) == 0 || people[0].ID != strconv.FormatInt(id, 10) {
		return nil, errNoAccount // never put someone else in the center
	}
	self, rest := people[0], people[1:]
	rings := splitRings(rest)
	avatars := b.loadAvatars(ctx, append([]person{self}, rest...))
	img, areas := drawLocked(self, rings, avatars, b.host, newCardText(lang))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return &circle{
		Self:  self,
		Rings: []ring{{"inner", rings[0]}, {"middle", rings[1]}, {"outer", rings[2]}},
		Spots: areas,
		PNG:   buf.Bytes(),
	}, nil
}

func drawLocked(self person, rings [3][]person, avatars map[string]*avatar, host string, t cardText) (*image.RGBA, []spot) {
	faces.Lock()
	defer faces.Unlock()
	return drawCircle(self, rings, avatars, host, t), spots(self, rings)
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
			defer func() {
				if r := recover(); r != nil { // a decoder bug costs one avatar
					log.Printf("avatar of %s: %v", p.ID, r)
				}
			}()
			a := b.fetch(ctx, p.Avatar)
			mu.Lock()
			out[p.ID] = a
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}
