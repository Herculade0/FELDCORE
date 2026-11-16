// Package apps serves the App Store: a cached copy of the Unraid Community
// Applications catalogue, and the translation of a chosen template into the
// container spec the existing setup page edits. Nothing is installed from here.
package apps

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Herculade0/feldcore/internal/docker"
)

const (
	feedFileName = "ca-feed.json"
	feedTimeName = "ca-feed.time"
	maxFeedBytes = 80 << 20
	staleAfter   = 24 * time.Hour
)

// InputError is a validation failure whose message is safe to show the user.
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

// OpError is an operational failure whose message is safe to show the user.
type OpError struct{ Msg string }

func (e *OpError) Error() string { return e.Msg }

// ErrNotFound is returned when the requested app is not in the cached catalogue.
var ErrNotFound = errors.New("app not found")

// Catalogue is what the App Store page renders. Apps is empty until a
// catalogue has been saved, or when the feed is offline and nothing was saved.
type Catalogue struct {
	UpdatedAt      string     `json:"updatedAt,omitempty"`
	Stale          bool       `json:"stale"`
	Offline        bool       `json:"offline"`
	Missing        bool       `json:"missing"`
	Message        string     `json:"message,omitempty"`
	Appdata        string     `json:"appdata"`
	AppdataDefault string     `json:"appdataDefault"`
	AppdataCustom  bool       `json:"appdataCustom"`
	Categories     []Category `json:"categories"`
	Apps           []App      `json:"apps"`
}

// Settings is the app-data folder Unraid paths are rewritten into.
type Settings struct {
	Appdata        string `json:"appdata"`
	AppdataDefault string `json:"appdataDefault"`
	AppdataCustom  bool   `json:"appdataCustom"`
}

// Service caches the Community Applications feed on disk and in memory.
type Service struct {
	dir        string
	sharesRoot string
	feedURL    string
	client     *http.Client
	log        *slog.Logger
	now        func() time.Time

	appdata    func() string
	setAppdata func(string) error
	sharePaths func() map[string]string

	mu        sync.Mutex
	refreshMu sync.Mutex
	loaded    bool
	parsed    *parsedFeed
	fetchedAt time.Time
}

// Deps are the pieces of FELDCORE the catalogue needs.
type Deps struct {
	DataDir    string
	SharesRoot string
	FeedURL    string
	Client     *http.Client
	Log        *slog.Logger
	// Appdata returns the saved folder, or "" when the default should be used.
	Appdata    func() string
	SetAppdata func(string) error
	// SharePaths returns lower-case share name → absolute folder.
	SharePaths func() map[string]string
}

// New returns a catalogue service. It does not download anything yet.
func New(d Deps) *Service {
	if d.FeedURL == "" {
		d.FeedURL = FeedURL
	}
	if d.Client == nil {
		d.Client = &http.Client{Timeout: 90 * time.Second}
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.Appdata == nil {
		d.Appdata = func() string { return "" }
	}
	if d.SetAppdata == nil {
		d.SetAppdata = func(string) error { return nil }
	}
	if d.SharePaths == nil {
		d.SharePaths = func() map[string]string { return nil }
	}
	return &Service{
		dir: d.DataDir, sharesRoot: d.SharesRoot, feedURL: d.FeedURL,
		client: d.Client, log: d.Log, now: time.Now,
		appdata: d.Appdata, setAppdata: d.SetAppdata, sharePaths: d.SharePaths,
	}
}

// Catalogue returns the saved catalogue. It never contacts the network; use
// Refresh for that, so opening the page does not depend on the feed being up.
func (s *Service) Catalogue() Catalogue {
	s.ensureLoaded()
	return s.view(false, "")
}

// Refresh downloads a new catalogue. If the feed is offline, the previous
// copy is kept and the result says so.
func (s *Service) Refresh(ctx context.Context) Catalogue {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()

	body, err := s.download(ctx)
	if err != nil {
		s.log.Warn("community applications feed unavailable", "err", err)
		s.ensureLoaded()
		return s.view(true, offlineMessage(s.hasApps()))
	}
	parsed, err := parseFeed(body)
	if err != nil {
		s.log.Warn("community applications feed rejected", "err", err)
		s.ensureLoaded()
		return s.view(true, "The catalogue download could not be read. "+offlineTail(s.hasApps()))
	}
	fetched := s.now()
	if err := s.save(body, fetched); err != nil {
		s.log.Error("could not save community applications catalogue", "err", err)
		s.mu.Lock()
		s.parsed = parsed
		s.fetchedAt = fetched
		s.loaded = true
		s.mu.Unlock()
		cat := s.view(false, "")
		cat.Message = "The catalogue was downloaded but could not be saved. It will be fetched again next time."
		return cat
	}
	s.mu.Lock()
	s.parsed = parsed
	s.fetchedAt = fetched
	s.loaded = true
	s.mu.Unlock()
	s.log.Info("community applications catalogue updated", "apps", len(parsed.apps))
	return s.view(false, "")
}

// Spec returns the container setup for one app, with Unraid paths rewritten
// for this server. The caller shows it; nothing is installed.
func (s *Service) Spec(id string) (App, docker.Spec, []string, error) {
	s.ensureLoaded()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.parsed == nil {
		return App{}, docker.Spec{}, nil, ErrNotFound
	}
	i, ok := s.parsed.byID[id]
	if !ok {
		return App{}, docker.Spec{}, nil, ErrNotFound
	}
	rec := s.parsed.apps[i]
	spec, notes := docker.TemplateToSpec(rec.tmpl, docker.PathMap{
		Appdata:    s.effectiveAppdata(),
		SharesRoot: s.sharesRoot,
		Shares:     s.sharePaths(),
	})
	return rec.app, spec, notes, nil
}

// Settings returns the appdata share templates are rewritten into.
func (s *Service) Settings() Settings {
	path := s.defaultAppdata()
	return Settings{Appdata: path, AppdataDefault: path}
}

// FeedURL is the catalogue address currently in use.
func (s *Service) FeedURL() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.feedURL == "" {
		return FeedURL
	}
	return s.feedURL
}

// SetFeedURL points the next refresh at raw. An empty raw restores the public feed.
func (s *Service) SetFeedURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = FeedURL
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return &InputError{Msg: "The catalogue address must be an http or https URL."}
	}
	s.mu.Lock()
	s.feedURL = raw
	s.mu.Unlock()
	return nil
}

// defaultAppdata is the appdata share when that share exists, so Unraid
// template paths under /mnt/user/appdata always land in that share.
func (s *Service) defaultAppdata() string {
	if p := s.sharePaths()["appdata"]; p != "" {
		return p
	}
	if s.sharesRoot == "" {
		return "/srv/feldcore/appdata"
	}
	return filepath.Join(s.sharesRoot, "appdata")
}

func (s *Service) effectiveAppdata() string {
	// Templates always land in the appdata share. A custom folder is not offered.
	return s.defaultAppdata()
}

func (s *Service) hasApps() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.parsed != nil && len(s.parsed.apps) > 0
}

func offlineMessage(have bool) string {
	if have {
		return "The Community Applications feed is offline. Showing the saved catalogue."
	}
	return "The Community Applications feed is offline, and there is no saved catalogue yet."
}

func offlineTail(have bool) string {
	if have {
		return "Showing the saved catalogue."
	}
	return "There is no saved catalogue yet."
}

func (s *Service) view(offline bool, message string) Catalogue {
	st := s.Settings()
	cat := Catalogue{
		Offline:        offline,
		Message:        message,
		Appdata:        st.Appdata,
		AppdataDefault: st.AppdataDefault,
		AppdataCustom:  st.AppdataCustom,
		Categories:     []Category{},
		Apps:           []App{},
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.parsed == nil || len(s.parsed.apps) == 0 {
		cat.Missing = true
		if cat.Message == "" && !offline {
			cat.Message = "The catalogue hasn't been downloaded yet."
		}
		return cat
	}
	if !s.fetchedAt.IsZero() {
		cat.UpdatedAt = s.fetchedAt.UTC().Format(time.RFC3339)
		cat.Stale = s.now().Sub(s.fetchedAt) > staleAfter
	}
	cat.Categories = s.parsed.categories
	cat.Apps = make([]App, len(s.parsed.apps))
	for i := range s.parsed.apps {
		cat.Apps[i] = s.parsed.apps[i].app
	}
	return cat
}

func (s *Service) ensureLoaded() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loaded {
		return
	}
	s.loaded = true
	body, err := os.ReadFile(s.feedPath())
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			s.log.Warn("could not read saved catalogue", "err", err)
		}
		return
	}
	parsed, err := parseFeed(body)
	if err != nil {
		s.log.Warn("saved catalogue could not be read", "err", err)
		return
	}
	s.parsed = parsed
	s.fetchedAt = s.readFetchedAt()
}

func (s *Service) download(ctx context.Context) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.FeedURL(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "FELDCORE")
	req.Header.Set("Accept", "application/json")
	res, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("feed returned %s", res.Status)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, maxFeedBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxFeedBytes {
		return nil, fmt.Errorf("feed is larger than %dMB", maxFeedBytes>>20)
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("feed was empty")
	}
	return body, nil
}

func (s *Service) save(body []byte, fetched time.Time) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	if err := writeAtomic(s.feedPath(), body, 0o600); err != nil {
		return err
	}
	stamp := []byte(fetched.UTC().Format(time.RFC3339Nano))
	return writeAtomic(filepath.Join(s.dir, feedTimeName), stamp, 0o600)
}

func (s *Service) readFetchedAt() time.Time {
	raw, err := os.ReadFile(filepath.Join(s.dir, feedTimeName))
	if err == nil {
		if t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(raw))); err == nil {
			return t
		}
	}
	info, err := os.Stat(s.feedPath())
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

func (s *Service) feedPath() string { return filepath.Join(s.dir, feedFileName) }

func writeAtomic(path string, body []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".feed-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
