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
	"net/url"
	"strconv"
	"strings"
)

type server struct {
	cfg     config
	tmpl    *template.Template
	circles *builder
}

func newServer(cfg config, tmpl *template.Template, b *builder) *server {
	return &server{cfg: cfg, tmpl: tmpl, circles: b}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("GET /robots.txt", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, "User-agent: *\nDisallow: /@\n")
	})
	assets, err := fs.Sub(assetFS, "assets")
	if err != nil {
		panic(err)
	}
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServerFS(assets)))
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /{path}", s.handleCircle)
	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Cache-Control", "public, max-age=300")
		h.Set("X-Robots-Tag", "noindex, nofollow")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; font-src 'self'; img-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

type pageData struct {
	T       map[string]string
	Domain  string
	Handle  string
	Message string
	Circle  *circle
	Image   string
}

func (s *server) page() pageData {
	return pageData{T: strs(s.cfg.lang), Domain: s.cfg.domain}
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

func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	d := s.page()
	in := r.URL.Query().Get("handle")
	if in == "" {
		s.render(w, http.StatusOK, "index.html", d)
		return
	}
	user, err := parseHandle(in, s.cfg.domain)
	if err != nil {
		d.Handle = in
		d.Message = s.handleError(err, d.T)
		s.render(w, http.StatusUnprocessableEntity, "index.html", d)
		return
	}
	http.Redirect(w, r, "/@"+url.PathEscape(user), http.StatusSeeOther)
}

func (s *server) handleCircle(w http.ResponseWriter, r *http.Request) {
	path := r.PathValue("path")
	wantsPNG := strings.HasSuffix(path, ".png")
	name, ok := strings.CutPrefix(strings.TrimSuffix(path, ".png"), "@")
	if !ok {
		s.fail(w, r, errBadHandle, wantsPNG)
		return
	}
	user, err := parseHandle(name, s.cfg.domain)
	if err != nil {
		s.fail(w, r, err, wantsPNG)
		return
	}
	c, err := s.circles.get(r.Context(), user)
	if err != nil {
		s.fail(w, r, err, wantsPNG)
		return
	}
	if wantsPNG {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Length", strconv.Itoa(len(c.PNG)))
		w.Write(c.PNG)
		return
	}
	d := s.page()
	d.Circle = c
	d.Image = "/@" + url.PathEscape(user) + ".png"
	s.render(w, http.StatusOK, "circle.html", d)
}

func (s *server) handleError(err error, t map[string]string) string {
	switch {
	case errors.Is(err, errOtherInstance):
		return fmt.Sprintf(t["error_instance"], s.cfg.domain)
	case errors.Is(err, errNoAccount):
		return fmt.Sprintf(t["error_notfound"], s.cfg.domain)
	case errors.Is(err, errBadHandle):
		return t["error_handle"]
	}
	return t["error_unavailable"]
}

func (s *server) fail(w http.ResponseWriter, r *http.Request, err error, wantsPNG bool) {
	status := http.StatusBadGateway
	switch {
	case errors.Is(err, errNoAccount):
		status = http.StatusNotFound
	case errors.Is(err, errOtherInstance), errors.Is(err, errBadHandle):
		status = http.StatusUnprocessableEntity
	case errors.Is(err, context.Canceled):
		return
	default:
		log.Printf("circle %s: %v", r.URL.Path, err)
	}
	w.Header().Set("Cache-Control", "no-store")
	if wantsPNG {
		http.Error(w, http.StatusText(status), status)
		return
	}
	d := s.page()
	d.Message = s.handleError(err, d.T)
	s.render(w, status, "index.html", d)
}
