package osupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Herculade0/feldcore/internal/store"
)

// Service checks, stages, and rolls back FELDCORE OS images.
type Service struct {
	// Root is a filesystem prefix. Empty means the real machine.
	Root string
	// ESP overrides the EFI system partition mount. Empty is <root>/efi.
	ESP string
	// DataDir is the state directory, normally /var/lib/feldcore.
	DataDir string
	// SlotA and SlotB override the block devices for the two root slots.
	SlotA string
	SlotB string
	// PubKey, when set, is the only public key that is accepted.
	PubKey string
	// ManifestURL, when set, wins over the address saved in the state file.
	ManifestURL string
	// AllowLocal permits http and loopback, for tests.
	AllowLocal    bool
	DaemonVersion string
	Store         *store.Store
	Client        *http.Client
	Now           func() time.Time
	RebootFn      func(context.Context) error
	RefreshFn     func(context.Context) error
	Log           *slog.Logger

	mu sync.Mutex
}

// Status is the Updates page.
type Status struct {
	Appliance     bool       `json:"appliance"`
	Version       string     `json:"version"`
	DaemonVersion string     `json:"daemonVersion"`
	Kernel        string     `json:"kernel"`
	Slot          string     `json:"slot,omitempty"`
	SecureBoot    string     `json:"secureBoot"`
	Slots         []SlotInfo `json:"slots"`
	Previous      *SlotInfo  `json:"previous,omitempty"`
	NextBoot      string     `json:"nextBoot,omitempty"`
	NextVersion   string     `json:"nextVersion,omitempty"`
	NextTries     *int       `json:"nextTries,omitempty"`
	ManifestURL   string     `json:"manifestURL"`
	URLLocked     bool       `json:"urlLocked"`
	PubKey        string     `json:"pubKey,omitempty"`
	PubKeyFound   bool       `json:"pubKeyFound"`
	Available     *Release   `json:"available,omitempty"`
	ScheduleAt    string     `json:"scheduleAt,omitempty"`
	Sysexts       []Sysext   `json:"sysexts"`
	Warning       string     `json:"warning,omitempty"`
}

// Status reports the booted image, the signed release last checked, and extensions.
func (s *Service) Status(context.Context) Status {
	lay := s.readLayout()
	st := Status{
		Appliance:     lay.Appliance,
		Version:       lay.Version,
		DaemonVersion: s.DaemonVersion,
		Kernel:        lay.Kernel,
		Slot:          lay.Slot,
		SecureBoot:    "off",
		Slots:         lay.Slots,
		Previous:      lay.Previous,
		NextBoot:      lay.NextBoot,
		NextVersion:   lay.NextVersion,
		NextTries:     lay.NextTries,
		ManifestURL:   s.manifestURL(),
		URLLocked:     s.urlLocked(),
		Sysexts:       []Sysext{},
	}
	if st.Version == "" {
		st.Version = s.DaemonVersion
	}
	if st.Slots == nil {
		st.Slots = []SlotInfo{}
	}
	if p, err := s.pubKeyPath(); err == nil {
		st.PubKey = p
		st.PubKeyFound = true
	} else {
		st.Warning = err.Error()
	}
	if s.Store != nil {
		cfg := s.Store.OSConfig()
		st.ScheduleAt = cfg.ScheduleAt
		if cfg.CheckedJSON != "" {
			var rel Release
			if json.Unmarshal([]byte(cfg.CheckedJSON), &rel) == nil && rel.Version != "" {
				st.Available = &rel
			}
		}
	}
	st.Sysexts = s.listSysexts(lay.Kernel)
	return st
}

// Check downloads the manifest, verifies its signature, and remembers it.
func (s *Service) Check(ctx context.Context) (Release, error) {
	m, err := s.fetchManifest(ctx)
	if err != nil {
		return Release{}, err
	}
	s.remember(m)
	return m.release(), nil
}

// SetManifestURL saves the address the Updates page checks. An empty address
// returns to the default. An address set by the process cannot be changed here.
func (s *Service) SetManifestURL(raw string) error {
	if s.urlLocked() {
		return input("The update address is set by the system.")
	}
	if s.Store == nil {
		return op("There is nowhere to save the update address.")
	}
	raw = strings.TrimSpace(raw)
	if raw != "" {
		if err := validateUpdateURL(raw, s.AllowLocal); err != nil {
			return err
		}
	}
	return s.Store.UpdateOS(func(c *store.OSConfig) error {
		c.ManifestURL = raw
		c.CheckedJSON = ""
		c.CheckedAt = ""
		return nil
	})
}

// SetSchedule stores a one-shot time. An empty time clears it.
func (s *Service) SetSchedule(at string) error {
	if s.Store == nil {
		return op("There is nowhere to save the schedule.")
	}
	at = strings.TrimSpace(at)
	if at != "" {
		t, err := time.Parse(time.RFC3339, at)
		if err != nil {
			return input("Choose a date and time.")
		}
		if !t.After(s.now()) {
			return input("Choose a time in the future.")
		}
		at = t.UTC().Format(time.RFC3339)
	}
	return s.Store.UpdateOS(func(c *store.OSConfig) error {
		c.ScheduleAt = at
		return nil
	})
}

// TakeSchedule reports whether a scheduled update is due, and clears it so it
// runs once.
func (s *Service) TakeSchedule() bool {
	if s.Store == nil {
		return false
	}
	cfg := s.Store.OSConfig()
	if cfg.ScheduleAt == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, cfg.ScheduleAt)
	if err != nil || s.now().Before(t) {
		return false
	}
	if err := s.Store.UpdateOS(func(c *store.OSConfig) error {
		c.ScheduleAt = ""
		return nil
	}); err != nil && s.Log != nil {
		s.Log.Warn("could not clear the update schedule", "err", err)
	}
	return true
}

// Loop starts a due update. start is the job runner.
func (s *Service) Loop(ctx context.Context, start func()) {
	if start == nil {
		return
	}
	tick := time.NewTicker(20 * time.Second)
	defer tick.Stop()
	fire := func() {
		if s.TakeSchedule() {
			start()
		}
	}
	fire()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			fire()
		}
	}
}

// Stage checks the signed release and writes it into the inactive slot.
// The boot entry gets three tries. The running slot is not modified.
func (s *Service) Stage(ctx context.Context, logf func(string)) (Release, error) {
	if logf == nil {
		logf = func(string) {}
	}
	var rel Release
	err := s.locked(func() error {
		var err error
		rel, err = s.stage(ctx, logf)
		return err
	})
	return rel, err
}

func (s *Service) stage(ctx context.Context, logf func(string)) (Release, error) {
	lay := s.readLayout()
	if !lay.Appliance {
		return Release{}, input("This machine is not running the FELDCORE OS image.")
	}
	if lay.Slot != "a" && lay.Slot != "b" {
		return Release{}, input("This boot did not name its slot.")
	}
	inactive := OtherSlot(lay.Slot)
	dev := s.slotDevice(inactive)
	size, err := deviceSize(dev)
	if err != nil || size <= 0 {
		return Release{}, input("Slot " + strings.ToUpper(inactive) + " is not on this disk.")
	}
	logf("Checking the signed release.")
	m, err := s.fetchManifest(ctx)
	if err != nil {
		return Release{}, err
	}
	s.remember(m)
	if m.Version == lay.Version {
		return Release{}, input("This image is already running " + m.Version + ".")
	}
	if err := m.installable(); err != nil {
		return Release{}, err
	}
	root, _ := m.artifact(kindRoot)
	kernel, _ := m.artifact(kindKernel)
	initrd, _ := m.artifact(kindInitrd)
	if root.Size > size {
		return Release{}, input("The new image is larger than the inactive slot.")
	}
	if root.Size > 8<<30 || kernel.Size > 128<<20 || initrd.Size > 256<<20 {
		return Release{}, input("The release is larger than this machine will write.")
	}
	logf("Writing the image into slot " + strings.ToUpper(inactive) + ".")
	if err := s.writeArtifact(ctx, root, dev, false, logf); err != nil {
		return Release{}, err
	}
	incoming := filepath.Join(s.dataDir(), "updates", "incoming")
	if err := os.MkdirAll(incoming, 0o755); err != nil {
		return Release{}, op("Could not prepare the update directory.")
	}
	kpath := filepath.Join(incoming, "vmlinuz")
	ipath := filepath.Join(incoming, "initrd.img")
	logf("Downloading the kernel.")
	if err := s.writeArtifact(ctx, kernel, kpath, true, logf); err != nil {
		return Release{}, err
	}
	logf("Downloading the initrd.")
	if err := s.writeArtifact(ctx, initrd, ipath, true, logf); err != nil {
		return Release{}, err
	}
	bootDir := filepath.Join(s.espDir(), "feldcore", inactive)
	if err := os.MkdirAll(bootDir, 0o755); err != nil {
		return Release{}, op("The EFI system partition is not writable.")
	}
	if err := replaceFile(kpath, filepath.Join(bootDir, "vmlinuz")); err != nil {
		return Release{}, op("Could not install the kernel.")
	}
	if err := replaceFile(ipath, filepath.Join(bootDir, "initrd.img")); err != nil {
		return Release{}, op("Could not install the initrd.")
	}
	if err := s.publishEntry(inactive, m.Version); err != nil {
		return Release{}, err
	}
	logf("Slot " + strings.ToUpper(inactive) + " will boot " + m.Version + " on the next restart. It has three tries.")
	return m.release(), nil
}

func (s *Service) writeArtifact(ctx context.Context, art Artifact, dest string, truncate bool, logf func(string)) error {
	flag := os.O_WRONLY
	if truncate {
		flag |= os.O_CREATE | os.O_TRUNC
	}
	f, err := os.OpenFile(dest, flag, 0o644)
	if err != nil {
		return op("Could not open the destination for the image.")
	}
	defer f.Close()
	if !truncate {
		if _, err := f.Seek(0, 0); err != nil {
			return op("Could not write the image.")
		}
	}
	rawURL, err := artifactURL(s.manifestURL(), art.Name)
	if err != nil {
		return err
	}
	sum, _, err := s.download(ctx, rawURL, art.Encoding, f, art.Size, true, func(done, total int64) {
		if total <= 0 {
			return
		}
		logf(fmt.Sprintf("%s %d%%", art.Kind, done*100/total))
	})
	if err != nil {
		return err
	}
	if !strings.EqualFold(sum, art.SHA256) {
		return input("The downloaded " + art.Kind + " does not match the signed SHA-256.")
	}
	if err := f.Sync(); err != nil {
		return op("Could not finish writing the image.")
	}
	return nil
}

func (s *Service) publishEntry(slot, version string) error {
	dir := filepath.Join(s.espDir(), "loader", "entries")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return op("The EFI system partition is not writable.")
	}
	name := EntryName(slot, BootTries)
	path := filepath.Join(dir, name)
	if err := writeAtomic(path, []byte(EntryBody(slot, version)), 0o644); err != nil {
		return op("Could not write the boot entry.")
	}
	list, _ := os.ReadDir(dir)
	for _, e := range list {
		ent, ok := ParseEntryName(e.Name())
		if ok && ent.Slot == slot && e.Name() != name {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
	conf := filepath.Join(s.espDir(), "loader", "loader.conf")
	cur, _ := os.ReadFile(conf)
	text := string(cur)
	if strings.TrimSpace(text) == "" {
		text = "timeout 3\nconsole-mode max\neditor no\nauto-entries no\nauto-firmware no\n"
	}
	if err := writeAtomic(conf, []byte(ClearDefault(text)), 0o644); err != nil {
		return op("Could not update the boot menu.")
	}
	return nil
}

// Rollback selects the other slot for the next boot. It does not reboot.
func (s *Service) Rollback(context.Context) (SlotInfo, error) {
	var info SlotInfo
	err := s.locked(func() error {
		var err error
		info, err = s.rollback()
		return err
	})
	return info, err
}

func (s *Service) rollback() (SlotInfo, error) {
	lay := s.readLayout()
	if !lay.Appliance {
		return SlotInfo{}, input("This machine is not running the FELDCORE OS image.")
	}
	if lay.Slot != "a" && lay.Slot != "b" {
		return SlotInfo{}, input("This boot did not name its slot.")
	}
	entries := s.readEntries()
	loader, _ := os.ReadFile(filepath.Join(s.espDir(), "loader", "loader.conf"))
	next, ok := NextBoot(DefaultName(string(loader)), entries)
	if !ok {
		return SlotInfo{}, input("There is no previous image to boot.")
	}
	other := "a"
	if next.Slot == "a" {
		other = "b"
	}
	best := bestEntry(entries, other)
	if best.Name == "" {
		return SlotInfo{}, input("There is no previous image to boot.")
	}
	if best.Version != "" && next.Version != "" && compareVersion(best.Version, next.Version) > 0 {
		return SlotInfo{}, input("The next boot is already the earlier image.")
	}
	dir := filepath.Join(s.espDir(), "loader", "entries")
	stable := EntryName(other, 0)
	body := best.Body
	if strings.TrimSpace(body) == "" {
		body = EntryBody(other, best.Version)
	}
	if err := writeAtomic(filepath.Join(dir, stable), []byte(body), 0o644); err != nil {
		return SlotInfo{}, op("Could not write the boot entry.")
	}
	list, _ := os.ReadDir(dir)
	for _, e := range list {
		ent, ok := ParseEntryName(e.Name())
		if ok && ent.Slot == other && e.Name() != stable {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
	conf := filepath.Join(s.espDir(), "loader", "loader.conf")
	cur, _ := os.ReadFile(conf)
	text := string(cur)
	if strings.TrimSpace(text) == "" {
		text = "timeout 3\nconsole-mode max\neditor no\nauto-entries no\nauto-firmware no\n"
	}
	if err := writeAtomic(conf, []byte(SetDefault(text, stable)), 0o644); err != nil {
		return SlotInfo{}, op("Could not update the boot menu.")
	}
	info := SlotInfo{ID: other, Version: best.Version, Good: true, Present: true}
	return info, nil
}

// Reboot asks systemd to reboot. The caller has already staged or rolled back.
func (s *Service) Reboot(ctx context.Context) error {
	if !s.readLayout().Appliance {
		return input("This machine is not running the FELDCORE OS image.")
	}
	if s.RebootFn != nil {
		return s.RebootFn(ctx)
	}
	cmd := exec.CommandContext(ctx, "systemctl", "reboot")
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = "Could not reboot."
		}
		return op(clip(msg))
	}
	return nil
}

func (s *Service) fetchManifest(ctx context.Context) (Manifest, error) {
	u := s.manifestURL()
	if err := validateUpdateURL(u, s.AllowLocal); err != nil {
		return Manifest{}, err
	}
	pub, _, err := s.publicKey()
	if err != nil {
		return Manifest{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	var man, sig strings.Builder
	if _, _, err := s.download(ctx, u, "", &stringWriter{&man}, maxManifest, false, nil); err != nil {
		return Manifest{}, err
	}
	if _, _, err := s.download(ctx, u+".minisig", "", &stringWriter{&sig}, 16<<10, false, nil); err != nil {
		return Manifest{}, input("The release has no signature.")
	}
	raw := []byte(man.String())
	if err := Verify(pub, raw, []byte(sig.String())); err != nil {
		return Manifest{}, err
	}
	return ParseManifest(raw)
}

func (s *Service) remember(m Manifest) {
	s.cacheManifest(m)
	if s.Store == nil {
		return
	}
	rel := m.release()
	buf, err := json.Marshal(rel)
	if err != nil {
		return
	}
	_ = s.Store.UpdateOS(func(c *store.OSConfig) error {
		c.CheckedJSON = string(buf)
		c.CheckedAt = s.now().UTC().Format(time.RFC3339)
		return nil
	})
}

func (s *Service) publicKey() (PublicKey, string, error) {
	path, err := s.pubKeyPath()
	if err != nil {
		return PublicKey{}, "", err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return PublicKey{}, path, input("The update public key could not be read.")
	}
	k, err := ParsePublic(string(b))
	if err != nil {
		return PublicKey{}, path, err
	}
	return k, path, nil
}

func (s *Service) pubKeyPath() (string, error) {
	if s.PubKey != "" {
		if st, err := os.Stat(s.PubKey); err != nil || st.IsDir() {
			return "", input("The update public key is not at the configured path.")
		}
		return s.PubKey, nil
	}
	for _, p := range []string{
		filepath.Join(s.dataDir(), "update.pub"),
		s.join("usr", "share", "feldcore", "update.pub"),
	} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", input("No update public key. Place a minisign public key at /var/lib/feldcore/update.pub.")
}

func (s *Service) locked(fn func() error) error {
	if !s.mu.TryLock() {
		return ErrBusy
	}
	defer s.mu.Unlock()
	return fn()
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

type stringWriter struct{ b *strings.Builder }

func (w stringWriter) Write(p []byte) (int, error) { return w.b.Write(p) }

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func replaceFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := out.ReadFrom(in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	out.Close()
	return os.Rename(tmp, dst)
}

func clip(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 240 {
		return s[:240]
	}
	return s
}
