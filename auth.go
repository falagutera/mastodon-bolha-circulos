package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	sessionCookie = "__Host-circles"
	stateCookie   = "__Host-circles-state"
	stateTTL      = 10 * time.Minute
	sessionTTL    = 4 * time.Hour
	oauthScope    = "profile"
	shareScope    = "profile write:media write:statuses"
)

type session struct {
	Account string    `json:"a"`
	Expires time.Time `json:"e"`
}

type loginState struct {
	State    string    `json:"s"`
	Verifier string    `json:"v"`
	Share    bool      `json:"h,omitempty"`
	Expires  time.Time `json:"e"`
}

// seal and open implement a signed cookie value: base64(payload).base64(hmac).
// The purpose is mixed into the HMAC so one cookie can't stand in for the other.
func (s *server) seal(purpose string, v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(raw)
	return payload + "." + s.sign(purpose, payload), nil
}

func (s *server) open(purpose, value string, v any) error {
	payload, sig, ok := strings.Cut(value, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(s.sign(purpose, payload))) {
		return errors.New("bad signature")
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

func (s *server) sign(purpose, payload string) string {
	m := hmac.New(sha256.New, s.cfg.secret)
	m.Write([]byte(purpose + "\x00" + payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (s *server) setCookie(w http.ResponseWriter, name, value string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		Secure:   true,
		// Lax: the cookie must survive the top-level redirect back from the instance.
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *server) clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
}

// currentSession returns the valid session of the request, if any.
func (s *server) currentSession(r *http.Request) (session, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return session{}, false
	}
	var sess session
	if err := s.open("session", c.Value, &sess); err != nil || time.Now().After(sess.Expires) {
		return session{}, false
	}
	return sess, true
}

func randomToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (s *server) redirectURI() string { return s.cfg.baseURL + "/auth/callback" }

func (s *server) handleLogin(w http.ResponseWriter, r *http.Request) {
	s.authorize(w, r, false)
}

// handleShare asks the instance for the right to post. It publishes nothing by
// itself: that only happens after the person approves it on the instance.
func (s *server) handleShare(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.currentSession(r); !ok {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.authorize(w, r, true)
}

func (s *server) authorize(w http.ResponseWriter, r *http.Request, share bool) {
	st := loginState{State: randomToken(), Verifier: randomToken(), Share: share, Expires: time.Now().Add(stateTTL)}
	value, err := s.seal("state", st)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.setCookie(w, stateCookie, value, stateTTL)
	challenge := sha256.Sum256([]byte(st.Verifier))
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {s.cfg.clientID},
		"redirect_uri":          {s.redirectURI()},
		"scope":                 {st.scope()},
		"state":                 {st.State},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method": {"S256"},
	}
	http.Redirect(w, r, s.cfg.instance+"/oauth/authorize?"+q.Encode(), http.StatusFound)
}

func (st loginState) scope() string {
	if st.Share {
		return shareScope
	}
	return oauthScope
}

func (s *server) handleCallback(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(stateCookie)
	if err != nil {
		s.renderError(w, r, http.StatusBadRequest, "expired")
		return
	}
	s.clearCookie(w, stateCookie)
	var st loginState
	if err := s.open("state", c.Value, &st); err != nil || time.Now().After(st.Expires) {
		s.renderError(w, r, http.StatusBadRequest, "expired")
		return
	}
	q := r.URL.Query()
	if !hmac.Equal([]byte(q.Get("state")), []byte(st.State)) {
		s.renderError(w, r, http.StatusBadRequest, "expired")
		return
	}
	if q.Get("code") == "" && st.Share {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	if q.Get("code") == "" {
		// The person declined on the instance's consent screen.
		s.renderError(w, r, http.StatusForbidden, "denied")
		return
	}
	token, err := s.exchangeCode(q.Get("code"), st.Verifier, st.scope())
	if err != nil {
		log.Printf("token exchange: %v", err)
		s.renderError(w, r, http.StatusBadGateway, "instance")
		return
	}
	// The token is only needed for this request, so it never outlives it.
	defer s.revokeToken(token)
	acct, err := s.fetchAccount(token)
	if err != nil {
		log.Printf("verify_credentials: %v", err)
		s.renderError(w, r, http.StatusBadGateway, "instance")
		return
	}
	if st.Share {
		s.share(w, r, token, acct, st.State)
		return
	}
	value, err := s.seal("session", session{Account: acct, Expires: time.Now().Add(sessionTTL)})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.setCookie(w, sessionCookie, value, sessionTTL)
	http.Redirect(w, r, "/", http.StatusFound)
}

func (s *server) handleLogout(w http.ResponseWriter, r *http.Request) {
	// Not Origin: Referrer-Policy no-referrer makes browsers send "Origin: null".
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	s.clearCookie(w, sessionCookie)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *server) exchangeCode(code, verifier, scope string) (string, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {s.cfg.clientID},
		"client_secret": {s.cfg.clientSecret},
		"redirect_uri":  {s.redirectURI()},
		"code_verifier": {verifier},
		"scope":         {scope},
	}
	resp, err := s.client.PostForm(s.cfg.instance+"/oauth/token", form)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out); err != nil {
		return "", err
	}
	if out.AccessToken == "" {
		return "", errors.New("empty access token")
	}
	return out.AccessToken, nil
}

func (s *server) fetchAccount(token string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, s.cfg.instance+"/api/v1/accounts/verify_credentials", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	var out struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return "", err
	}
	if out.Username == "" {
		return "", errors.New("empty account")
	}
	return out.Username, nil
}

// share publishes the circle of the account that approved it and opens the post.
func (s *server) share(w http.ResponseWriter, r *http.Request, token, acct, key string) {
	lang := language(r, s.cfg.lang)
	c, err := s.circles.get(r.Context(), acct, lang)
	if err != nil {
		s.circleError(w, r, err)
		return
	}
	t := strs(lang)
	id, err := s.uploadMedia(token, c.PNG, fmt.Sprintf(t["result"], c.Self.DisplayName))
	if err == nil {
		var post string
		if post, err = s.publish(token, id, fmt.Sprintf(t["share_text"], s.cfg.baseURL), key); err == nil {
			http.Redirect(w, r, post, http.StatusFound)
			return
		}
	}
	log.Printf("share: %v", err)
	s.renderError(w, r, http.StatusBadGateway, "instance")
}

func (s *server) uploadMedia(token string, png []byte, alt string) (string, error) {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", "circle.png")
	if err != nil {
		return "", err
	}
	part.Write(png)
	form.WriteField("description", alt)
	form.Close()
	var out struct {
		ID string `json:"id"`
	}
	if err := s.post(token, "/api/v1/media", form.FormDataContentType(), "", &body, &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", errors.New("empty media id")
	}
	return out.ID, nil
}

// publish uses the state as the idempotency key: a reloaded callback can't post twice.
func (s *server) publish(token, media, text, key string) (string, error) {
	form := url.Values{"status": {text}, "media_ids[]": {media}, "visibility": {"public"}}
	var out struct {
		URL string `json:"url"`
	}
	if err := s.post(token, "/api/v1/statuses", "application/x-www-form-urlencoded", key, strings.NewReader(form.Encode()), &out); err != nil {
		return "", err
	}
	if !strings.HasPrefix(out.URL, s.cfg.instance+"/") {
		return s.cfg.instance, nil
	}
	return out.URL, nil
}

func (s *server) post(token, path, kind, key string, body io.Reader, out any) error {
	req, err := http.NewRequest(http.MethodPost, s.cfg.instance+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", kind)
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: status %d", path, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

func (s *server) revokeToken(token string) {
	resp, err := s.client.PostForm(s.cfg.instance+"/oauth/revoke", url.Values{
		"client_id":     {s.cfg.clientID},
		"client_secret": {s.cfg.clientSecret},
		"token":         {token},
	})
	if err != nil {
		log.Printf("revoke: %v", err)
		return
	}
	resp.Body.Close()
}
