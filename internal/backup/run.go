package backup

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Herculade0/feldcore/internal/events"
)

func (s *Service) runBackup(ctx context.Context, j *bjob) (err error) {
	dest, ok := s.findDest(j.DestinationID)
	if !ok {
		return input("That backup destination is gone.")
	}
	if err = s.ready(dest); err != nil {
		return err
	}
	repo, release, err := s.openRepo(ctx, dest, j)
	if err != nil {
		return err
	}
	defer release()
	s.step(j, "Opening the repository")
	if err = s.engine().Ensure(ctx, repo); err != nil {
		return err
	}
	s.step(j, "Choosing files")
	plan, err := s.buildPlan(ctx, dest, j.ID)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := plan.Cleanup(context.Background()); cerr != nil {
			s.logLine(j, cerr.Error())
			if err == nil {
				err = cerr
			}
		}
	}()
	for _, note := range plan.Notes {
		s.logLine(j, note)
	}
	plan.Excludes = append(plan.Excludes, repoExclude(repo.Location))
	s.step(j, "Copying")
	tags := []string{"feldcore", "dest:" + dest.ID}
	tags = append(tags, prefixTags("set:", dest.Sets.names())...)
	if plan.IAHead != "" {
		head := plan.IAHead
		if len(head) > 16 {
			head = head[:16]
		}
		tags = append(tags, "ia:"+head)
	}
	sum, err := s.engine().Backup(ctx, repo, BackupReq{Paths: plan.Paths, Excludes: plan.Excludes, Tags: tags}, func(p Progress) {
		s.progress(j, p)
	})
	if err != nil {
		return err
	}
	s.jobMu.Lock()
	j.SnapshotID = sum.SnapshotID
	j.iaHead = plan.IAHead
	s.jobMu.Unlock()
	s.step(j, "Applying retention")
	if err = s.engine().Forget(ctx, repo, dest.ID, Retention{Daily: dest.KeepDaily, Weekly: dest.KeepWeekly, Monthly: dest.KeepMonthly}); err != nil {
		return err
	}
	if plan.IAHead != "" {
		s.logLine(j, "Internal Affairs ledger head "+shortHead(plan.IAHead)+" is in this copy.")
	}
	s.logLine(j, "Snapshot "+shortSnap(sum.SnapshotID)+" is stored.")
	return nil
}

func (s *Service) runCheck(ctx context.Context, j *bjob) error {
	dest, ok := s.findDest(j.DestinationID)
	if !ok {
		return input("That backup destination is gone.")
	}
	repo, release, err := s.openRepo(ctx, dest, j)
	if err != nil {
		return err
	}
	defer release()
	s.step(j, "Checking the repository")
	if j.readData {
		s.logLine(j, "Reading a sample of the stored data.")
	}
	if err := s.engine().Check(ctx, repo, j.readData, func(p Progress) { s.progress(j, p) }); err != nil {
		return err
	}
	s.logLine(j, "The repository is consistent.")
	return nil
}

func (s *Service) runRestore(ctx context.Context, j *bjob) error {
	dest, ok := s.findDest(j.DestinationID)
	if !ok {
		return input("That backup destination is gone.")
	}
	repo, release, err := s.openRepo(ctx, dest, j)
	if err != nil {
		return err
	}
	defer release()
	target := j.Detail
	if j.mode != "replace" {
		target = filepath.Join(s.DataDir, "restores", j.ID)
		if err := os.MkdirAll(target, 0o700); err != nil {
			return op("Could not prepare the restore folder.")
		}
	}
	s.step(j, "Restoring")
	if err := s.engine().Restore(ctx, repo, j.snap, j.paths, target, func(p Progress) { s.progress(j, p) }); err != nil {
		return err
	}
	s.jobMu.Lock()
	j.Detail = target
	s.jobMu.Unlock()
	if j.mode == "replace" {
		s.logLine(j, "Restored onto the original paths.")
	} else {
		s.logLine(j, "Restored into "+target+".")
	}
	return nil
}

func (s *Service) openRepo(ctx context.Context, dest Destination, j *bjob) (repo Repo, release func(), err error) {
	release = func() {}
	pass, err := s.repoPassword(dest.ID)
	if err != nil {
		return Repo{}, release, err
	}
	if j != nil {
		j.redact = append(j.redact, pass)
	}
	repo.Password = pass
	switch dest.Kind {
	case "local":
		root, err := s.localRoot(dest)
		if err != nil {
			return Repo{}, release, err
		}
		dir := repoFolder(root)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return Repo{}, release, op("Could not create the backup folder.")
		}
		repo.Location = dir
		return repo, release, nil
	case "smb", "nfs":
		mp := filepath.Join(s.DataDir, "backup-mounts", dest.ID)
		spec, err := s.mountSpec(dest, mp)
		if err != nil {
			return Repo{}, release, err
		}
		if j != nil && spec.Password != "" {
			j.redact = append(j.redact, spec.Password)
		}
		if err := os.MkdirAll(filepath.Dir(mp), 0o700); err != nil {
			return Repo{}, release, op("Could not prepare the network backup folder.")
		}
		if err := s.mounter().Mount(ctx, spec); err != nil {
			return Repo{}, release, err
		}
		release = func() { _ = s.mounter().Unmount(context.Background(), mp) }
		dir := filepath.Join(mp, repoDirName)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			release()
			return Repo{}, func() {}, op("Could not create the backup folder on the share.")
		}
		repo.Location = dir
		return repo, release, nil
	case "sftp":
		if s.Secrets == nil {
			return Repo{}, release, op("Secrets are not available, so the SFTP key could not be read.")
		}
		key, _, err := s.Secrets.Get(keySecret(dest.ID))
		if err != nil || key == "" {
			return Repo{}, release, input("The SFTP private key could not be read.")
		}
		if j != nil {
			j.redact = append(j.redact, key)
		}
		dir := filepath.Join(s.DataDir, "backup-ssh", dest.ID)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return Repo{}, release, op("Could not prepare the SFTP key.")
		}
		keyFile := filepath.Join(dir, "id_key")
		known := filepath.Join(dir, "known_hosts")
		if err := os.WriteFile(keyFile, []byte(key), 0o600); err != nil {
			return Repo{}, release, op("Could not write the SFTP key.")
		}
		port := dest.Port
		if port == 0 {
			port = 22
		}
		repo.Location = fmt.Sprintf("sftp://%s@%s:%d%s", dest.Username, dest.Host, port, remoteRepo(dest.Path))
		repo.SFTPArgs = "-i " + shellQuote(keyFile) + " -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=" + shellQuote(known) + " -o BatchMode=yes -o IdentitiesOnly=yes"
		return repo, release, nil
	case "proton":
		user, pass, mailbox, twofa, err := s.protonCreds(dest)
		if err != nil {
			return Repo{}, release, err
		}
		conf, hidden, err := writeProtonFile(s.DataDir, user, pass, mailbox, twofa)
		if err != nil {
			return Repo{}, release, err
		}
		if j != nil {
			j.redact = append(j.redact, hidden...)
		}
		repo.Hidden = hidden
		repo.RcloneConfig = conf
		repo.Location = "rclone:" + remoteName + ":" + remoteRepo(dest.Path)
		release = func() { os.RemoveAll(filepath.Dir(conf)) }
		return repo, release, nil
	default:
		return Repo{}, release, input("That destination is not supported.")
	}
}

func (s *Service) mountSpec(dest Destination, mp string) (MountSpec, error) {
	spec := MountSpec{Kind: dest.Kind, Mountpoint: mp, Username: dest.Username, Port: dest.Port}
	if dest.HasPassword && s.Secrets != nil {
		pw, _, err := s.Secrets.Get(passSecret(dest.ID))
		if err != nil {
			return spec, op("Could not read the share password.")
		}
		spec.Password = pw
	}
	switch dest.Kind {
	case "smb":
		spec.Source = "//" + dest.Host + "/" + dest.Path
	case "nfs":
		spec.Source = dest.Host + ":" + dest.Path
	default:
		return spec, input("That destination cannot be attached.")
	}
	return spec, nil
}

func (s *Service) localRoot(dest Destination) (string, error) {
	if dest.Share != "" {
		root := s.SharesRoot
		if strings.TrimSpace(root) == "" {
			root = "/srv/feldcore"
		}
		p := filepath.Join(root, dest.Share)
		if dest.Path != "" {
			p = filepath.Join(p, filepath.FromSlash(dest.Path))
		}
		return p, nil
	}
	if !filepath.IsAbs(dest.Path) {
		return "", input("Use an absolute path, or choose a share.")
	}
	return dest.Path, nil
}

func (s *Service) repoPassword(id string) (string, error) {
	if s.Secrets == nil {
		return "", op("Secrets are not available, so the repository password could not be read.")
	}
	pw, ok, err := s.Secrets.Get(repoSecret(id))
	if err != nil {
		return "", op("Could not read the repository password.")
	}
	if ok && pw != "" {
		return pw, nil
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", op("Could not create the repository password.")
	}
	pw = base64.RawURLEncoding.EncodeToString(raw)
	if err := s.Secrets.Put(repoSecret(id), pw); err != nil {
		return "", op("Could not store the repository password.")
	}
	return pw, nil
}

func (s *Service) protonCreds(dest Destination) (user, pass, mailbox, twofa string, err error) {
	if s.Secrets == nil {
		return "", "", "", "", op("Secrets are not available.")
	}
	if dest.UseSavedProton {
		if s.Store == nil {
			return "", "", "", "", input("The Proton account saved in Settings could not be read.")
		}
		b := s.Store.Snapshot().Backup
		user = strings.TrimSpace(b.Username)
		var ok bool
		pass, ok, err = s.Secrets.Get(secretPassword)
		if err != nil || !ok || pass == "" || user == "" {
			return "", "", "", "", input("Save the Proton account on the Settings page, or enter it here.")
		}
		mailbox, _, _ = s.Secrets.Get(secretMailbox)
		twofa, _, _ = s.Secrets.Get(secretTwoFA)
		return user, pass, mailbox, twofa, nil
	}
	user = dest.Username
	var ok bool
	pass, ok, err = s.Secrets.Get(passSecret(dest.ID))
	if err != nil || !ok || pass == "" || user == "" {
		return "", "", "", "", input("Enter the Proton email and password.")
	}
	mailbox, _, _ = s.Secrets.Get(mailSecret(dest.ID))
	twofa, _, _ = s.Secrets.Get(otpSecretName(dest.ID))
	return user, pass, mailbox, twofa, nil
}

func writeProtonFile(dataDir, user, pass, mailbox, twofa string) (string, []string, error) {
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
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return "", nil, op("Could not prepare the rclone configuration.")
	}
	dir, err := os.MkdirTemp(dataDir, ".rclone-")
	if err != nil {
		return "", nil, op("Could not prepare the rclone configuration.")
	}
	var buf strings.Builder
	fmt.Fprintf(&buf, "[%s]\ntype = protondrive\nusername = %s\npassword = %s\n", remoteName, user, obscured)
	if boxMail != "" {
		fmt.Fprintf(&buf, "mailbox_password = %s\n", boxMail)
	}
	if twofa != "" {
		compact := strings.ToUpper(strings.ReplaceAll(twofa, " ", ""))
		if otpSecret.MatchString(compact) {
			fmt.Fprintf(&buf, "otp_secret_key = %s\n", compact)
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

func (s *Service) recordBackup(id, snap, head, errMsg string) {
	ok := errMsg == ""
	_ = s.withCatalog(func(c *catalogFile) error {
		for i := range c.Destinations {
			if c.Destinations[i].ID != id {
				continue
			}
			c.Destinations[i].LastRun = s.now()
			c.Destinations[i].LastOK = ok
			c.Destinations[i].LastError = errMsg
			if snap != "" {
				c.Destinations[i].LastSnapshot = snap
			}
			break
		}
		if ok && head != "" && snap != "" {
			c.IAHeads = append(c.IAHeads, iaRecord{
				At: s.now(), DestinationID: id, SnapshotID: snap, Head: head,
			})
			if len(c.IAHeads) > 40 {
				c.IAHeads = c.IAHeads[len(c.IAHeads)-40:]
			}
		}
		return nil
	})
}

func (s *Service) recordFailure(id, msg string) {
	s.recordBackup(id, "", "", msg)
}

func repoExclude(location string) string {
	if strings.HasPrefix(location, "/") {
		return location
	}
	return ""
}

func prefixTags(prefix string, names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, prefix+n)
	}
	return out
}

func shortHead(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

func shortSnap(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	if id == "" {
		return "the copy"
	}
	return id
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func (s *Service) emitFailed(_ context.Context, j *bjob, err error) {
	if j == nil || err == nil {
		return
	}
	name := "backup.failed"
	title := "Backup failed"
	switch j.Kind {
	case "check":
		name = "backup.check_failed"
		title = "Backup check failed"
	case "restore":
		name = "backup.restore_failed"
		title = "Restore failed"
	}
	if j.Destination != "" {
		title += ": " + j.Destination
	}
	key := name
	if j.DestinationID != "" {
		key = name + ":" + j.DestinationID
	}
	ev := events.Event{
		Name: name, Severity: events.Critical, Title: title,
		Body:   strings.TrimSpace(j.Destination + " failed. " + clipErr(err.Error())),
		Source: "backup", Key: key,
		Fields: map[string]string{"destination": j.DestinationID, "job": j.ID, "kind": j.Kind},
	}
	if s.Events != nil {
		s.Events.Publish(ev)
		return
	}
	events.Publish(ev)
}

func (s *Service) findDest(id string) (Destination, bool) {
	c, err := s.readCatalog()
	if err != nil {
		return Destination{}, false
	}
	for _, d := range c.Destinations {
		if d.ID == id {
			return d, true
		}
	}
	return Destination{}, false
}
