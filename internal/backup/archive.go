package backup

import (
	"context"
	"sort"
	"strings"
	"time"
)

// Overview is the Backups page.
type Overview struct {
	Engine          string        `json:"engine"`
	Why             string        `json:"why"`
	Installed       bool          `json:"installed"`
	InstallHint     string        `json:"installHint,omitempty"`
	Sets            []SetInfo     `json:"sets"`
	Destinations    []Destination `json:"destinations"`
	Jobs            []Job         `json:"jobs"`
	InternalAffairs IAStatus      `json:"internalAffairs"`
	ProtonSaved     bool          `json:"protonSaved"`
	ProtonUser      string        `json:"protonUser,omitempty"`
}

// SetInfo describes one thing a destination can include.
type SetInfo struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Detail  string `json:"detail"`
	Present bool   `json:"present"`
}

// IAStatus is how the cabinet sits in the backup set.
type IAStatus struct {
	Path           string `json:"path"`
	Present        bool   `json:"present"`
	InDefaultSet   bool   `json:"inDefaultSet"`
	LedgerHead     string `json:"ledgerHead,omitempty"`
	LastBackedHead string `json:"lastBackedHead,omitempty"`
	LastSnapshot   string `json:"lastSnapshot,omitempty"`
	Note           string `json:"note"`
}

// RestoreInput asks for a full copy or one file or folder.
type RestoreInput struct {
	DestinationID string   `json:"destinationId"`
	SnapshotID    string   `json:"snapshotId"`
	Paths         []string `json:"paths"`
	Mode          string   `json:"mode"`
	Confirm       string   `json:"confirm"`
}

func (s *Service) Overview(ctx context.Context) Overview {
	installed := s.engine().Available() == nil
	hint := ""
	if !installed {
		hint = "Install restic to take versioned backups. The button on this page uses the same install job as the other packages."
	}
	c, _ := s.readCatalog()
	dests := c.Destinations
	if dests == nil {
		dests = []Destination{}
	}
	ia := s.iaStatus(c)
	protonUser := ""
	protonSaved := false
	if s.Store != nil {
		b := s.Store.Snapshot().Backup
		protonSaved = b.HasPassword && strings.TrimSpace(b.Username) != ""
		protonUser = b.Username
	}
	return Overview{
		Engine: "restic", Why: WhyRestic, Installed: installed, InstallHint: hint,
		Sets: s.setInfo(), Destinations: dests, Jobs: s.Jobs(),
		InternalAffairs: ia, ProtonSaved: protonSaved, ProtonUser: protonUser,
	}
}

func (s *Service) setInfo() []SetInfo {
	return []SetInfo{
		{Key: "config", Label: "FELDCORE", Detail: "state.json, the backup list, and the encrypted secrets, including the key that reads them.", Present: pathExists(filepathJoin(s.DataDir, "state.json")) || dirExists(filepathJoin(s.DataDir, "secrets"))},
		{Key: "appdata", Label: "Appdata", Detail: "Container configuration and data.", Present: dirExists(s.appdataDir())},
		{Key: "compose", Label: "Compose stacks", Detail: "Folders named compose or stacks, plus any stack Docker reports when a backup runs.", Present: s.composePresent()},
		{Key: "vms", Label: "Virtual machines", Detail: "Definitions, and disks via a snapshot or a short pause when libvirt is installed.", Present: dirExists(s.domainsDir())},
		{Key: "internalAffairs", Label: "Internal Affairs", Detail: "The case cabinet, including the ledger. .obsidian is left out.", Present: dirExists(s.iaDir())},
		{Key: "snapshots", Label: "Container snapshots", Detail: "Snapshot archives, when that folder exists.", Present: len(s.snapshotDirs()) > 0 && anyDir(s.snapshotDirs())},
	}
}

func (s *Service) iaStatus(c catalogFile) IAStatus {
	path := s.iaDir()
	st := IAStatus{
		Path: path, Present: dirExists(path), InDefaultSet: true,
		Note: "Reports are not deleted from Internal Affairs. How old a report must be before it can be pruned is not decided. A finished backup records the ledger head, so a later prune can require a backed-up copy.",
	}
	if st.Present {
		st.LedgerHead = ledgerHead(path)
	}
	for i := len(c.IAHeads) - 1; i >= 0; i-- {
		if c.IAHeads[i].Head != "" {
			st.LastBackedHead = c.IAHeads[i].Head
			st.LastSnapshot = c.IAHeads[i].SnapshotID
			break
		}
	}
	if !st.Present {
		st.Note = "Internal Affairs is not set up yet. When the cabinet exists it is included in the default backup set. " + st.Note
	}
	return st
}

func (s *Service) SaveDestination(in DestinationInput, actor, tokenID string) (Destination, error) {
	var prev *Destination
	if id := strings.TrimSpace(inID(in)); id != "" {
		d, ok := s.findDest(id)
		if !ok {
			return Destination{}, input("That backup destination is gone.")
		}
		prev = &d
	}
	d, err := s.normalize(in, prev)
	if err != nil {
		return Destination{}, err
	}
	err = s.withCatalog(func(c *catalogFile) error {
		if prev == nil {
			c.Destinations = append(c.Destinations, d)
			return nil
		}
		for i := range c.Destinations {
			if c.Destinations[i].ID == d.ID {
				c.Destinations[i] = d
				return nil
			}
		}
		c.Destinations = append(c.Destinations, d)
		return nil
	})
	if err != nil {
		return Destination{}, err
	}
	s.audit(actor, tokenID, "backup destination saved", d.Name, "ok")
	return d, nil
}

// UpdateDestination saves an existing destination. id comes from the URL.
func (s *Service) UpdateDestination(id string, in DestinationInput, actor, tokenID string) (Destination, error) {
	if !idRe.MatchString(id) {
		return Destination{}, input("That backup destination is gone.")
	}
	in.Name = strings.TrimSpace(in.Name)
	// The id is not on the form body. Stash it by looking up first inside normalize via prev.
	d, ok := s.findDest(id)
	if !ok {
		return Destination{}, input("That backup destination is gone.")
	}
	next, err := s.normalize(in, &d)
	if err != nil {
		return Destination{}, err
	}
	if err := s.withCatalog(func(c *catalogFile) error {
		for i := range c.Destinations {
			if c.Destinations[i].ID == id {
				c.Destinations[i] = next
				return nil
			}
		}
		return input("That backup destination is gone.")
	}); err != nil {
		return Destination{}, err
	}
	s.audit(actor, tokenID, "backup destination saved", next.Name, "ok")
	return next, nil
}

func (s *Service) DeleteDestination(id, actor, tokenID string) error {
	if !idRe.MatchString(id) {
		return input("That backup destination is gone.")
	}
	var name string
	err := s.withCatalog(func(c *catalogFile) error {
		next := c.Destinations[:0]
		found := false
		for _, d := range c.Destinations {
			if d.ID == id {
				found = true
				name = d.Name
				continue
			}
			next = append(next, d)
		}
		if !found {
			return input("That backup destination is gone.")
		}
		c.Destinations = next
		return nil
	})
	if err != nil {
		return err
	}
	if s.Secrets != nil {
		_ = s.Secrets.Put(passSecret(id), "")
		_ = s.Secrets.Put(keySecret(id), "")
		_ = s.Secrets.Put(mailSecret(id), "")
		_ = s.Secrets.Put(otpSecretName(id), "")
		_ = s.Secrets.Put(repoSecret(id), "")
	}
	s.audit(actor, tokenID, "backup destination deleted", name, "ok")
	return nil
}

func (s *Service) TestDestination(ctx context.Context, id, actor, tokenID string) (string, error) {
	dest, ok := s.findDest(id)
	if !ok {
		return "", input("That backup destination is gone.")
	}
	if err := s.ready(dest); err != nil {
		return "", err
	}
	repo, release, err := s.openRepo(ctx, dest, nil)
	if err != nil {
		return "", err
	}
	defer release()
	if err := s.engine().Probe(ctx, repo); err != nil {
		s.audit(actor, tokenID, "backup destination tested", dest.Name, "error")
		return "", err
	}
	s.audit(actor, tokenID, "backup destination tested", dest.Name, "ok")
	return "Reached " + dest.Name + ". The repository will be created on the first backup if it is not there yet.", nil
}

func (s *Service) RevealPassword(id, actor, tokenID string) (string, error) {
	if !idRe.MatchString(id) {
		return "", input("That backup destination is gone.")
	}
	if _, ok := s.findDest(id); !ok {
		return "", input("That backup destination is gone.")
	}
	if s.Secrets == nil {
		return "", op("Secrets are not available.")
	}
	pw, ok, err := s.Secrets.Get(repoSecret(id))
	if err != nil || !ok || pw == "" {
		return "", input("This destination has not created a repository password yet. Run a backup first.")
	}
	s.audit(actor, tokenID, "backup repository password revealed", id, "ok")
	return pw, nil
}

func (s *Service) StartBackup(id, actor, tokenID string) (Job, error) {
	return s.launchKind(id, "backup", actor, tokenID, func(j *bjob) func(context.Context) error {
		return func(ctx context.Context) error { return s.runBackup(ctx, j) }
	})
}

func (s *Service) StartCheck(id string, readData bool, actor, tokenID string) (Job, error) {
	return s.launchKind(id, "check", actor, tokenID, func(j *bjob) func(context.Context) error {
		j.readData = readData
		return func(ctx context.Context) error { return s.runCheck(ctx, j) }
	})
}

func (s *Service) StartRestore(in RestoreInput, actor, tokenID string) (Job, error) {
	if !idRe.MatchString(in.DestinationID) || !snapRe.MatchString(in.SnapshotID) {
		return Job{}, input("Choose a destination and a copy to restore.")
	}
	mode := strings.ToLower(strings.TrimSpace(in.Mode))
	if mode == "" {
		mode = "copy"
	}
	if mode != "copy" && mode != "replace" {
		return Job{}, input("Restore into a folder, or replace the copies on this server.")
	}
	if mode == "replace" && in.Confirm != "replace" {
		return Job{}, input("Type replace to put the files back where they came from.")
	}
	var paths []string
	for _, p := range in.Paths {
		c, err := cleanSnapPath(p)
		if err != nil {
			return Job{}, err
		}
		paths = append(paths, c)
	}
	return s.launchKind(in.DestinationID, "restore", actor, tokenID, func(j *bjob) func(context.Context) error {
		j.snap = strings.ToLower(in.SnapshotID)
		j.paths = paths
		j.mode = mode
		if mode == "replace" {
			j.Detail = "/"
		}
		return func(ctx context.Context) error { return s.runRestore(ctx, j) }
	})
}

func (s *Service) Snapshots(ctx context.Context, id string) ([]Snapshot, error) {
	dest, ok := s.findDest(id)
	if !ok {
		return nil, input("That backup destination is gone.")
	}
	repo, release, err := s.openRepo(ctx, dest, nil)
	if err != nil {
		return nil, err
	}
	defer release()
	list, err := s.engine().Snapshots(ctx, repo, dest.ID)
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []Snapshot{}
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].Time.After(list[j].Time) })
	return list, nil
}

func (s *Service) Browse(ctx context.Context, id, snap, dir string) (Browse, error) {
	if !snapRe.MatchString(snap) {
		return Browse{}, input("That copy was not recognised.")
	}
	dest, ok := s.findDest(id)
	if !ok {
		return Browse{}, input("That backup destination is gone.")
	}
	repo, release, err := s.openRepo(ctx, dest, nil)
	if err != nil {
		return Browse{}, err
	}
	defer release()
	nodes, err := s.engine().List(ctx, repo, snap)
	if err != nil {
		return Browse{}, err
	}
	view := listDir(nodes, dir)
	if len(nodes) >= 100000 {
		view.Truncated = true
	}
	return view, nil
}

// ArchiveLoop starts scheduled backups until ctx is cancelled.
func (s *Service) ArchiveLoop(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			s.scheduled(ctx, now)
		}
	}
}

func (s *Service) scheduled(ctx context.Context, now time.Time) {
	c, err := s.readCatalog()
	if err != nil {
		return
	}
	for _, d := range c.Destinations {
		if !d.Enabled {
			continue
		}
		due, err := scheduleDue(d.ScheduleMode, d.ScheduleTime, d.ScheduleDay, d.LastRun, now)
		if err != nil || !due {
			continue
		}
		if _, err := s.StartBackup(d.ID, "scheduler", ""); err != nil && s.Log != nil {
			s.Log.Warn("scheduled backup did not start", "dest", d.Name, "err", err)
		}
	}
}

func (s *Service) launchKind(id, kind, actor, tokenID string, prep func(*bjob) func(context.Context) error) (Job, error) {
	if !idRe.MatchString(id) {
		return Job{}, input("That backup destination is gone.")
	}
	dest, ok := s.findDest(id)
	if !ok {
		return Job{}, input("That backup destination is gone.")
	}
	s.jobMu.Lock()
	if existing, ok := s.runningLike(id, kind); ok {
		view := existing.view()
		s.jobMu.Unlock()
		return view, nil
	}
	if _, ok := s.runningLike(id, ""); ok {
		s.jobMu.Unlock()
		return Job{}, input("A job is already running for this destination.")
	}
	if actor == "" {
		actor = "admin"
	}
	j := &bjob{Job: Job{
		ID: idGen(), Kind: kind, DestinationID: dest.ID, Destination: dest.Name,
		State: jobQueued, Step: "Queued", CreatedAt: s.now(), Actor: actor, Log: []string{},
	}}
	j.tokenID = tokenID
	work := prep(j)
	s.jobsLocked()[j.ID] = j
	s.jobQ = append(s.jobQ, j.ID)
	s.trimJobs()
	view := j.view()
	s.jobMu.Unlock()
	go s.execute(j, work)
	return view, nil
}

func (s *Service) execute(j *bjob, work func(context.Context) error) {
	defer func() {
		if rec := recover(); rec != nil {
			s.finish(j, op("The backup stopped unexpectedly."))
		}
	}()
	s.jobMu.Lock()
	j.State = jobRunning
	j.StartedAt = s.now()
	if j.Step == "" || j.Step == "Queued" {
		j.Step = "Starting"
	}
	s.jobMu.Unlock()
	s.logLine(j, "Starting.")
	s.audit(j.Actor, j.tokenID, "backup "+j.Kind+" started", j.Destination, "ok")
	if s.Log != nil {
		s.Log.Info("backup job started", "kind", j.Kind, "dest", j.Destination, "id", j.ID)
	}
	ctx := s.Root
	if ctx == nil {
		ctx = context.Background()
	}
	s.finish(j, work(ctx))
}

func (s *Service) finish(j *bjob, err error) {
	s.jobMu.Lock()
	if j.State == jobSucceeded || j.State == jobFailed {
		s.jobMu.Unlock()
		return
	}
	j.FinishedAt = s.now()
	snap, head := j.SnapshotID, j.iaHead
	s.jobMu.Unlock()
	if err != nil {
		msg := clipErr(err.Error())
		if j.Kind == "backup" {
			s.recordBackup(j.DestinationID, snap, "", msg)
		}
		s.audit(j.Actor, j.tokenID, "backup "+j.Kind+" failed", j.Destination, "error")
		s.emitFailed(context.Background(), j, err)
		if s.Log != nil {
			s.Log.Error("backup job failed", "kind", j.Kind, "dest", j.Destination, "id", j.ID, "err", msg)
		}
		s.jobMu.Lock()
		j.State = jobFailed
		j.Error = msg
		j.Step = "Failed"
		s.jobMu.Unlock()
		s.logLine(j, msg)
		return
	}
	if j.Kind == "backup" {
		s.recordBackup(j.DestinationID, snap, head, "")
	}
	s.audit(j.Actor, j.tokenID, "backup "+j.Kind+" finished", j.Destination, "ok")
	if s.Log != nil {
		s.Log.Info("backup job finished", "kind", j.Kind, "dest", j.Destination, "id", j.ID)
	}
	s.jobMu.Lock()
	j.State = jobSucceeded
	j.Step = "Finished"
	j.Percent = 100
	s.jobMu.Unlock()
	s.logLine(j, "Finished.")
}

func (s *Service) trimJobs() {
	for len(s.jobQ) > 30 {
		drop := -1
		for i, id := range s.jobQ {
			j := s.jobM[id]
			if j == nil || (j.State != jobQueued && j.State != jobRunning) {
				drop = i
				break
			}
		}
		if drop < 0 {
			return
		}
		delete(s.jobM, s.jobQ[drop])
		s.jobQ = append(s.jobQ[:drop], s.jobQ[drop+1:]...)
	}
}

func (s *Service) composePresent() bool {
	root := s.SharesRoot
	if strings.TrimSpace(root) == "" {
		root = "/srv/feldcore"
	}
	return dirExists(filepathJoin(s.DataDir, "compose")) || dirExists(filepathJoin(root, "compose")) || dirExists(filepathJoin(root, "stacks"))
}

func (s *Service) audit(actor, tokenID, action, target, result string) {
	if s.Audit == nil {
		return
	}
	if strings.TrimSpace(actor) == "" {
		actor = "admin"
	}
	s.Audit(actor, tokenID, action, clipErr(target), result)
}

func idGen() string { return newJobID() }

func inID(DestinationInput) string { return "" }

func filepathJoin(a, b string) string {
	if a == "" {
		return ""
	}
	return a + "/" + b
}

func anyDir(paths []string) bool {
	for _, p := range paths {
		if dirExists(p) {
			return true
		}
	}
	return false
}
