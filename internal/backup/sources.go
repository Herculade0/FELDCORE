package backup

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// filePlan is the set of paths restic should read for one job.
type filePlan struct {
	Paths    []string
	Excludes []string
	Notes    []string
	IAHead   string
	IAPath   string
	Cleanup  func(context.Context) error
}

func (s *Service) buildPlan(ctx context.Context, dest Destination, jobID string, admin bool) (filePlan, error) {
	staging := filepath.Join(s.DataDir, "backup-staging", jobID)
	if err := os.MkdirAll(staging, 0o700); err != nil {
		return filePlan{}, op("Could not prepare a working folder for the backup.")
	}
	plan := filePlan{Cleanup: func(context.Context) error { return os.RemoveAll(staging) }}
	add := func(p string) {
		if p == "" || !pathExists(p) {
			return
		}
		plan.Paths = append(plan.Paths, p)
	}
	if dest.Sets.Config {
		state := filepath.Join(s.DataDir, "state.json")
		if !admin {
			plan.Notes = append(plan.Notes, "Settings file left out.")
		} else if pathExists(state) {
			add(state)
		} else {
			plan.Notes = append(plan.Notes, "No FELDCORE state file was found.")
		}
		sec := filepath.Join(s.DataDir, "secrets")
		if dirExists(sec) {
			add(sec)
		}
		cat := s.catalogPath()
		if pathExists(cat) {
			add(cat)
		}
	}
	if dest.Sets.Appdata {
		app := s.appdataDir()
		if dirExists(app) {
			add(app)
		} else {
			plan.Notes = append(plan.Notes, "No appdata folder was found, so it was not included.")
		}
	}
	if dest.Sets.Compose {
		for _, dir := range s.composeDirs(ctx) {
			if covered(plan.Paths, dir) {
				continue
			}
			add(dir)
		}
		if !hasCompose(plan.Paths) {
			plan.Notes = append(plan.Notes, "No Compose stack folder was found, so none were included.")
		}
	}
	if dest.Sets.Snapshots {
		found := false
		for _, dir := range s.snapshotDirs() {
			if dirExists(dir) {
				add(dir)
				found = true
			}
		}
		if !found {
			plan.Notes = append(plan.Notes, "No snapshot archives folder was found, so none were included.")
		}
	}
	if dest.Sets.InternalAffairs {
		ia := s.iaDir()
		plan.IAPath = ia
		if dirExists(ia) {
			add(ia)
			plan.IAHead = ledgerHead(ia)
		} else {
			plan.Notes = append(plan.Notes, "Internal Affairs is not set up on this server, so the cabinet was not included.")
		}
	}
	if dest.Sets.VMs {
		prep, err := prepareVMs(ctx, s.commander(), s.domainsDir(), filepath.Join(staging, "vms"))
		if err != nil {
			_ = plan.Cleanup(ctx)
			return filePlan{}, err
		}
		plan.Notes = append(plan.Notes, prep.Notes...)
		for _, p := range prep.Paths {
			if !covered(plan.Paths, p) {
				plan.Paths = append(plan.Paths, p)
			}
		}
		prev := plan.Cleanup
		plan.Cleanup = func(ctx context.Context) error {
			err := prep.Cleanup(ctx)
			if e := prev(ctx); e != nil && err == nil {
				err = e
			}
			return err
		}
	}
	plan.Excludes = []string{
		".obsidian",
		".obsidian/**",
		"**/.obsidian/**",
		repoDirName,
		"**/" + repoDirName + "/**",
		filepath.Join(s.DataDir, "backup-mounts"),
		filepath.Join(s.DataDir, "backup-staging"),
		filepath.Join(s.DataDir, "backup-ssh"),
		filepath.Join(s.DataDir, "restores"),
		filepath.Join(s.DataDir, "restic-cache"),
		filepath.Join(s.DataDir, "bin"),
	}
	manifest := map[string]any{
		"taken": s.now().UTC().Format(time.RFC3339),
		"sets":  dest.Sets,
		"notes": plan.Notes,
		"paths": plan.Paths,
	}
	if plan.IAHead != "" {
		manifest["internalAffairsHead"] = plan.IAHead
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		_ = plan.Cleanup(ctx)
		return filePlan{}, op("Could not write the backup manifest.")
	}
	mf := filepath.Join(staging, "manifest.json")
	if err := os.WriteFile(mf, raw, 0o600); err != nil {
		_ = plan.Cleanup(ctx)
		return filePlan{}, op("Could not write the backup manifest.")
	}
	plan.Paths = append(plan.Paths, mf)
	plan.Paths = uniqueStrings(plan.Paths)
	if len(plan.Paths) == 1 {
		_ = plan.Cleanup(ctx)
		return filePlan{}, input("Nothing was found to back up.")
	}
	return plan, nil
}

func (s *Service) appdataDir() string {
	explicit := ""
	if s.Store != nil {
		explicit = s.Store.Snapshot().Appdata
	}
	return AppdataPath(explicit, s.SharesRoot)
}

func (s *Service) domainsDir() string {
	if s.Store != nil {
		if p := strings.TrimSpace(s.Store.Snapshot().Prefs.VMDomains); p != "" {
			return p
		}
	}
	root := s.SharesRoot
	if strings.TrimSpace(root) == "" {
		root = "/srv/feldcore"
	}
	return filepath.Join(root, "domains")
}

func (s *Service) iaDir() string {
	root := s.SharesRoot
	if strings.TrimSpace(root) == "" {
		root = "/srv/feldcore"
	}
	return filepath.Join(root, "internal-affairs")
}

func (s *Service) snapshotDirs() []string {
	var out []string
	if s.DataDir != "" {
		out = append(out, filepath.Join(s.DataDir, "snapshots"))
	}
	if s.Store != nil {
		if d := strings.TrimSpace(s.Store.Snapshot().Snapshots.Dir); d != "" {
			out = append(out, d)
		}
	}
	if s.SharesRoot != "" {
		out = append(out, filepath.Join(s.SharesRoot, "system", "snapshots"))
	}
	return uniqueStrings(out)
}

func (s *Service) composeDirs(ctx context.Context) []string {
	var extra []string
	if s.Compose != nil {
		extra = s.Compose(ctx)
	} else {
		extra = dockerComposeDirs(ctx, s.commander())
	}
	root := s.SharesRoot
	if strings.TrimSpace(root) == "" {
		root = "/srv/feldcore"
	}
	cands := []string{
		filepath.Join(s.DataDir, "compose"),
		filepath.Join(root, "compose"),
		filepath.Join(root, "stacks"),
	}
	cands = append(cands, extra...)
	var out []string
	for _, dir := range cands {
		dir = strings.TrimSpace(dir)
		if dir == "" || !filepath.IsAbs(dir) || strings.Contains(dir, "..") {
			continue
		}
		if dirExists(filepath.Clean(dir)) {
			out = append(out, filepath.Clean(dir))
		}
	}
	return uniqueStrings(out)
}

func dockerComposeDirs(ctx context.Context, cmd Commander) []string {
	if cmd == nil {
		return nil
	}
	if _, err := cmd.Look("docker"); err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	ids, err := cmd.Run(ctx, "docker", "ps", "-aq")
	if err != nil {
		return nil
	}
	var args []string
	for _, id := range strings.Fields(ids) {
		args = append(args, id)
		if len(args) >= 200 {
			break
		}
	}
	if len(args) == 0 {
		return nil
	}
	inspect := append([]string{"inspect", "--format", `{{index .Config.Labels "com.docker.compose.project.working_dir"}}`}, args...)
	out, err := cmd.Run(ctx, "docker", inspect...)
	if err != nil {
		return nil
	}
	var dirs []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if filepath.IsAbs(line) {
			dirs = append(dirs, line)
		}
	}
	return dirs
}

func ledgerHead(dir string) string {
	f, err := os.Open(filepath.Join(dir, "ledger.jsonl"))
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	var last string
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line != "" {
			last = line
		}
	}
	var row struct {
		Line string `json:"line"`
	}
	if json.Unmarshal([]byte(last), &row) != nil {
		return ""
	}
	return strings.TrimSpace(row.Line)
}

func covered(parents []string, child string) bool {
	for _, p := range parents {
		if p == child || insideDir(p, child) {
			return true
		}
	}
	return false
}

func insideDir(parent, child string) bool {
	parent = filepath.Clean(parent)
	child = filepath.Clean(child)
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func hasCompose(paths []string) bool {
	for _, p := range paths {
		base := filepath.Base(p)
		if base == "compose" || base == "stacks" || strings.Contains(p, string(filepath.Separator)+"compose") || strings.Contains(p, string(filepath.Separator)+"stacks") {
			return true
		}
	}
	return false
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func (s *Service) commander() Commander {
	if s.Cmd != nil {
		return s.Cmd
	}
	return osCmd{}
}

func (s *Service) mounter() Mounter {
	if s.Mounts != nil {
		return s.Mounts
	}
	return osMounter{}
}

func (s *Service) engine() Engine {
	if s.Engine != nil {
		return s.Engine
	}
	return &CLI{DataDir: s.DataDir}
}
