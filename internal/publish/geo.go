package publish

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/oschwald/geoip2-golang"

	"github.com/Herculade0/feldcore/internal/secrets"
)

// secretGeo is the MaxMind licence key. It lives in the secrets box, never
// in the state file and never in a log line.
const secretGeo = "geoip-license"

const geoURLHost = "download.maxmind.com"

// geoDB is a local GeoLite2 country database. It stays closed until the
// user turns country lists on and a file has been downloaded.
type geoDB struct {
	mu   sync.RWMutex
	db   *geoip2.Reader
	path string
}

func (g *geoDB) Ready() bool {
	if g == nil {
		return false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.db != nil
}

func (g *geoDB) Lookup(ip net.IP) (string, bool) {
	if g == nil || ip == nil {
		return "", false
	}
	g.mu.RLock()
	db := g.db
	g.mu.RUnlock()
	if db == nil {
		return "", false
	}
	rec, err := db.Country(ip)
	if err != nil || rec == nil {
		return "", false
	}
	code := strings.ToUpper(strings.TrimSpace(rec.Country.IsoCode))
	if code == "" {
		return "", false
	}
	return code, true
}

func (g *geoDB) open(path string) error {
	r, err := geoip2.Open(path)
	if err != nil {
		return err
	}
	g.mu.Lock()
	old := g.db
	g.db = r
	g.path = path
	g.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	return nil
}

func (s *Service) geoPath() string {
	return filepath.Join(s.DataDir, "geoip", "GeoLite2-Country.mmdb")
}

func (s *Service) countryDB() *geoDB {
	s.geoMu.Lock()
	defer s.geoMu.Unlock()
	if s.countries == nil {
		s.countries = &geoDB{}
	}
	return s.countries
}

func (s *Service) geoReady() bool {
	if s.geoOverride != nil {
		return s.geoOverride.Ready()
	}
	return s.countryDB().Ready()
}

func (s *Service) lookupCountry(ip net.IP) (string, bool) {
	if s.geoOverride != nil {
		return s.geoOverride.Lookup(ip)
	}
	return s.countryDB().Lookup(ip)
}

func (s *Service) setGeoErr(msg string) {
	s.geoMu.Lock()
	s.geoErr = msg
	s.geoMu.Unlock()
}

func (s *Service) getGeoErr() string {
	s.geoMu.Lock()
	defer s.geoMu.Unlock()
	return s.geoErr
}

// OpenGeo loads a database that was already downloaded. It does not contact
// MaxMind. A missing file is not an error: country lists stay inactive
// until the user saves protection settings with a licence key.
func (s *Service) OpenGeo() {
	if s == nil || s.Store == nil || !s.Store.Snapshot().Publish.Protect.GeoEnabled {
		return
	}
	if err := s.countryDB().open(s.geoPath()); err != nil {
		s.setGeoErr("The country database is not ready. Save protection settings to download it.")
		if s.Log != nil {
			s.Log.Info("country database is not loaded")
		}
	}
}

// refreshGeo downloads GeoLite2 when the user has turned it on. The licence
// key is read from the secrets box and is not written to the error text.
func (s *Service) refreshGeo(ctx context.Context) error {
	if s.Secrets == nil {
		return op("The secrets store is not available.")
	}
	key, ok, err := s.Secrets.Get(secretGeo)
	if err != nil || !ok || strings.TrimSpace(key) == "" {
		return input("A MaxMind licence key is required before the country database can be downloaded.")
	}
	if err := downloadGeoLite(ctx, strings.TrimSpace(key), s.geoPath()); err != nil {
		s.setGeoErr(err.Error())
		return err
	}
	if err := s.countryDB().open(s.geoPath()); err != nil {
		s.setGeoErr("The country database was downloaded but could not be read.")
		return op("The country database was downloaded but could not be read.")
	}
	s.setGeoErr("")
	return nil
}

func downloadGeoLite(ctx context.Context, key, dest string) error {
	if strings.ContainsAny(key, " \r\n") {
		return input("The licence key should not contain spaces.")
	}
	u := &url.URL{Scheme: "https", Host: geoURLHost, Path: "/app/geoip_download"}
	q := url.Values{}
	q.Set("edition_id", "GeoLite2-Country")
	q.Set("license_key", key)
	q.Set("suffix", "tar.gz")
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return op("Could not start the country database download.")
	}
	req.Header.Set("User-Agent", "feldcore")
	client := &http.Client{
		Timeout: 90 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Host != geoURLHost || req.URL.Scheme != "https" {
				return fmt.Errorf("refusing a redirect away from MaxMind")
			}
			if len(via) >= 3 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return op("Could not download the country database. " + redactKey(err.Error(), key))
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return input("MaxMind refused the licence key. Check it and try again.")
	}
	if resp.StatusCode != http.StatusOK {
		return op("Could not download the country database (" + resp.Status + ").")
	}
	body := io.LimitReader(resp.Body, 80<<20)
	raw, err := extractMMDB(body)
	if err != nil {
		return op("The country database download was not a GeoLite2 archive.")
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return op("Could not create the country database folder.")
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".geo-*")
	if err != nil {
		return op("Could not write the country database.")
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return op("Could not write the country database.")
	}
	if err := tmp.Close(); err != nil {
		return op("Could not write the country database.")
	}
	if err := os.Rename(tmpName, dest); err != nil {
		return op("Could not write the country database.")
	}
	return nil
}

func redactKey(msg, key string) string {
	if key != "" {
		msg = strings.ReplaceAll(msg, key, "[redacted]")
		if enc := url.QueryEscape(key); enc != key {
			msg = strings.ReplaceAll(msg, enc, "[redacted]")
		}
	}
	return secrets.Redact(msg, key)
}

// extractMMDB returns the first .mmdb member of a gzipped tar. The archive
// path is never used as a destination, so a hostile name cannot escape.
func extractMMDB(r io.Reader) ([]byte, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if hdr.FileInfo().IsDir() || !strings.HasSuffix(strings.ToLower(hdr.Name), ".mmdb") {
			continue
		}
		if hdr.Size < 0 || hdr.Size > 80<<20 {
			return nil, fmt.Errorf("database is too large")
		}
		return io.ReadAll(io.LimitReader(tr, 80<<20))
	}
	return nil, fmt.Errorf("archive did not contain a country database")
}
