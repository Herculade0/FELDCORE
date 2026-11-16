package backup

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const repoDirName = "feldcore-restic"

// Sets is what one destination copies. Internal Affairs is on in DefaultSets.
type Sets struct {
	Config          bool `json:"config"`
	Appdata         bool `json:"appdata"`
	Compose         bool `json:"compose"`
	VMs             bool `json:"vms"`
	InternalAffairs bool `json:"internalAffairs"`
	Snapshots       bool `json:"snapshots"`
}

// DefaultSets is the first backup set. The Internal Affairs cabinet is included.
func DefaultSets() Sets {
	return Sets{Config: true, Appdata: true, Compose: true, VMs: true, InternalAffairs: true, Snapshots: true}
}

func (s Sets) any() bool {
	return s.Config || s.Appdata || s.Compose || s.VMs || s.InternalAffairs || s.Snapshots
}

func (s Sets) names() []string {
	var out []string
	if s.Config {
		out = append(out, "config")
	}
	if s.Appdata {
		out = append(out, "appdata")
	}
	if s.Compose {
		out = append(out, "compose")
	}
	if s.VMs {
		out = append(out, "vms")
	}
	if s.InternalAffairs {
		out = append(out, "internal-affairs")
	}
	if s.Snapshots {
		out = append(out, "snapshots")
	}
	return out
}

// Destination is one place copies are kept. Secrets are not stored on it.
type Destination struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Kind           string    `json:"kind"`
	Enabled        bool      `json:"enabled"`
	Path           string    `json:"path,omitempty"`
	Share          string    `json:"share,omitempty"`
	Host           string    `json:"host,omitempty"`
	Port           int       `json:"port,omitempty"`
	Username       string    `json:"username,omitempty"`
	HasPassword    bool      `json:"hasPassword"`
	HasKey         bool      `json:"hasKey"`
	UseSavedProton bool      `json:"useSavedProton,omitempty"`
	ScheduleMode   string    `json:"scheduleMode"`
	ScheduleTime   string    `json:"scheduleTime,omitempty"`
	ScheduleDay    string    `json:"scheduleDay,omitempty"`
	KeepDaily      int       `json:"keepDaily"`
	KeepWeekly     int       `json:"keepWeekly"`
	KeepMonthly    int       `json:"keepMonthly"`
	Sets           Sets      `json:"sets"`
	LastRun        time.Time `json:"lastRun,omitempty"`
	LastOK         bool      `json:"lastOk"`
	LastError      string    `json:"lastError,omitempty"`
	LastSnapshot   string    `json:"lastSnapshot,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
}

// DestinationInput is the form. Password and Key are write-only.
type DestinationInput struct {
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	Enabled        bool   `json:"enabled"`
	Path           string `json:"path"`
	Share          string `json:"share"`
	Host           string `json:"host"`
	Port           int    `json:"port"`
	Username       string `json:"username"`
	Password       string `json:"password,omitempty"`
	Key            string `json:"key,omitempty"`
	Mailbox        string `json:"mailbox,omitempty"`
	TwoFA          string `json:"twoFa,omitempty"`
	ClearPassword  bool   `json:"clearPassword,omitempty"`
	ClearKey       bool   `json:"clearKey,omitempty"`
	UseSavedProton bool   `json:"useSavedProton"`
	ScheduleMode   string `json:"scheduleMode"`
	ScheduleTime   string `json:"scheduleTime"`
	ScheduleDay    string `json:"scheduleDay"`
	KeepDaily      int    `json:"keepDaily"`
	KeepWeekly     int    `json:"keepWeekly"`
	KeepMonthly    int    `json:"keepMonthly"`
	Sets           *Sets  `json:"sets"`
}

type iaRecord struct {
	At            time.Time `json:"at"`
	DestinationID string    `json:"destinationId"`
	SnapshotID    string    `json:"snapshotId"`
	Head          string    `json:"head"`
}

type catalogFile struct {
	Destinations []Destination `json:"destinations"`
	IAHeads      []iaRecord    `json:"iaHeads,omitempty"`
}

var (
	destNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 _.-]{0,63}$`)
	shareRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,31}$`)
	hostRe     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.:-]{0,252}$`)
	userRe     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,31}$`)
	remotePath = regexp.MustCompile(`^[A-Za-z0-9_./-]{1,400}$`)
	idRe       = regexp.MustCompile(`^[a-f0-9]{16}$`)
	snapRe     = regexp.MustCompile(`^[a-fA-F0-9]{8,64}$`)
)

var blockedBackup = []string{
	"/proc", "/sys", "/dev", "/etc", "/boot", "/bin", "/sbin",
	"/usr", "/lib", "/lib64", "/root", "/run",
}

func newDestID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func (s *Service) normalize(in DestinationInput, prev *Destination) (Destination, error) {
	name := strings.TrimSpace(in.Name)
	if !destNameRe.MatchString(name) {
		return Destination{}, input("Name the backup with letters, numbers, spaces, dots, hyphens or underscores.")
	}
	kind := strings.ToLower(strings.TrimSpace(in.Kind))
	switch kind {
	case "local", "smb", "nfs", "sftp", "proton":
	default:
		return Destination{}, input("Choose a local folder, SMB, NFS, SFTP, or Proton Drive.")
	}
	if in.KeepDaily < 0 || in.KeepWeekly < 0 || in.KeepMonthly < 0 ||
		in.KeepDaily > 3650 || in.KeepWeekly > 3650 || in.KeepMonthly > 3650 {
		return Destination{}, input("Retention is between 0 and 3650. Zero in every box keeps every version.")
	}
	mode, clock, day, err := cleanSchedule(in.ScheduleMode, in.ScheduleTime, in.ScheduleDay)
	if err != nil {
		return Destination{}, err
	}
	sets := DefaultSets()
	if in.Sets != nil {
		sets = *in.Sets
	}
	if !sets.any() {
		return Destination{}, input("Choose at least one thing to back up. Internal Affairs is included unless you turn it off.")
	}
	d := Destination{
		Name: name, Kind: kind, Enabled: in.Enabled, Sets: sets,
		ScheduleMode: mode, ScheduleTime: clock, ScheduleDay: day,
		KeepDaily: in.KeepDaily, KeepWeekly: in.KeepWeekly, KeepMonthly: in.KeepMonthly,
		UseSavedProton: kind == "proton" && in.UseSavedProton,
	}
	if prev != nil {
		d.ID = prev.ID
		d.CreatedAt = prev.CreatedAt
		d.LastRun, d.LastOK, d.LastError, d.LastSnapshot = prev.LastRun, prev.LastOK, prev.LastError, prev.LastSnapshot
		d.HasPassword, d.HasKey = prev.HasPassword, prev.HasKey
	} else {
		id, err := newDestID()
		if err != nil {
			return Destination{}, op("Could not prepare the backup.")
		}
		d.ID = id
		d.CreatedAt = s.now()
	}
	switch kind {
	case "local":
		if err := s.cleanLocal(&d, in); err != nil {
			return Destination{}, err
		}
	case "smb":
		if err := cleanNet(&d, in, true); err != nil {
			return Destination{}, err
		}
	case "nfs":
		if err := cleanNet(&d, in, false); err != nil {
			return Destination{}, err
		}
	case "sftp":
		if err := cleanSFTP(&d, in); err != nil {
			return Destination{}, err
		}
	case "proton":
		if err := s.cleanProton(&d, in); err != nil {
			return Destination{}, err
		}
	}
	if err := s.storeSecrets(d.ID, kind, in, &d); err != nil {
		return Destination{}, err
	}
	if d.Enabled {
		if err := s.ready(d); err != nil {
			return Destination{}, err
		}
	}
	return d, nil
}

func (s *Service) cleanLocal(d *Destination, in DestinationInput) error {
	share := strings.TrimSpace(in.Share)
	path := strings.TrimSpace(in.Path)
	if share != "" && path != "" && filepath.IsAbs(path) {
		return input("Choose a share or a path, not both.")
	}
	if share != "" {
		if !shareRe.MatchString(share) {
			return input("Share names use letters, numbers, _ and -.")
		}
		if path != "" {
			if filepath.IsAbs(path) || strings.Contains(path, "..") || strings.ContainsAny(path, "\\\x00") {
				return input("The folder inside the share is a relative path.")
			}
		}
		d.Share = share
		d.Path = path
		return nil
	}
	abs, err := cleanAbs(path)
	if err != nil {
		return err
	}
	d.Path = abs
	return nil
}

func cleanNet(d *Destination, in DestinationInput, smb bool) error {
	host := strings.TrimSpace(in.Host)
	if !hostRe.MatchString(host) || strings.Contains(host, "..") {
		return input("Enter the server address, such as 192.168.1.20 or nas.home.")
	}
	share := strings.Trim(strings.TrimSpace(in.Path), "/")
	if smb {
		if share == "" || strings.Contains(share, "..") || strings.ContainsAny(share, `/\`) || len(share) > 80 {
			return input("Enter the SMB share name.")
		}
	} else if !validRemotePath(share) {
		return input("Enter the NFS export, such as /volume1/backups.")
	}
	user := strings.TrimSpace(in.Username)
	if user != "" && !userRe.MatchString(user) {
		return input("The user name can use letters, numbers, dots, _ and -.")
	}
	if in.Port < 0 || in.Port > 65535 {
		return input("The port is between 1 and 65535.")
	}
	d.Host = host
	d.Path = share
	d.Username = user
	d.Port = in.Port
	return nil
}

func cleanSFTP(d *Destination, in DestinationInput) error {
	host := strings.TrimSpace(in.Host)
	if !hostRe.MatchString(host) || strings.Contains(host, "..") {
		return input("Enter the SFTP server address.")
	}
	user := strings.TrimSpace(in.Username)
	if !userRe.MatchString(user) {
		return input("Enter the SFTP user name.")
	}
	p := strings.TrimSpace(in.Path)
	if !validRemotePath(p) {
		return input("The SFTP folder is an absolute path using letters, numbers, / _ . and -.")
	}
	if in.Port < 0 || in.Port > 65535 {
		return input("The port is between 1 and 65535.")
	}
	d.Host = host
	d.Username = user
	d.Path = p
	d.Port = in.Port
	return nil
}

func (s *Service) cleanProton(d *Destination, in DestinationInput) error {
	remote, err := CleanRemote(in.Path)
	if err != nil {
		return err
	}
	d.Path = remote
	if d.UseSavedProton {
		return nil
	}
	user := strings.TrimSpace(in.Username)
	if strings.ContainsAny(user, "\r\n") || len(user) > 200 {
		return input("Enter the Proton account email.")
	}
	d.Username = user
	return nil
}

func (s *Service) storeSecrets(id, kind string, in DestinationInput, d *Destination) error {
	if s.Secrets == nil && (in.Password != "" || in.Key != "" || in.Mailbox != "" || in.TwoFA != "" || in.ClearPassword || in.ClearKey) {
		return op("Secrets are not available, so the password could not be stored.")
	}
	if s.Secrets == nil {
		return nil
	}
	put := func(name, value string) error {
		if err := s.Secrets.Put(name, value); err != nil {
			return op("Could not store that secret.")
		}
		return nil
	}
	passName := passSecret(id)
	keyName := keySecret(id)
	if kind == "proton" && !d.UseSavedProton {
		// A destination-specific Proton password stays out of the Settings card.
	}
	if in.ClearPassword {
		if err := put(passName, ""); err != nil {
			return err
		}
		d.HasPassword = false
	} else if in.Password != "" {
		if err := put(passName, in.Password); err != nil {
			return err
		}
		d.HasPassword = true
	}
	if in.ClearKey {
		if err := put(keyName, ""); err != nil {
			return err
		}
		d.HasKey = false
	} else if strings.TrimSpace(in.Key) != "" {
		if !strings.Contains(in.Key, "PRIVATE KEY") {
			return input("Paste a private key, including the BEGIN line.")
		}
		if err := put(keyName, in.Key); err != nil {
			return err
		}
		d.HasKey = true
	}
	if in.Mailbox != "" {
		if err := put(mailSecret(id), in.Mailbox); err != nil {
			return err
		}
	}
	if in.TwoFA != "" {
		if err := put(otpSecretName(id), in.TwoFA); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) ready(d Destination) error {
	switch d.Kind {
	case "local":
		return nil
	case "smb":
		if d.Username != "" && !d.HasPassword {
			return input("Enter the password for that SMB share.")
		}
	case "nfs":
		return nil
	case "sftp":
		if !d.HasKey {
			return input("Paste the SFTP private key. It is stored encrypted, and it is not written to the log.")
		}
	case "proton":
		if d.UseSavedProton {
			if s.Store == nil || strings.TrimSpace(s.Store.Snapshot().Backup.Username) == "" || !s.Store.Snapshot().Backup.HasPassword {
				return input("Save the Proton account on the Settings page, or enter it here.")
			}
			return nil
		}
		if strings.TrimSpace(d.Username) == "" || !d.HasPassword {
			return input("Enter the Proton email and password, or use the account saved in Settings.")
		}
	}
	return nil
}

func cleanAbs(p string) (string, error) {
	p = strings.TrimSpace(p)
	if !filepath.IsAbs(p) {
		return "", input("Use an absolute path, or choose a share.")
	}
	c := filepath.Clean(p)
	if strings.Count(c, "/") < 2 {
		return "", input("Choose a folder, not the root of a disk.")
	}
	for _, b := range blockedBackup {
		if c == b || strings.HasPrefix(c, b+"/") {
			return "", input("That folder is part of the operating system.")
		}
	}
	return c, nil
}

func validRemotePath(p string) bool {
	if !strings.HasPrefix(p, "/") || strings.Contains(p, "..") || !remotePath.MatchString(p) {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return false
		}
	}
	return true
}

func repoFolder(path string) string {
	if filepath.Base(path) == repoDirName {
		return path
	}
	return filepath.Join(path, repoDirName)
}

func remoteRepo(path string) string {
	path = strings.TrimRight(path, "/")
	if strings.HasSuffix(path, "/"+repoDirName) || path == repoDirName {
		return path
	}
	return path + "/" + repoDirName
}

func passSecret(id string) string    { return "bkp-" + id }
func keySecret(id string) string     { return "bkk-" + id }
func mailSecret(id string) string    { return "bkm-" + id }
func otpSecretName(id string) string { return "bkf-" + id }
func repoSecret(id string) string    { return "bkr-" + id }

func (s *Service) catalogPath() string {
	return filepath.Join(s.DataDir, "backup-catalog.json")
}

func (s *Service) loadCatalog() (catalogFile, error) {
	var c catalogFile
	raw, err := os.ReadFile(s.catalogPath())
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if len(raw) == 0 {
		return c, nil
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, err
	}
	return c, nil
}

func (s *Service) saveCatalog(c catalogFile) error {
	if err := os.MkdirAll(s.DataDir, 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	tmp, err := os.CreateTemp(s.DataDir, ".catalog-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.catalogPath())
}

func (s *Service) withCatalog(fn func(*catalogFile) error) error {
	s.catMu.Lock()
	defer s.catMu.Unlock()
	c, err := s.loadCatalog()
	if err != nil {
		return op("Could not read the backup list.")
	}
	if err := fn(&c); err != nil {
		return err
	}
	if err := s.saveCatalog(c); err != nil {
		return op("Could not save the backup list.")
	}
	return nil
}

func (s *Service) readCatalog() (catalogFile, error) {
	s.catMu.Lock()
	defer s.catMu.Unlock()
	c, err := s.loadCatalog()
	if err != nil {
		return c, op("Could not read the backup list.")
	}
	return c, nil
}
