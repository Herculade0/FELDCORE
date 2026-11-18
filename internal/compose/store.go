package compose

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Herculade0/feldcore/internal/docker"
)

// Deps wires the stack store to the rest of the daemon.
type Deps struct {
	DataDir    string
	SharesRoot string
	Log        *slog.Logger
	Paths      func() PathInfo
	Inspect    func(ctx context.Context, id string) (docker.SpecInfo, error)
	Remove     func(ctx context.Context, id string) error
	// Run replaces process execution in tests.
	Run Exec
}

// Service stores stacks on disk and drives the Compose CLI.
type Service struct {
	data    string
	shares  string
	log     *slog.Logger
	paths   func() PathInfo
	inspect func(context.Context, string) (docker.SpecInfo, error)
	remove  func(context.Context, string) error
	exec    Exec

	mu    sync.Mutex
	locks map[string]*sync.Mutex
	now   func() time.Time
}

// New returns a stack service. DataDir is the FELDCORE data directory.
func New(d Deps) *Service {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	return &Service{
		data: d.DataDir, shares: d.SharesRoot, log: d.Log,
		paths: d.Paths, inspect: d.Inspect, remove: d.Remove, exec: d.Run,
		locks: map[string]*sync.Mutex{}, now: time.Now,
	}
}

// Root is the directory backups should include.
func (s *Service) Root() string {
	return filepath.Join(s.data, DirName)
}

func (s *Service) pathInfo() PathInfo {
	if s.paths != nil {
		return s.paths()
	}
	root := s.shares
	if strings.TrimSpace(root) == "" {
		root = "/mnt/feldcore/data"
	}
	return PathInfo{SharesRoot: root}
}

func (s *Service) dir(name string) string {
	return filepath.Join(s.Root(), name)
}

func (s *Service) hold(name string) func() {
	s.mu.Lock()
	m := s.locks[name]
	if m == nil {
		m = &sync.Mutex{}
		s.locks[name] = m
	}
	s.mu.Unlock()
	m.Lock()
	return m.Unlock
}

// Preview validates a file without writing it. name may be empty.
func (s *Service) Preview(name, compose, env string, isolate, rewrite bool, fixes []Fix) (Preview, error) {
	base := ""
	if n, err := CleanName(name); err == nil {
		name = n
		base = s.dir(name)
	} else if strings.TrimSpace(name) != "" {
		return Preview{}, err
	}
	p, err := Prepare(compose, env, isolate, s.pathInfo(), base, rewrite, fixes)
	if err != nil {
		return Preview{}, err
	}
	s.annotate(&p, name, isolate)
	if name != "" {
		if prev, err := s.read(name); err == nil {
			body := compose
			if p.Changed {
				body = p.Compose
			}
			p.Diff = Diff(prev.Compose, body)
		}
		p.Project = Project(name)
	}
	return p, nil
}

func (s *Service) annotate(p *Preview, name string, isolate bool) {
	if p.OwnNetworks || !p.Valid {
		return
	}
	if name == "" {
		if isolate {
			p.Network = docker.StackNetworkPrefix + "<name>"
		} else {
			p.Network = "bridge"
		}
		return
	}
	if isolate {
		p.Network = docker.StackNetwork(name)
	} else {
		p.Network = "bridge"
	}
}

// Check reports a Gate when a deploy must show a diff or wait for
// Deploy anyway. A nil error means the file may be started.
func (s *Service) Check(name, compose, env string, isolate, confirm, anyway bool) (Preview, error) {
	name, err := CleanName(name)
	if err != nil {
		return Preview{}, err
	}
	p, err := s.Preview(name, compose, env, isolate, false, nil)
	if err != nil {
		return Preview{}, err
	}
	if !p.Valid {
		return p, &InputError{Msg: p.Error}
	}
	gate := &Gate{Diff: p.Diff, Findings: p.Findings}
	if _, err := s.read(name); err == nil && p.Diff != "" && !confirm {
		gate.Confirm = true
	}
	if NeedsAck(p.Findings) && !anyway {
		gate.Ack = true
	}
	if gate.Confirm && gate.Ack {
		gate.Msg = "Review the changes. This stack has high-risk settings, so choose Deploy anyway to continue."
		return p, gate
	}
	if gate.Ack {
		gate.Msg = "This stack has high-risk settings. Read the warnings or choose Deploy anyway."
		return p, gate
	}
	if gate.Confirm {
		gate.Msg = "Review the changes, then deploy."
		return p, gate
	}
	return p, nil
}

// Create stores a new stack. It does not start containers.
func (s *Service) Create(in Write) (Stack, error) {
	return s.save(in, false)
}

// Save replaces the files of a stack that already exists.
func (s *Service) Save(in Write) (Stack, error) {
	return s.save(in, true)
}

func (s *Service) save(in Write, mustExist bool) (Stack, error) {
	name, err := CleanName(in.Name)
	if err != nil {
		return Stack{}, err
	}
	in.Name = name
	p, err := Prepare(in.Compose, in.Env, in.Isolate, s.pathInfo(), s.dir(name), false, nil)
	if err != nil {
		return Stack{}, err
	}
	if !p.Valid {
		return Stack{}, &InputError{Msg: p.Error}
	}
	unlock := s.hold(name)
	defer unlock()
	_, readErr := s.read(name)
	exists := readErr == nil
	if mustExist && !exists {
		return Stack{}, inputf("There is no stack named %s.", name)
	}
	if !mustExist && exists {
		return Stack{}, inputf("A stack named %s already exists.", name)
	}
	var created time.Time
	state := "saved"
	if exists {
		if prev, err := s.read(name); err == nil {
			created = prev.CreatedAt
			if prev.State != "" {
				state = prev.State
			}
		}
	}
	if created.IsZero() {
		created = s.now()
	}
	if strings.TrimSpace(in.Source) == "" {
		in.Source = "paste"
	}
	meta := diskMeta{
		Name: name, Source: in.Source, Isolate: in.Isolate,
		GitURL: in.GitURL, GitPath: in.GitPath, GitRef: in.GitRef,
		Replaces: strings.TrimSpace(in.Replaces),
		Created:  created, Updated: s.now(), State: state,
	}
	if err := s.write(meta, in.Compose, in.Env); err != nil {
		return Stack{}, err
	}
	return s.read(name)
}

func (s *Service) write(meta diskMeta, compose, env string) error {
	if err := os.MkdirAll(s.Root(), 0o750); err != nil {
		return opf("Could not create the stack folder.")
	}
	readme := filepath.Join(s.Root(), readmeFile)
	if _, err := os.Stat(readme); errors.Is(err, fs.ErrNotExist) {
		_ = writeAtomic(readme, Readme, 0o640)
	}
	dir := s.dir(meta.Name)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return opf("Could not create the stack folder.")
	}
	if err := writeAtomic(filepath.Join(dir, composeFile), compose, 0o640); err != nil {
		return opf("Could not write the compose file.")
	}
	envPath := filepath.Join(dir, envFile)
	if strings.TrimSpace(env) == "" {
		_ = os.Remove(envPath)
	} else if err := writeAtomic(envPath, env, 0o600); err != nil {
		return opf("Could not write the env file.")
	}
	ov, _, err := Override(compose, meta.Name, meta.Isolate)
	if err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(dir, overrideFile), ov, 0o640); err != nil {
		return opf("Could not write the network override.")
	}
	raw, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return opf("Could not write the stack record.")
	}
	if err := writeAtomic(filepath.Join(dir, metaFile), string(raw)+"\n", 0o600); err != nil {
		return opf("Could not write the stack record.")
	}
	return nil
}

func writeAtomic(path, data string, mode os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".tmp-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmp)
		}
	}()
	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.WriteString(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	ok = true
	return nil
}

type diskMeta struct {
	Name     string    `json:"name"`
	Source   string    `json:"source,omitempty"`
	Isolate  bool      `json:"isolate"`
	GitURL   string    `json:"gitUrl,omitempty"`
	GitPath  string    `json:"gitPath,omitempty"`
	GitRef   string    `json:"gitRef,omitempty"`
	Replaces string    `json:"replaces,omitempty"`
	Created  time.Time `json:"createdAt"`
	Updated  time.Time `json:"updatedAt"`
	State    string    `json:"state"`
}

func (s *Service) read(name string) (Stack, error) {
	name, err := CleanName(name)
	if err != nil {
		return Stack{}, err
	}
	dir := s.dir(name)
	body, err := os.ReadFile(filepath.Join(dir, composeFile))
	if err != nil {
		return Stack{}, inputf("There is no stack named %s.", name)
	}
	if len(body) > maxCompose {
		return Stack{}, inputf("The compose file for %s is too large.", name)
	}
	st := Stack{
		Name: name, Compose: string(body), Isolate: true, State: "saved",
		Project: Project(name), Path: dir, Services: []LiveService{},
	}
	if env, err := os.ReadFile(filepath.Join(dir, envFile)); err == nil && len(env) <= maxEnv {
		st.Env = string(env)
	}
	raw, err := os.ReadFile(filepath.Join(dir, metaFile))
	if err == nil {
		var meta diskMeta
		if json.Unmarshal(raw, &meta) == nil {
			st.Isolate = meta.Isolate
			st.Source = meta.Source
			st.GitURL, st.GitPath, st.GitRef = meta.GitURL, meta.GitPath, meta.GitRef
			st.Replaces = meta.Replaces
			st.CreatedAt, st.UpdatedAt = meta.Created, meta.Updated
			if meta.State != "" {
				st.State = meta.State
			}
		}
	}
	if st.Isolate && !st.ownNetworks() {
		st.Network = docker.StackNetwork(name)
	} else if !st.Isolate && !st.ownNetworks() {
		st.Network = "bridge"
	}
	return st, nil
}

func (st Stack) ownNetworks() bool {
	f, err := parse(st.Compose)
	return err == nil && f.hasNets
}

// Get returns one stack and, when Compose is available, its containers.
func (s *Service) Get(ctx context.Context, name string) (Stack, error) {
	st, err := s.read(name)
	if err != nil {
		return Stack{}, err
	}
	s.attachLive(ctx, &st)
	return st, nil
}

// Catalog lists every stack. ComposeOK is false when the v2 CLI is missing.
func (s *Service) Catalog(ctx context.Context) (Catalog, error) {
	cat := Catalog{Root: s.Root(), Stacks: []Summary{}}
	if ver, err := s.Version(ctx); err == nil {
		cat.Compose = ver
		cat.ComposeOK = true
	}
	entries, err := os.ReadDir(s.Root())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return cat, nil
		}
		return Catalog{}, opf("Could not read the stack folder.")
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		st, err := s.read(e.Name())
		if err != nil {
			continue
		}
		s.attachLive(ctx, &st)
		cat.Stacks = append(cat.Stacks, Summary{
			Name: st.Name, Isolate: st.Isolate, Source: st.Source, Replaces: st.Replaces,
			UpdatedAt: st.UpdatedAt, State: st.State, Network: st.Network,
			Project: st.Project, Path: st.Path, Services: st.Services,
		})
	}
	sort.Slice(cat.Stacks, func(i, j int) bool { return cat.Stacks[i].Name < cat.Stacks[j].Name })
	return cat, nil
}

func (s *Service) attachLive(ctx context.Context, st *Stack) {
	infos := []ServiceInfo{}
	if f, err := parse(st.Compose); err == nil {
		infos = f.infos()
	}
	live, err := s.ps(ctx, st.Name)
	if err != nil {
		live = nil
	}
	st.Services = mergeLive(infos, live)
	if st.Services == nil {
		st.Services = []LiveService{}
	}
	if err == nil {
		st.State = rollup(st.Services, st.State)
	}
}

func mergeLive(file []ServiceInfo, live []LiveService) []LiveService {
	by := map[string]LiveService{}
	var order []string
	for _, l := range live {
		if _, ok := by[l.Name]; !ok {
			order = append(order, l.Name)
		}
		by[l.Name] = l
	}
	var out []LiveService
	seen := map[string]bool{}
	for _, s := range file {
		l := by[s.Name]
		if l.Name == "" {
			l.Name = s.Name
		}
		if l.Image == "" {
			l.Image = s.Image
		}
		if l.Container == "" {
			l.Container = s.Container
		}
		out = append(out, l)
		seen[s.Name] = true
	}
	for _, name := range order {
		if !seen[name] {
			out = append(out, by[name])
		}
	}
	if out == nil {
		out = []LiveService{}
	}
	return out
}

func rollup(svcs []LiveService, stored string) string {
	known, running := 0, 0
	for _, s := range svcs {
		if s.State == "" {
			continue
		}
		known++
		if s.State == "running" {
			running++
		}
	}
	if known == 0 {
		if stored != "" {
			return stored
		}
		return "saved"
	}
	switch {
	case running == 0:
		return "stopped"
	case running == known:
		return "running"
	default:
		return "partial"
	}
}

func (s *Service) setState(name, state string) {
	st, err := s.read(name)
	if err != nil {
		return
	}
	meta := diskMeta{
		Name: name, Source: st.Source, Isolate: st.Isolate,
		GitURL: st.GitURL, GitPath: st.GitPath, GitRef: st.GitRef,
		Replaces: st.Replaces, Created: st.CreatedAt, Updated: s.now(), State: state,
	}
	raw, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return
	}
	_ = writeAtomic(filepath.Join(s.dir(name), metaFile), string(raw)+"\n", 0o600)
}

func (s *Service) clearReplaces(name string) {
	st, err := s.read(name)
	if err != nil || st.Replaces == "" {
		return
	}
	meta := diskMeta{
		Name: name, Source: st.Source, Isolate: st.Isolate,
		GitURL: st.GitURL, GitPath: st.GitPath, GitRef: st.GitRef,
		Created: st.CreatedAt, Updated: s.now(), State: st.State,
	}
	raw, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return
	}
	_ = writeAtomic(filepath.Join(s.dir(name), metaFile), string(raw)+"\n", 0o600)
}
