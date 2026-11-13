package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"time"

	"gitlab.gutocarvalho.net/open/bolha/circulos/internal/relnotes"
)

type server struct {
	cfg     config
	tmpl    *template.Template
	client  *http.Client
	circles *builder
}

func newServer(cfg config, tmpl *template.Template, client *http.Client, b *builder) *server {
	return &server{cfg: cfg, tmpl: tmpl, client: client, circles: b}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("GET /robots.txt", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, "User-agent: *\nDisallow: /\n")
	})
	assets, err := fs.Sub(assetFS, "assets")
	if err != nil {
		panic(err)
	}
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServerFS(assets)))
	mux.HandleFunc("GET /login", s.handleLogin)
	mux.HandleFunc("GET /share", s.handleShare)
	mux.HandleFunc("GET /auth/callback", s.handleCallback)
	mux.HandleFunc("POST /logout", s.handleLogout)
	mux.HandleFunc("GET /circle.png", s.handleImage)
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /release-notes", s.handleNotes)
	mux.Handle("GET /release-notes.json", relnotes.JSONHandler("circulos", version, releases))
	return securityHeaders(mux)
}

// securityHeaders allows same-origin scripts for the bar the edge injects on
// bolha sites. The app itself has no JavaScript.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("X-Robots-Tag", "noindex, nofollow")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; connect-src 'self'; style-src 'self'; img-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

type pageData struct {
	Notes    bool          // on the release notes page
	Releases template.HTML // release notes
	Version  string
	T        map[string]string
	Domain   string
	Message  string
	Circle   *circle
}

func (s *server) page(r *http.Request) pageData {
	return pageData{Version: version, T: strs(language(r, s.cfg.lang)), Domain: s.cfg.domain}
}

// handleNotes serves the public release notes page.
func (s *server) handleNotes(w http.ResponseWriter, r *http.Request) {
	d := s.page(r)
	d.Notes, d.Releases = true, relnotes.HTML(releases)
	s.render(w, http.StatusOK, "notes.html", d)
}

func (s *server) render(w http.ResponseWriter, status int, name string, data pageData) {
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		log.Printf("template %s: %v", name, err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	w.Write(buf.Bytes())
}

func (s *server) renderError(w http.ResponseWriter, r *http.Request, status int, key string) {
	d := s.page(r)
	d.Message = d.T["error_"+key]
	s.render(w, status, "index.html", d)
}

func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if lang := r.URL.Query().Get("lang"); lang != "" {
		if _, ok := translations[lang]; ok {
			s.setCookie(w, langCookie, lang, 365*24*time.Hour)
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	sess, ok := s.currentSession(r)
	if !ok {
		s.render(w, http.StatusOK, "index.html", s.page(r))
		return
	}
	c, err := s.circles.get(r.Context(), sess.Account, language(r, s.cfg.lang))
	if err != nil {
		s.circleError(w, r, err)
		return
	}
	d := s.page(r)
	d.Circle = c
	s.render(w, http.StatusOK, "circle.html", d)
}

func (s *server) handleImage(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.currentSession(r)
	if !ok {
		http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		return
	}
	c, err := s.circles.get(r.Context(), sess.Account, language(r, s.cfg.lang))
	if err != nil {
		s.circleError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Length", strconv.Itoa(len(c.PNG)))
	w.Write(c.PNG)
}

func (s *server) circleError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, context.Canceled):
	case errors.Is(err, errNoAccount):
		s.renderError(w, r, http.StatusNotFound, "account")
	default:
		log.Printf("circle: %v", err)
		s.renderError(w, r, http.StatusBadGateway, "unavailable")
	}
}
