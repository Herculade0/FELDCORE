package vm

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const virtioBase = "https://fedorapeople.org/groups/virt/virtio-win/direct-downloads/"

// Job is a VirtIO download the page can poll.
type Job struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Channel  string `json:"channel,omitempty"`
	Label    string `json:"label"`
	Done     int64  `json:"done"`
	Total    int64  `json:"total"`
	Percent  int    `json:"percent"`
	Finished bool   `json:"finished"`
	Error    string `json:"error,omitempty"`
	Filename string `json:"filename,omitempty"`
}

// VirtioRelease is one channel on Fedora's virtio-win direct-downloads page.
type VirtioRelease struct {
	Channel  string `json:"channel"`
	Version  string `json:"version,omitempty"`
	Filename string `json:"filename,omitempty"`
	URL      string `json:"url,omitempty"`
	Present  bool   `json:"present"`
	Error    string `json:"error,omitempty"`
}

// VirtioFile is a driver ISO already in the isos share.
type VirtioFile struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256,omitempty"`
}

// VirtioView is the VM Manager's driver-ISO card.
type VirtioView struct {
	Releases  []VirtioRelease `json:"releases"`
	Installed []VirtioFile    `json:"installed"`
}

type relEntry struct {
	at  time.Time
	rel VirtioRelease
	err error
}

var (
	virtioDirVer  = regexp.MustCompile(`^virtio-win-(\d+\.\d+\.\d+(?:-\d+)?)$`)
	virtioFileVer = regexp.MustCompile(`(?i)^virtio-win-(\d+\.\d+\.\d+(?:-\d+)?)\.iso$`)
)

func (m *Manager) Virtio(ctx context.Context) VirtioView {
	view := VirtioView{Releases: []VirtioRelease{}, Installed: m.installedVirtio()}
	have := map[string]bool{}
	for _, f := range view.Installed {
		have[strings.ToLower(f.Name)] = true
	}
	for _, ch := range []string{"stable", "latest"} {
		rel, err := m.cachedVirtio(ctx, ch)
		if err != nil {
			view.Releases = append(view.Releases, VirtioRelease{Channel: ch, Error: err.Error()})
			continue
		}
		rel.Present = have[strings.ToLower(rel.Filename)]
		view.Releases = append(view.Releases, rel)
	}
	return view
}

func (m *Manager) cachedVirtio(ctx context.Context, channel string) (VirtioRelease, error) {
	m.relMu.Lock()
	if e, ok := m.relCache[channel]; ok {
		ttl := 15 * time.Minute
		if e.err != nil {
			ttl = 20 * time.Second
		}
		if time.Since(e.at) < ttl {
			m.relMu.Unlock()
			return e.rel, e.err
		}
	}
	m.relMu.Unlock()
	rel, err := resolveVirtio(ctx, channel)
	m.relMu.Lock()
	m.relCache[channel] = relEntry{at: time.Now(), rel: rel, err: err}
	m.relMu.Unlock()
	return rel, err
}

// StartVirtioDownload fetches the stable or latest Fedora virtio-win ISO into
// the isos share. The request returns immediately; poll Job for progress.
func (m *Manager) StartVirtioDownload(channel string) (Job, error) {
	channel = strings.ToLower(strings.TrimSpace(channel))
	if channel != "stable" && channel != "latest" {
		return Job{}, invalid("Choose the stable or the latest VirtIO driver ISO.")
	}
	m.jobMu.Lock()
	for _, j := range m.jobs {
		if j.Kind == "virtio" && j.Channel == channel && !j.Finished {
			cp := *j
			m.jobMu.Unlock()
			return cp, nil
		}
	}
	id := newID()
	j := &Job{ID: id, Kind: "virtio", Channel: channel, Label: "VirtIO " + channel}
	m.jobs[id] = j
	m.jobMu.Unlock()
	go m.runVirtio(id, channel)
	return *j, nil
}

// Job returns a download's progress.
func (m *Manager) Job(id string) (Job, error) {
	m.jobMu.Lock()
	defer m.jobMu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return Job{}, invalid("That download is no longer listed.")
	}
	return *j, nil
}

func (m *Manager) runVirtio(id, channel string) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()
	fail := func(err error) {
		m.finishJob(id, "", err)
	}
	rel, err := resolveVirtio(ctx, channel)
	if err != nil {
		fail(err)
		return
	}
	m.touchJob(id, func(j *Job) {
		j.Label = "VirtIO " + rel.Version
		j.Filename = rel.Filename
	})
	dir := m.isosDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fail(&OpError{"Could not create the ISO folder."})
		return
	}
	dest := filepath.Join(dir, rel.Filename)
	if !within(dir, dest) {
		fail(invalid("The VirtIO file name is not valid."))
		return
	}
	tmp := dest + ".partial"
	sum, n, total, err := downloadTo(ctx, rel.URL, tmp, func(done, all int64) {
		m.touchJob(id, func(j *Job) {
			j.Done = done
			j.Total = all
			if all > 0 {
				j.Percent = int(done * 100 / all)
			}
		})
	})
	if err != nil {
		os.Remove(tmp)
		fail(err)
		return
	}
	if err := verifyISOFile(tmp, total); err != nil {
		os.Remove(tmp)
		fail(err)
		return
	}
	if published := publishedSum(ctx, rel.URL, rel.Filename); published != "" {
		got := sum
		if len(published) == 32 {
			got = fileMD5(tmp)
		}
		if !strings.EqualFold(got, published) {
			os.Remove(tmp)
			fail(&OpError{"The download did not match Fedora's checksum."})
			return
		}
	}
	if err := os.Rename(tmp, dest); err != nil {
		os.Remove(tmp)
		fail(&OpError{"Could not save the VirtIO ISO."})
		return
	}
	relaxPerms(dest, false)
	side := sum + "  " + rel.Filename + "\n"
	_ = os.WriteFile(dest+".sha256", []byte(side), 0o644)
	_ = n
	m.relMu.Lock()
	delete(m.relCache, channel)
	m.relMu.Unlock()
	m.finishJob(id, rel.Filename, nil)
}

func (m *Manager) DeleteVirtio(name string) error {
	if err := checkFileName(name); err != nil {
		return err
	}
	low := strings.ToLower(name)
	if !strings.HasPrefix(low, "virtio-win") || !strings.HasSuffix(low, ".iso") {
		return invalid("Only a VirtIO driver ISO can be removed here.")
	}
	dir := m.isosDir()
	path := filepath.Join(dir, name)
	if !within(dir, path) {
		return invalid("That file is not in the ISO folder.")
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return invalid("That ISO is no longer there.")
		}
		return &OpError{"Could not delete the ISO."}
	}
	_ = os.Remove(path + ".sha256")
	return nil
}

func (m *Manager) installedVirtio() []VirtioFile {
	dir := m.isosDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []VirtioFile{}
	}
	var out []VirtioFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		low := strings.ToLower(name)
		if !strings.HasPrefix(low, "virtio-win") || !strings.HasSuffix(low, ".iso") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		f := VirtioFile{Name: name, Size: info.Size()}
		if b, err := os.ReadFile(filepath.Join(dir, name+".sha256")); err == nil {
			fields := strings.Fields(string(b))
			if len(fields) > 0 && len(fields[0]) == 64 {
				f.SHA256 = fields[0]
			}
		}
		out = append(out, f)
	}
	if out == nil {
		out = []VirtioFile{}
	}
	return out
}

func (m *Manager) touchJob(id string, fn func(*Job)) {
	m.jobMu.Lock()
	defer m.jobMu.Unlock()
	if j, ok := m.jobs[id]; ok {
		fn(j)
	}
}

func (m *Manager) finishJob(id, filename string, err error) {
	m.jobMu.Lock()
	defer m.jobMu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return
	}
	j.Finished = true
	j.Percent = 100
	if filename != "" {
		j.Filename = filename
	}
	if err != nil {
		j.Percent = 0
		j.Error = err.Error()
	}
	// Keep the map from growing without bound.
	if len(m.jobs) > 24 {
		for k, old := range m.jobs {
			if old.Finished && k != id {
				delete(m.jobs, k)
				break
			}
		}
	}
}

func virtioURL(channel string) string {
	switch channel {
	case "stable":
		return virtioBase + "stable-virtio/virtio-win.iso"
	case "latest":
		return virtioBase + "latest-virtio/virtio-win.iso"
	default:
		return ""
	}
}

func resolveVirtio(ctx context.Context, channel string) (VirtioRelease, error) {
	start := virtioURL(channel)
	if start == "" {
		return VirtioRelease{}, invalid("Choose the stable or the latest VirtIO driver ISO.")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, start, nil)
	if err != nil {
		return VirtioRelease{}, err
	}
	req.Header.Set("User-Agent", "FELDCORE")
	req.Header.Set("Range", "bytes=0-0")
	client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: virtioRedirect}
	resp, err := client.Do(req)
	if err != nil {
		return VirtioRelease{}, &OpError{"Could not reach Fedora's VirtIO downloads. Drop a virtio-win ISO into the isos share instead."}
	}
	defer resp.Body.Close()
	if resp.Request == nil || resp.Request.URL == nil {
		return VirtioRelease{}, &OpError{"Fedora did not point at a VirtIO ISO."}
	}
	final := *resp.Request.URL
	final.Scheme = "https"
	if err := allowedVirtio(&final); err != nil {
		return VirtioRelease{}, err
	}
	ver, file := virtioNames(final.String())
	if file == "" || ver == "" {
		return VirtioRelease{}, &OpError{"Fedora did not point at a VirtIO ISO."}
	}
	final.Path = path.Clean(final.Path)
	// The range request only checked the redirect. The full file is fetched
	// from the canonical HTTPS URL.
	return VirtioRelease{Channel: channel, Version: ver, Filename: file, URL: final.String()}, nil
}

func virtioRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 6 {
		return fmt.Errorf("too many redirects")
	}
	if err := allowedVirtio(req.URL); err != nil {
		return err
	}
	req.URL.Scheme = "https"
	return nil
}

func allowedVirtio(u *url.URL) error {
	if u == nil {
		return invalid("The VirtIO download URL is not valid.")
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return invalid("The VirtIO download URL is not valid.")
	}
	if !strings.EqualFold(u.Hostname(), "fedorapeople.org") {
		return invalid("VirtIO ISOs are only downloaded from Fedora.")
	}
	if !strings.HasPrefix(u.Path, "/groups/virt/virtio-win/direct-downloads/") {
		return invalid("VirtIO ISOs are only downloaded from Fedora's virtio-win folder.")
	}
	return nil
}

// virtioNames pulls the version and the file name we save from a resolved URL.
func virtioNames(raw string) (version, filename string) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", ""
	}
	base := path.Base(u.Path)
	dir := path.Base(path.Dir(u.Path))
	if m := virtioDirVer.FindStringSubmatch(dir); len(m) == 2 {
		version = m[1]
	}
	low := strings.ToLower(base)
	if strings.HasPrefix(low, "virtio-win-") && strings.HasSuffix(low, ".iso") {
		filename = base
		if version == "" {
			if m := virtioFileVer.FindStringSubmatch(base); len(m) == 2 {
				version = m[1]
			}
		}
		return version, filename
	}
	if version == "" {
		return "", ""
	}
	return version, "virtio-win-" + version + ".iso"
}

func downloadTo(ctx context.Context, rawURL, dest string, progress func(done, total int64)) (sum string, n, total int64, err error) {
	if u, err := url.Parse(rawURL); err != nil || allowedVirtio(u) != nil {
		return "", 0, 0, invalid("VirtIO ISOs are only downloaded from Fedora.")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", 0, 0, err
	}
	req.Header.Set("User-Agent", "FELDCORE")
	client := &http.Client{CheckRedirect: virtioRedirect}
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, 0, &OpError{"The VirtIO download failed: " + err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, 0, &OpError{fmt.Sprintf("Fedora returned %d for the VirtIO ISO.", resp.StatusCode)}
	}
	total = resp.ContentLength
	if total < 32<<20 {
		return "", 0, 0, &OpError{"Fedora did not offer a complete VirtIO ISO."}
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return "", 0, 0, &OpError{"Could not write the VirtIO ISO."}
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, 256<<10)
	var done int64
	for {
		nr, rerr := resp.Body.Read(buf)
		if nr > 0 {
			if _, werr := f.Write(buf[:nr]); werr != nil {
				return "", done, total, &OpError{"Could not write the VirtIO ISO."}
			}
			_, _ = h.Write(buf[:nr])
			done += int64(nr)
			if progress != nil && (done == total || done%(1<<20) < int64(nr)) {
				progress(done, total)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return "", done, total, &OpError{"The VirtIO download was interrupted."}
		}
	}
	if done != total {
		return "", done, total, &OpError{"The VirtIO download ended early, so it was not saved."}
	}
	if err := f.Sync(); err != nil {
		return "", done, total, &OpError{"Could not save the VirtIO ISO."}
	}
	return hex.EncodeToString(h.Sum(nil)), done, total, nil
}

// verifyISOFile checks that path is a complete ISO 9660 image. Fedora's
// directory listing is behind a browser challenge and the ISO has no separate
// SHA256 beside it, so the check is the file size plus the CD001 volume
// descriptor. A published checksum is applied as well when one is present.
func verifyISOFile(path string, expect int64) error {
	st, err := os.Stat(path)
	if err != nil {
		return &OpError{"The VirtIO ISO was not saved."}
	}
	if expect > 0 && st.Size() != expect {
		return &OpError{"The VirtIO download ended early, so it was not saved."}
	}
	if st.Size() < 32<<20 {
		return &OpError{"The download is too small to be a VirtIO ISO."}
	}
	f, err := os.Open(path)
	if err != nil {
		return &OpError{"The VirtIO ISO was not saved."}
	}
	defer f.Close()
	hdr := make([]byte, 6)
	if _, err := f.ReadAt(hdr, 32768); err != nil || hdr[0] != 1 || string(hdr[1:]) != "CD001" {
		return &OpError{"The download is not an ISO image, so it was not saved."}
	}
	return nil
}

func publishedSum(ctx context.Context, isoURL, filename string) string {
	u, err := url.Parse(isoURL)
	if err != nil {
		return ""
	}
	u.Path = path.Dir(u.Path) + "/CHECKSUM"
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "FELDCORE")
	resp, err := (&http.Client{Timeout: 15 * time.Second, CheckRedirect: virtioRedirect}).Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		return ""
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return ""
	}
	return checksumFor(string(body), filename)
}

// checksumFor finds an md5 (32 hex) or sha256 (64 hex) line for filename.
func checksumFor(text, filename string) string {
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		name := strings.TrimPrefix(f[len(f)-1], "*")
		if name != filename && name != "virtio-win.iso" {
			continue
		}
		sum := strings.ToLower(f[0])
		if len(sum) == 64 || len(sum) == 32 {
			if _, err := hex.DecodeString(sum); err == nil {
				return sum
			}
		}
	}
	return ""
}

func fileMD5(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
