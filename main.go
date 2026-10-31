// circles draws the people closest to an account of a Mastodon instance, from
// the instance's own database. See README.md.
package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed assets
var assetFS embed.FS

type config struct {
	listen       string
	baseURL      string // https://circles.bolha.us
	instance     string // https://bolha.us
	domain       string // bolha.us
	clientID     string
	clientSecret string
	secret       []byte
	lang         string
	cacheTTL     time.Duration
	dbHost       string
	dbPort       string
	dbUser       string
	dbPassword   string
	dbName       string
	dbSSLMode    string
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func loadConfig() (config, error) {
	c := config{
		listen:       env("CIRCLES_LISTEN", ":8080"),
		baseURL:      strings.TrimRight(os.Getenv("CIRCLES_BASE_URL"), "/"),
		clientID:     os.Getenv("CIRCLES_CLIENT_ID"),
		clientSecret: os.Getenv("CIRCLES_CLIENT_SECRET"),
		secret:       []byte(os.Getenv("CIRCLES_SECRET")),
		instance:     strings.TrimRight(env("CIRCLES_INSTANCE", "https://bolha.us"), "/"),
		lang:         env("CIRCLES_LANG", "en"),
		cacheTTL:     time.Hour,
		dbHost:       os.Getenv("CIRCLES_DB_HOST"),
		dbPort:       env("CIRCLES_DB_PORT", "5432"),
		dbUser:       os.Getenv("CIRCLES_DB_USER"),
		dbPassword:   os.Getenv("CIRCLES_DB_PASSWORD"),
		dbName:       env("CIRCLES_DB_NAME", "mastodon_production"),
		dbSSLMode:    env("CIRCLES_DB_SSLMODE", "prefer"),
	}
	if v := os.Getenv("CIRCLES_CACHE_TTL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return c, fmt.Errorf("CIRCLES_CACHE_TTL: %w", err)
		}
		c.cacheTTL = d
	}
	u, err := url.Parse(c.instance)
	if err != nil || u.Host == "" {
		return c, fmt.Errorf("CIRCLES_INSTANCE must be a URL like https://example.social")
	}
	c.domain = u.Host
	if _, err := strconv.Atoi(c.dbPort); err != nil {
		return c, fmt.Errorf("CIRCLES_DB_PORT: %w", err)
	}
	switch {
	case c.baseURL == "":
		return c, fmt.Errorf("CIRCLES_BASE_URL is required")
	case c.clientID == "" || c.clientSecret == "":
		return c, fmt.Errorf("CIRCLES_CLIENT_ID and CIRCLES_CLIENT_SECRET are required")
	case len(c.secret) < 32:
		return c, fmt.Errorf("CIRCLES_SECRET must have at least 32 characters")
	}
	if c.dbHost == "" || c.dbUser == "" || c.dbPassword == "" {
		return c, fmt.Errorf("CIRCLES_DB_HOST, CIRCLES_DB_USER and CIRCLES_DB_PASSWORD are required")
	}
	return c, nil
}

func main() {
	healthcheck := flag.Bool("healthcheck", false, "probe the local server and exit")
	flag.Parse()
	if *healthcheck {
		resp, err := http.Get("http://127.0.0.1" + env("CIRCLES_LISTEN", ":8080") + "/healthz")
		if err != nil || resp.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		return
	}
	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}
	db, err := openStore(context.Background(), cfg)
	if err != nil {
		log.Fatal(err)
	}
	tmpl, err := template.ParseFS(templateFS, "templates/*.html")
	if err != nil {
		log.Fatal(err)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	api := &instanceAPI{base: cfg.instance, client: client}
	s := newServer(cfg, tmpl, client, newBuilder(db, api, cfg))
	log.Printf("listening on %s for %s", cfg.listen, cfg.domain)
	srv := &http.Server{
		Addr:              cfg.listen,
		Handler:           s.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Fatal(srv.ListenAndServe())
}
