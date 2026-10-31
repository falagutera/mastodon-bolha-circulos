package main

import (
	"bytes"
	"context"
	"errors"
	"html/template"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseHandle(t *testing.T) {
	for in, want := range map[string]string{
		"guto": "guto", "@guto": "guto", " @guto@bolha.us ": "guto", "guto@BOLHA.US": "guto", "a.b_c": "a.b_c",
	} {
		if got, err := parseHandle(in, "bolha.us"); err != nil || got != want {
			t.Errorf("parseHandle(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := parseHandle("guto@mastodon.social", "bolha.us"); !errors.Is(err, errOtherInstance) {
		t.Errorf("other instance: %v", err)
	}
	for _, in := range []string{"", "@", "a/b", "a b", "../x", "guto.", strings.Repeat("a", 65)} {
		if _, err := parseHandle(in, "bolha.us"); !errors.Is(err, errBadHandle) {
			t.Errorf("parseHandle(%q): %v", in, err)
		}
	}
}

func TestRankingQueryFollowsSchema(t *testing.T) {
	vanilla := buildRankingQuery([]string{"id", "visibility"})
	if strings.Contains(vanilla, "local_only") || strings.Contains(vanilla, "expires_at") {
		t.Fatal("a stock Mastodon has no local_only or expires_at")
	}
	fork := buildRankingQuery([]string{"local_only", "expires_at"})
	// posts, replies and boosts must all filter.
	if n := strings.Count(fork, "s.local_only IS NOT TRUE"); n != 3 {
		t.Fatalf("local_only filtered %d times", n)
	}
	if n := strings.Count(fork, "s.expires_at IS NULL"); n != 3 {
		t.Fatalf("expires_at filtered %d times", n)
	}
	if n := strings.Count(vanilla, "s.visibility IN (0, 1)"); n != 3 {
		t.Fatalf("visibility filtered %d times", n)
	}
}

func TestSplitRings(t *testing.T) {
	people := make([]person, 60)
	r := splitRings(people)
	if len(r[0]) != 8 || len(r[1]) != 15 || len(r[2]) != 26 {
		t.Fatalf("got %d %d %d", len(r[0]), len(r[1]), len(r[2]))
	}
	r = splitRings(make([]person, 10))
	if len(r[0]) != 8 || len(r[1]) != 2 || len(r[2]) != 0 {
		t.Fatalf("got %d %d %d", len(r[0]), len(r[1]), len(r[2]))
	}
}

func TestDecodeAvatarRejectsGarbageAndHuge(t *testing.T) {
	if decodeAvatar([]byte("not an image")) != nil {
		t.Fatal("garbage decoded")
	}
	var buf bytes.Buffer
	png.Encode(&buf, image.NewAlpha(image.Rect(0, 0, maxDimension+1, 1)))
	if decodeAvatar(buf.Bytes()) != nil {
		t.Fatal("oversized image decoded")
	}
}

func TestFitText(t *testing.T) {
	long := "@" + strings.Repeat("alguem", 20) + "@" + strings.Repeat("instancia", 20) + ".example"
	got := fitText(labelFace, long, 120)
	if textWidth(labelFace, got) > 120 || !strings.HasSuffix(got, "…") {
		t.Fatalf("got %q", got)
	}
	if got := fitText(labelFace, "@guto", 120); got != "@guto" {
		t.Fatalf("short text changed: %q", got)
	}
	if got := fitText(labelFace, "ação longa", 1); got != "…" {
		t.Fatalf("tiny width: %q", got)
	}
}

func TestDecodeAvatarDownscales(t *testing.T) {
	var buf bytes.Buffer
	png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 800, 800)))
	a := decodeAvatar(buf.Bytes())
	if a == nil || a.img.Bounds().Dx() != 2*selfRadius {
		t.Fatalf("avatar not downscaled: %+v", a)
	}
}

type dirWithoutSelf struct{ fakeDir }

func (d dirWithoutSelf) people(ctx context.Context, ids []int64) ([]person, error) {
	return d.fakeDir.people(ctx, ids[1:])
}

func TestMissingSelfIsNotReplaced(t *testing.T) {
	b := newBuilder(&fakeDB{}, dirWithoutSelf{}, config{domain: "bolha.us", cacheTTL: time.Hour})
	b.fetch = func(context.Context, string) *avatar { return nil }
	if _, err := b.get(context.Background(), "guto"); !errors.Is(err, errNoAccount) {
		t.Fatalf("got %v", err)
	}
}

type fakeDB struct{ calls atomic.Int32 }

func (f *fakeDB) accountID(_ context.Context, u string) (int64, error) {
	if strings.ToLower(u) == "nobody" {
		return 0, errNoAccount
	}
	return 1, nil
}

func (f *fakeDB) ranking(context.Context, int64) ([]int64, error) {
	f.calls.Add(1)
	ids := make([]int64, 30)
	for i := range ids {
		ids[i] = int64(i + 2)
	}
	return ids, nil
}

type fakeDir struct{}

func (fakeDir) people(_ context.Context, ids []int64) ([]person, error) {
	out := []person{}
	for _, id := range ids {
		s := strconv.FormatInt(id, 10)
		out = append(out, person{ID: s, Acct: "u" + s + "@a-very-long-instance-name.far.example", Username: "u" + s, Name: "<b>:blob: U" + s + "</b>", URL: "https://x/" + s})
	}
	return out, nil
}

func testServer(t *testing.T) (*httptest.Server, *fakeDB) {
	t.Helper()
	db := &fakeDB{}
	cfg := config{domain: "bolha.us", lang: "pt", cacheTTL: time.Hour}
	b := newBuilder(db, fakeDir{}, cfg)
	b.fetch = func(context.Context, string) *avatar { return nil }
	tmpl := template.Must(template.ParseFS(templateFS, "templates/*.html"))
	ts := httptest.NewServer(newServer(cfg, tmpl, b).routes())
	t.Cleanup(ts.Close)
	return ts, db
}

func get(t *testing.T, url string) (int, string, http.Header) {
	t.Helper()
	c := http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var b bytes.Buffer
	b.ReadFrom(resp.Body)
	return resp.StatusCode, b.String(), resp.Header
}

func TestPages(t *testing.T) {
	ts, db := testServer(t)
	if code, body, _ := get(t, ts.URL+"/"); code != 200 || !strings.Contains(body, "Círculos") {
		t.Fatalf("index: %d", code)
	}
	if code, _, h := get(t, ts.URL+"/?handle=@guto@bolha.us"); code != 303 || h.Get("Location") != "/@guto" {
		t.Fatalf("redirect: %d %s", code, h.Get("Location"))
	}
	if code, body, _ := get(t, ts.URL+"/?handle=a@mastodon.social"); code != 422 || !strings.Contains(body, "Só contas da bolha.us") {
		t.Fatalf("other instance: %d", code)
	}
	code, body, h := get(t, ts.URL+"/@guto")
	if code != 200 || !strings.Contains(body, "u2@a-very-long-instance-name.far.example") {
		t.Fatalf("circle: %d", code)
	}
	if strings.Contains(body, "<b>") {
		t.Fatal("display name not escaped")
	}
	if !strings.Contains(h.Get("Content-Security-Policy"), "default-src 'none'") {
		t.Fatal("missing CSP")
	}
	code, body, h = get(t, ts.URL+"/@guto.png")
	if code != 200 || h.Get("Content-Type") != "image/png" {
		t.Fatalf("png: %d %s", code, h.Get("Content-Type"))
	}
	img, err := png.Decode(strings.NewReader(body))
	if err != nil || img.Bounds().Dx() != canvasSize {
		t.Fatalf("png decode: %v", err)
	}
	if db.calls.Load() != 1 {
		t.Fatalf("expected one build for page + image, got %d", db.calls.Load())
	}
	if code, _, _ := get(t, ts.URL+"/@nobody"); code != 404 {
		t.Fatalf("missing account: %d", code)
	}
	if code, _, _ := get(t, ts.URL+"/guto"); code != 422 {
		t.Fatalf("no @: %d", code)
	}
}
