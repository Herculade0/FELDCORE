package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Herculade0/feldcore/internal/events"
	"github.com/Herculade0/feldcore/internal/secrets"
	"github.com/Herculade0/feldcore/internal/store"
)

const (
	secretPassword = "drive-password"
	secretMailbox  = "drive-mailbox"
	secretTwoFA    = "drive-2fa"
	remoteName     = "feldcore"
)

// Box stores the Proton account secrets.
type Box interface {
	Put(name, value string) error
	Get(name string) (string, bool, error)
}

// Exec runs a command. extraEnv is appended and must not be logged.
type Exec interface {
	LookPath(name string) (string, error)
	Run(ctx context.Context, argv []string, extraEnv []string) (string, error)
}

// Service backs up to Proton Drive.
type Service struct {
	Store      *store.Store
	Secrets    Box
	DataDir    string
	SharesRoot string
	Log        *slog.Logger
	Now        func() time.Time
	RunExec    Exec
	Exists     func(string) bool

	// Events receives backup, check, and restore failures. Nil publishes on
	// the process bus after the daemon has called events.SetDefault.
	Events *events.Bus
	// Audit records state-changing actions. The HTTP layer sets it.
	Audit func(actor, tokenID, action, target, result string)
	// Root cancels a running job when the daemon stops.
	Root context.Context
	// Engine runs restic. Nil uses the restic binary on this machine.
	Engine Engine
	// Cmd runs virsh, qemu-img and docker. Nil uses the host PATH.
	Cmd Commander
	// Mounts attaches an SMB or NFS destination. Nil uses mount(8).
	Mounts Mounter
	// Compose lists extra stack directories. Nil asks Docker, then the usual folders.
	Compose func(context.Context) []string

	mu   sync.Mutex
	busy bool

	jobMu sync.Mutex
	jobM  map[string]*bjob
	jobQ  []string
	catMu sync.Mutex
}

// Settings is what the page edits. Passwords are write-only.
type Settings struct {
	Enabled          bool   `json:"enabled"`
	RemotePath       string `json:"remotePath"`
	Schedule         string `json:"schedule"`
	Retention        int    `json:"retention"`
	IncludeSnapshots bool   `json:"includeSnapshots"`
	IncludeAppdata   bool   `json:"includeAppdata"`
	Username         string `json:"username"`
	Password         string `json:"password,omitempty"`
	Mailbox          string `json:"mailbox,omitempty"`
	TwoFA            string `json:"twoFa,omitempty"`
	ClearMailbox     bool   `json:"clearMailbox,omitempty"`
	ClearTwoFA       bool   `json:"clearTwoFa,omitempty"`
}

// View is the page's read model. It has no secrets.
type View struct {
	Enabled          bool      `json:"enabled"`
	Configured       bool      `json:"configured"`
	RemotePath       string    `json:"remotePath"`
	Schedule         string    `json:"schedule"`
	Retention        int       `json:"retention"`
	IncludeSnapshots bool      `json:"includeSnapshots"`
	IncludeAppdata   bool      `json:"includeAppdata"`
	Username         string    `json:"username"`
	HasPassword      bool      `json:"hasPassword"`
	HasMailbox       bool      `json:"hasMailbox"`
	HasTwoFA         bool      `json:"hasTwoFA"`
	LastRun          time.Time `json:"lastRun,omitempty"`
	LastOK           bool      `json:"lastOk"`
	LastError        string    `json:"lastError,omitempty"`
	LastName         string    `json:"lastName,omitempty"`
	Experimental     bool      `json:"experimental"`
	Note             string    `json:"note"`
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) exists(path string) bool {
	if s.Exists != nil {
		return s.Exists(path)
	}
	_, err := os.Stat(path)
	return err == nil
}

// View returns the saved settings.
func (s *Service) View() View {
	b := s.Store.Snapshot().Backup
	v := View{
		Enabled: b.Enabled, RemotePath: b.RemotePath, Schedule: b.Schedule,
		Retention: b.Retention, IncludeSnapshots: b.IncludeSnapshots, IncludeAppdata: b.IncludeAppdata,
		Username: b.Username, HasPassword: b.HasPassword, HasMailbox: b.HasMailbox, HasTwoFA: b.HasTwoFA,
		LastRun: b.LastRun, LastOK: b.LastOK, LastError: b.LastError, LastName: b.LastName,
		Experimental: true,
		Note:         "Experimental. rclone's Proton Drive backend is unofficial and is not supported by Proton.",
	}
	v.Configured = b.Username != "" || b.HasPassword || b.Enabled
	if v.RemotePath == "" {
		v.RemotePath = "feldcore"
	}
	if v.Retention == 0 && !v.Configured {
		v.Retention = 7
	}
	if !v.Configured {
		v.IncludeSnapshots = true
	}
	return v
}

// Save stores settings. Empty password fields keep the stored secret.
func (s *Service) Save(in Settings) error {
	remote, err := CleanRemote(in.RemotePath)
	if err != nil {
		return err
	}
	if strings.TrimSpace(in.Schedule) != "" {
		if _, _, err := parseClock(in.Schedule); err != nil {
			return err
		}
	}
	if in.Retention < 0 || in.Retention > 100 {
		return input("Keep between 0 and 100 backups. Zero keeps every backup.")
	}
	user := strings.TrimSpace(in.Username)
	if in.Enabled && user == "" {
		return input("Enter the Proton account email before turning backups on.")
	}
	cur := s.Store.Snapshot().Backup
	if in.Enabled && !cur.HasPassword && strings.TrimSpace(in.Password) == "" {
		return input("Enter the Proton account password before turning backups on.")
	}
	if pw := in.Password; pw != "" {
		if err := s.Secrets.Put(secretPassword, pw); err != nil {
			return op("Could not store the Proton Drive password.")
		}
		cur.HasPassword = true
	}
	if in.ClearMailbox {
		_ = s.Secrets.Put(secretMailbox, "")
		cur.HasMailbox = false
	} else if m := in.Mailbox; m != "" {
		if err := s.Secrets.Put(secretMailbox, m); err != nil {
			return op("Could not store the mailbox password.")
		}
		cur.HasMailbox = true
	}
	if in.ClearTwoFA {
		_ = s.Secrets.Put(secretTwoFA, "")
		cur.HasTwoFA = false
	} else if c := in.TwoFA; c != "" {
		if err := s.Secrets.Put(secretTwoFA, c); err != nil {
			return op("Could not store the 2FA code.")
		}
		cur.HasTwoFA = true
	}
	cur.Enabled = in.Enabled
	cur.RemotePath = remote
	cur.Schedule = strings.TrimSpace(in.Schedule)
	cur.Retention = in.Retention
	cur.IncludeSnapshots = in.IncludeSnapshots
	cur.IncludeAppdata = in.IncludeAppdata
	cur.Username = user
	return s.Store.Update(func(st *store.State) error {
		prev := st.Backup
		cur.LastRun, cur.LastOK, cur.LastError, cur.LastName = prev.LastRun, prev.LastOK, prev.LastError, prev.LastName
		st.Backup = cur
		return nil
	})
}

// Test asks rclone to list the Drive folder.
func (s *Service) Test(ctx context.Context) (string, error) {
	if !s.Store.Snapshot().Backup.Enabled {
		return "", input("Turn Proton Drive backups on before testing the connection.")
	}
	out, err := s.rclone(ctx, []string{"lsd", remoteName + ":" + s.remote()})
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(out) == "" {
		return "Connected. The folder is empty.", nil
	}
	return "Connected to Proton Drive.", nil
}

// Run builds an archive and uploads it.
func (s *Service) Run(ctx context.Context) (string, error) {
	if !s.Store.Snapshot().Backup.Enabled {
		return "", input("Turn Proton Drive backups on before running a backup.")
	}
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return "", input("A backup is already running.")
	}
	s.busy = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.busy = false
		s.mu.Unlock()
	}()

	name := ArchiveName(s.now())
	dir := filepath.Join(s.DataDir, "backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", op("Could not create the local backup folder.")
	}
	dest := filepath.Join(dir, name)
	sources := s.sources()
	if err := writeArchive(dest, sources); err != nil {
		return "", op("Could not build the backup archive.")
	}
	defer os.Remove(dest)

	remote := s.remote()
	if _, err := s.rclone(ctx, []string{"copyto", dest, remoteFile(remote, name)}); err != nil {
		s.record(false, name, err.Error())
		return "", err
	}
	if err := s.pruneRemote(ctx, remote); err != nil {
		s.record(true, name, "")
		return name, err
	}
	s.record(true, name, "")
	if s.Log != nil {
		s.Log.Info("proton drive backup uploaded", "name", name)
	}
	msg := "Uploaded " + name + "."
	if notes := Notes(sources); len(notes) > 0 {
		msg += " " + strings.Join(notes, " ")
	}
	return msg, nil
}

// Loop runs the daily schedule until ctx is cancelled.
func (s *Service) Loop(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			b := s.Store.Snapshot().Backup
			if !b.Enabled {
				continue
			}
			due, err := Due(b.Schedule, b.LastRun, now)
			if err != nil || !due {
				continue
			}
			if _, err := s.Run(ctx); err != nil && s.Log != nil {
				s.Log.Warn("scheduled backup failed", "err", err)
			}
		}
	}
}

func (s *Service) sources() []Source {
	snap := s.Store.Snapshot()
	b := snap.Backup
	app := AppdataPath(snap.Appdata, s.SharesRoot)
	out := SelectSources(s.DataDir, app, b.IncludeSnapshots, b.IncludeAppdata, s.exists)
	if !b.IncludeSnapshots {
		return out
	}
	// Container snapshots live under the shares root when that feature is
	// installed, and under the data directory otherwise. Include whichever
	// of those folders is actually there.
	extra := snap.Snapshots.Dir
	if extra == "" && s.SharesRoot != "" {
		extra = filepath.Join(s.SharesRoot, "system", "snapshots")
	}
	dataSnap := filepath.Join(s.DataDir, "snapshots")
	if extra != "" && extra != dataSnap && s.exists(extra) {
		out = append(out, Source{Path: extra, Name: "system-snapshots"})
	}
	return out
}

func (s *Service) remote() string {
	r, err := CleanRemote(s.Store.Snapshot().Backup.RemotePath)
	if err != nil {
		return "feldcore"
	}
	return r
}

func (s *Service) record(ok bool, name, errMsg string) {
	_ = s.Store.Update(func(st *store.State) error {
		st.Backup.LastRun = s.now()
		st.Backup.LastOK = ok
		st.Backup.LastName = name
		st.Backup.LastError = errMsg
		return nil
	})
}

func (s *Service) pruneRemote(ctx context.Context, remote string) error {
	keep := s.Store.Snapshot().Backup.Retention
	out, err := s.rclone(ctx, []string{"lsf", remoteName + ":" + remote})
	if err != nil {
		return err
	}
	var names []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			names = append(names, line)
		}
	}
	for _, old := range Prune(names, keep) {
		if _, err := s.rclone(ctx, []string{"deletefile", remoteFile(remote, old)}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) rclone(ctx context.Context, args []string) (string, error) {
	conf, hidden, err := s.writeConfig()
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(filepath.Dir(conf))
	ex := s.RunExec
	if ex == nil {
		ex = localExec{}
	}
	argv := append([]string{"rclone", "--config", conf}, args...)
	if path, err := ex.LookPath("rclone"); err == nil && path != "" {
		argv[0] = path
		out, runErr := ex.Run(ctx, argv, nil)
		return scrub(out, runErr, hidden)
	}
	// Fall back to the official rclone image so the host does not need the binary.
	dockerArgs := []string{
		"docker", "run", "--rm",
		"-v", conf + ":/config/rclone.conf:ro",
		"-v", filepath.Join(s.DataDir, "backups") + ":/backups:ro",
		"rclone/rclone:latest",
		"--config", "/config/rclone.conf",
	}
	// copyto's local path has to be the path inside the container.
	mapped := mapRcloneArgs(args, filepath.Join(s.DataDir, "backups"), "/backups")
	dockerArgs = append(dockerArgs, mapped...)
	if _, err := ex.LookPath("docker"); err != nil {
		return "", op("Install rclone, or start Docker, so FELDCORE can reach Proton Drive.")
	}
	out, err := ex.Run(ctx, dockerArgs, nil)
	text, scrubbed := scrub(out, err, hidden)
	return text, scrubbed
}

func scrub(out string, err error, hidden []string) (string, error) {
	out = secrets.Redact(out, hidden...)
	if err == nil {
		return out, nil
	}
	return out, op(secrets.Redact(err.Error(), hidden...))
}

func mapRcloneArgs(args []string, hostDir, mount string) []string {
	out := append([]string{}, args...)
	for i, a := range out {
		if strings.HasPrefix(a, hostDir+"/") || a == hostDir {
			out[i] = mount + strings.TrimPrefix(a, hostDir)
		}
	}
	return out
}

func (s *Service) writeConfig() (string, []string, error) {
	b := s.Store.Snapshot().Backup
	if strings.TrimSpace(b.Username) == "" || !b.HasPassword {
		return "", nil, input("Enter the Proton account email and password first.")
	}
	pass, ok, err := s.Secrets.Get(secretPassword)
	if err != nil || !ok || pass == "" {
		return "", nil, input("The Proton Drive password could not be read.")
	}
	mailbox, _, _ := s.Secrets.Get(secretMailbox)
	twofa, _, _ := s.Secrets.Get(secretTwoFA)
	obscured, err := mustObscure(pass)
	if err != nil {
		return "", nil, op("Could not prepare the Proton Drive password.")
	}
	var boxMail string
	if mailbox != "" {
		boxMail, err = mustObscure(mailbox)
		if err != nil {
			return "", nil, op("Could not prepare the mailbox password.")
		}
	}
	dir, err := os.MkdirTemp(s.DataDir, ".rclone-")
	if err != nil {
		return "", nil, op("Could not prepare the rclone configuration.")
	}
	var buf strings.Builder
	fmt.Fprintf(&buf, "[%s]\ntype = protondrive\nusername = %s\npassword = %s\n", remoteName, b.Username, obscured)
	if boxMail != "" {
		fmt.Fprintf(&buf, "mailbox_password = %s\n", boxMail)
	}
	if twofa != "" {
		if otpSecret.MatchString(strings.ToUpper(strings.ReplaceAll(twofa, " ", ""))) {
			fmt.Fprintf(&buf, "otp_secret_key = %s\n", strings.ToUpper(strings.ReplaceAll(twofa, " ", "")))
		} else {
			fmt.Fprintf(&buf, "2fa = %s\n", twofa)
		}
	}
	path := filepath.Join(dir, "rclone.conf")
	if err := os.WriteFile(path, []byte(buf.String()), 0o600); err != nil {
		os.RemoveAll(dir)
		return "", nil, op("Could not write the rclone configuration.")
	}
	hidden := []string{pass}
	if len(mailbox) >= 8 {
		hidden = append(hidden, mailbox)
	}
	if len(twofa) >= 8 {
		hidden = append(hidden, twofa)
	}
	return path, hidden, nil
}

var otpSecret = regexp.MustCompile(`^[A-Z2-7]{16,}$`)

func writeArchive(dest string, sources []Source) error {
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, src := range sources {
		if src.Path == "" {
			continue
		}
		if err := addPath(tw, src.Path, src.Name); err != nil {
			tw.Close()
			gz.Close()
			return err
		}
	}
	if err := tw.Close(); err != nil {
		gz.Close()
		return err
	}
	return gz.Close()
}

func addPath(tw *tar.Writer, src, name string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	if info.IsDir() {
		return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.Type()&os.ModeSymlink != 0 {
				return nil
			}
			rel, err := filepath.Rel(src, path)
			if err != nil {
				return err
			}
			entry := name
			if rel != "." {
				entry = filepath.ToSlash(filepath.Join(name, rel))
			}
			if path == src && d.IsDir() {
				return nil
			}
			return addFile(tw, path, entry, d)
		})
	}
	return addFile(tw, src, name, nil)
}

func addFile(tw *tar.Writer, path, name string, d fs.DirEntry) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() && !info.IsDir() {
		return nil
	}
	hdr, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	hdr.Name = name
	if info.IsDir() && !strings.HasSuffix(hdr.Name, "/") {
		hdr.Name += "/"
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	if info.IsDir() {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(tw, f)
	return err
}

type localExec struct{}

func (localExec) LookPath(name string) (string, error) { return exec.LookPath(name) }

func (localExec) Run(ctx context.Context, argv []string, extraEnv []string) (string, error) {
	if len(argv) == 0 {
		return "", fmt.Errorf("empty command")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), extraEnv...)
	out, err := cmd.CombinedOutput()
	text := string(out)
	if err != nil {
		if text != "" {
			return text, fmt.Errorf("%s", strings.TrimSpace(text))
		}
		return "", err
	}
	return text, nil
}
