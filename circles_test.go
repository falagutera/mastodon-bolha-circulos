package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

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

func TestDrawable(t *testing.T) {
	for in, want := range map[string]string{
		"felipe siles 🌵": "felipe siles", "Carlos 🇧🇷 Nogueira": "Carlos Nogueira", "José ❤️": "José", "/mastodon/guto": "/mastodon/guto",
	} {
		if got := drawable(in); got != want {
			t.Errorf("drawable(%q) = %q", in, got)
		}
	}
}

func TestSpots(t *testing.T) {
	people := make([]person, 49)
	for i := range people {
		people[i] = person{Acct: strings.Repeat("a", 30) + "@instance.example", URL: "https://x"}
	}
	all := spots(person{Acct: "guto"}, splitRings(people))
	if len(all) != 50 || all[49].R != selfRadius {
		t.Fatalf("got %d spots", len(all))
	}
	for _, s := range all {
		if s.TipX < 0 || s.TipY < 0 || s.TipX+s.TipW > canvasWidth || s.TipY+s.TipH > canvasHeight {
			t.Errorf("label off the image: %+v", s)
		}
		if s.X-s.R < 0 || s.Y-s.R < 0 || s.Y+s.R > canvasHeight {
			t.Errorf("avatar off the image: %+v", s)
		}
	}
}

func TestLocalAccountsGetTheDomain(t *testing.T) {
	inst := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"id":"1","acct":"guto"},{"id":"2","acct":"a@far.example"}]`)
	}))
	defer inst.Close()
	api := &instanceAPI{base: inst.URL, domain: "bolha.us", client: inst.Client()}
	got, err := api.people(context.Background(), []int64{1, 2})
	if err != nil || len(got) != 2 || got[0].Acct != "guto@bolha.us" || got[1].Acct != "a@far.example" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestFitText(t *testing.T) {
	long := "@" + strings.Repeat("alguem", 20) + "@" + strings.Repeat("instancia", 20) + ".example"
	got := fitText(rowDimFace, long, 120)
	if textWidth(rowDimFace, got) > 120 || !strings.HasSuffix(got, "…") {
		t.Fatalf("got %q", got)
	}
	if got := fitText(rowDimFace, "@guto", 120); got != "@guto" {
		t.Fatalf("short text changed: %q", got)
	}
	if got := fitText(rowDimFace, "ação longa", 1); got != "…" {
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
	if _, err := b.get(context.Background(), "guto", "en"); !errors.Is(err, errNoAccount) {
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

func testServer(t *testing.T, instance string) (*server, *httptest.Server, *fakeDB) {
	t.Helper()
	db := &fakeDB{}
	cfg := config{domain: "bolha.us", lang: "pt", cacheTTL: time.Hour, instance: instance, baseURL: "https://circles.bolha.us",
		clientID: "id", clientSecret: "secret", secret: []byte(strings.Repeat("k", 32))}
	b := newBuilder(db, fakeDir{}, cfg)
	b.fetch = func(context.Context, string) *avatar { return nil }
	tmpl := template.Must(template.ParseFS(templateFS, "templates/*.html"))
	s := newServer(cfg, tmpl, &http.Client{Timeout: time.Second}, b)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	return s, ts, db
}

// get sends the cookies by hand: the app sets __Host- cookies, and the test server is http.
func get(t *testing.T, url string, cookies ...*http.Cookie) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	c := http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var b bytes.Buffer
	b.ReadFrom(resp.Body)
	return resp, b.String()
}

func sessionFor(t *testing.T, s *server, account string, expires time.Time) *http.Cookie {
	t.Helper()
	v, err := s.seal("session", session{Account: account, Expires: expires})
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: sessionCookie, Value: v}
}

func TestAnonymousSeesOnlyTheLogin(t *testing.T) {
	_, ts, db := testServer(t, "")
	resp, body := get(t, ts.URL+"/")
	if resp.StatusCode != 200 || !strings.Contains(body, "/login") {
		t.Fatalf("index: %d", resp.StatusCode)
	}
	if resp, _ := get(t, ts.URL+"/circle.png"); resp.StatusCode != 401 {
		t.Fatalf("anonymous image: %d", resp.StatusCode)
	}
	if resp, _ := get(t, ts.URL+"/@guto"); resp.StatusCode != 404 {
		t.Fatalf("public circle page: %d", resp.StatusCode)
	}
	if db.calls.Load() != 0 {
		t.Fatal("anonymous request reached the database")
	}
}

func TestBadSessionsAreRejected(t *testing.T) {
	s, ts, _ := testServer(t, "")
	expired := sessionFor(t, s, "guto", time.Now().Add(-time.Minute))
	forged := sessionFor(t, s, "guto", time.Now().Add(time.Hour))
	forged.Value += "x"
	state, _ := s.seal("state", session{Account: "guto", Expires: time.Now().Add(time.Hour)})
	for name, c := range map[string]*http.Cookie{"expired": expired, "forged": forged, "other purpose": {Name: sessionCookie, Value: state}} {
		if resp, _ := get(t, ts.URL+"/circle.png", c); resp.StatusCode != 401 {
			t.Errorf("%s session: %d", name, resp.StatusCode)
		}
	}
}

func TestOwnCircle(t *testing.T) {
	s, ts, db := testServer(t, "")
	c := sessionFor(t, s, "guto", time.Now().Add(time.Hour))
	resp, body := get(t, ts.URL+"/", c)
	if resp.StatusCode != 200 || !strings.Contains(body, "u2@a-very-long-instance-name.far.example") {
		t.Fatalf("circle: %d", resp.StatusCode)
	}
	if strings.Contains(body, "<b>") {
		t.Fatal("display name not escaped")
	}
	if !strings.Contains(body, `aria-label="@u2@a-very-long-instance-name.far.example"`) || !strings.Contains(body, `href="https://x/2"`) {
		t.Fatal("missing hover area")
	}
	if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "default-src 'none'") || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("missing security headers")
	}
	dump(t, "circle.html", body)
	resp, body = get(t, ts.URL+"/circle.png", c)
	dump(t, "circle.png", body)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("png: %d", resp.StatusCode)
	}
	if img, err := png.Decode(strings.NewReader(body)); err != nil || img.Bounds().Dx() != canvasWidth {
		t.Fatalf("png decode: %v", err)
	}
	if db.calls.Load() != 1 {
		t.Fatalf("expected one build for page + image, got %d", db.calls.Load())
	}
	if resp, _ := get(t, ts.URL+"/", sessionFor(t, s, "nobody", time.Now().Add(time.Hour))); resp.StatusCode != 404 {
		t.Fatalf("missing account: %d", resp.StatusCode)
	}
}

func TestLoginFlow(t *testing.T) {
	var revoked atomic.Bool
	inst := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			fmt.Fprint(w, `{"access_token":"tok"}`)
		case "/api/v1/accounts/verify_credentials":
			if r.Header.Get("Authorization") != "Bearer tok" {
				w.WriteHeader(401)
				return
			}
			fmt.Fprint(w, `{"username":"guto"}`)
		case "/oauth/revoke":
			revoked.Store(true)
		}
	}))
	defer inst.Close()
	_, ts, _ := testServer(t, inst.URL)
	resp, _ := get(t, ts.URL+"/login")
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if resp.StatusCode != 302 || loc.Query().Get("scope") != "profile" || loc.Query().Get("code_challenge") == "" {
		t.Fatalf("login: %d %s", resp.StatusCode, loc)
	}
	var state *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == stateCookie {
			state = &http.Cookie{Name: c.Name, Value: c.Value}
		}
	}
	if resp, _ := get(t, ts.URL+"/auth/callback?code=c&state=wrong", state); resp.StatusCode != 400 {
		t.Fatalf("wrong state: %d", resp.StatusCode)
	}
	resp, _ = get(t, ts.URL+"/auth/callback?code=c&state="+url.QueryEscape(loc.Query().Get("state")), state)
	if resp.StatusCode != 302 || resp.Header.Get("Location") != "/" {
		t.Fatalf("callback: %d", resp.StatusCode)
	}
	var sess *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookie {
			sess = &http.Cookie{Name: c.Name, Value: c.Value}
		}
	}
	if sess == nil || !revoked.Load() {
		t.Fatalf("session %v revoked %v", sess, revoked.Load())
	}
	if resp, _ := get(t, ts.URL+"/circle.png", sess); resp.StatusCode != 200 {
		t.Fatalf("after login: %d", resp.StatusCode)
	}
}

func TestShareFlow(t *testing.T) {
	var posted, revoked atomic.Bool
	var status url.Values
	inst := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			fmt.Fprint(w, `{"access_token":"tok"}`)
		case "/api/v1/accounts/verify_credentials":
			fmt.Fprint(w, `{"username":"guto"}`)
		case "/api/v1/media":
			if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
				t.Errorf("media: %s", r.Header.Get("Content-Type"))
			}
			fmt.Fprint(w, `{"id":"7"}`)
		case "/api/v1/statuses":
			r.ParseForm()
			status = r.PostForm
			posted.Store(true)
			fmt.Fprintf(w, `{"url":"http://%s/@guto/1"}`, r.Host)
		case "/oauth/revoke":
			revoked.Store(true)
		}
	}))
	defer inst.Close()
	s, ts, _ := testServer(t, inst.URL)
	if resp, _ := get(t, ts.URL+"/share"); resp.StatusCode != 303 {
		t.Fatalf("anonymous share: %d", resp.StatusCode)
	}
	value, _ := s.seal("session", session{Account: "guto", Expires: time.Now().Add(time.Hour)})
	sess := &http.Cookie{Name: sessionCookie, Value: value}
	resp, _ := get(t, ts.URL+"/share", sess)
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if resp.StatusCode != 302 || loc.Query().Get("scope") != shareScope {
		t.Fatalf("share: %d %s", resp.StatusCode, loc)
	}
	if posted.Load() {
		t.Fatal("posted before the approval")
	}
	var state *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == stateCookie {
			state = &http.Cookie{Name: c.Name, Value: c.Value}
		}
	}
	resp, _ = get(t, ts.URL+"/auth/callback?code=c&state="+url.QueryEscape(loc.Query().Get("state")), state, sess)
	if resp.StatusCode != 302 || resp.Header.Get("Location") != inst.URL+"/@guto/1" {
		t.Fatalf("callback: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	if !revoked.Load() {
		t.Fatal("token not revoked")
	}
	if status.Get("media_ids[]") != "7" || status.Get("visibility") != "public" {
		t.Fatalf("status: %v", status)
	}
}

func TestLanguage(t *testing.T) {
	s, ts, db := testServer(t, "")
	ask := func(header string, cookies ...*http.Cookie) string {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/", nil)
		req.Header.Set("Accept-Language", header)
		for _, c := range cookies {
			req.AddCookie(c)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var b bytes.Buffer
		b.ReadFrom(resp.Body)
		return b.String()
	}
	en, pt := `<html lang="en" data-family="bolha">`, `<html lang="pt-BR" data-family="bolha">`
	for header, want := range map[string]string{"en-US,en;q=0.9": en, "pt-BR,pt;q=0.9,en;q=0.8": pt, "fr-FR,en;q=0.5": en, "de": pt, "": pt} {
		if !strings.Contains(ask(header), want) {
			t.Errorf("Accept-Language %q: wanted %s", header, want)
		}
	}
	if !strings.Contains(ask("pt-BR", &http.Cookie{Name: langCookie, Value: "en"}), en) {
		t.Error("the chosen language must win over the browser")
	}
	if !strings.Contains(ask("en", &http.Cookie{Name: langCookie, Value: "xx"}), en) {
		t.Error("an unknown cookie must be ignored")
	}
	resp, _ := get(t, ts.URL+"/?lang=en")
	if resp.StatusCode != 303 || len(resp.Cookies()) != 1 || resp.Cookies()[0].Name != langCookie {
		t.Fatalf("switch: %d %v", resp.StatusCode, resp.Cookies())
	}
	if resp, _ := get(t, ts.URL+"/?lang=xx"); len(resp.Cookies()) != 0 {
		t.Fatal("unknown language was stored")
	}
	// Each language has its own image, the same one for page and download.
	c := sessionFor(t, s, "guto", time.Now().Add(time.Hour))
	_, a := get(t, ts.URL+"/circle.png", c, &http.Cookie{Name: langCookie, Value: "en"})
	_, b := get(t, ts.URL+"/circle.png", c, &http.Cookie{Name: langCookie, Value: "pt"})
	if a == b || db.calls.Load() != 2 {
		t.Fatalf("languages share an image, %d builds", db.calls.Load())
	}
}

func TestLogoutRefusesOtherSites(t *testing.T) {
	_, ts, _ := testServer(t, "")
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/logout", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("cross-site logout: %d", resp.StatusCode)
	}
}

func TestInstanceIsRequired(t *testing.T) {
	t.Setenv("CIRCLES_INSTANCE", "")
	t.Setenv("CIRCLES_BASE_URL", "https://app.example.social")
	if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "CIRCLES_INSTANCE") {
		t.Fatalf("started without an instance: %v", err)
	}
}

func TestMediaClientRefusesPrivateAddresses(t *testing.T) {
	for _, a := range []string{"127.0.0.1", "10.42.0.1", "192.168.99.30", "169.254.169.254", "100.64.0.1", "::1", "fd00::1", "fe80::1", "::ffff:10.0.0.1", "0.0.0.0"} {
		if publicAddress(netip.MustParseAddr(a)) {
			t.Errorf("%s counts as public", a)
		}
	}
	for _, a := range []string{"15.235.51.205", "2606:4700::1"} {
		if !publicAddress(netip.MustParseAddr(a)) {
			t.Errorf("%s counts as private", a)
		}
	}
	ts := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer ts.Close()
	if resp, err := mediaClient(time.Second).Get(ts.URL); err == nil {
		resp.Body.Close()
		t.Fatal("reached a loopback address")
	}
}

type panicDir struct{ fakeDir }

func (panicDir) people(context.Context, []int64) ([]person, error) { panic("bug") }

func TestPanicDoesNotStopTheServer(t *testing.T) {
	b := newBuilder(&fakeDB{}, panicDir{}, config{domain: "bolha.us", cacheTTL: time.Hour})
	if _, err := b.get(context.Background(), "guto", "en"); !errors.Is(err, errBuildFailed) {
		t.Fatalf("err = %v", err)
	}
	if len(b.cache) != 0 {
		t.Fatal("a failed circle stayed in the cache")
	}
	if len(b.slots) != 0 {
		t.Fatal("the build slot was not released")
	}
}

func TestAvatarPanicCostsOneAvatar(t *testing.T) {
	b := newBuilder(&fakeDB{}, fakeDir{}, config{domain: "bolha.us", cacheTTL: time.Hour})
	b.fetch = func(context.Context, string) *avatar { panic("bug") }
	if got := b.loadAvatars(context.Background(), []person{{ID: "1"}}); got["1"] != nil {
		t.Fatalf("got %v", got)
	}
}

func TestConcurrentBuilds(t *testing.T) {
	b := newBuilder(&fakeDB{}, fakeDir{}, config{domain: "bolha.us", cacheTTL: time.Hour})
	b.fetch = func(context.Context, string) *avatar { return nil }
	var wg sync.WaitGroup
	for _, u := range []string{"a", "b", "c", "d"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := b.get(context.Background(), u, "en"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}
