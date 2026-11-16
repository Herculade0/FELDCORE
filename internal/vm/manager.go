package vm

import (
	"context"
	"encoding/xml"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Herculade0/feldcore/internal/store"
)

// Deps are the VM manager's inputs.
type Deps struct {
	Store      *store.Store
	SharesRoot string
	Shares     func() []ShareInfo
	Log        *slog.Logger
	Runner     Runner
	// Root, when set, is a fake /proc and /sys tree for tests.
	Root string
}

// Manager talks to libvirt and the shares that hold disks and ISOs.
type Manager struct {
	st         *store.Store
	sharesRoot string
	shareFn    func() []ShareInfo
	log        *slog.Logger
	run        Runner
	root       string
	kvm        func() (bool, string)

	jobMu    sync.Mutex
	jobs     map[string]*Job
	relMu    sync.Mutex
	relCache map[string]relEntry
}

// New builds a manager. A nil runner uses virsh and qemu-img on PATH.
func New(d Deps) *Manager {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.Runner == nil {
		d.Runner = execRunner{}
	}
	if d.Shares == nil {
		d.Shares = func() []ShareInfo { return nil }
	}
	return &Manager{
		st: d.Store, sharesRoot: d.SharesRoot, shareFn: d.Shares,
		log: d.Log, run: d.Runner, root: d.Root, kvm: KVMStatus,
		jobs: map[string]*Job{}, relCache: map[string]relEntry{},
	}
}

// VM is one row on the VMs page.
type VM struct {
	Name        string `json:"name"`
	State       string `json:"state"`
	CPUs        int    `json:"cpus"`
	MemoryMB    int    `json:"memoryMB"`
	MaxMemoryMB int    `json:"maxMemoryMB"`
	Autostart   bool   `json:"autostart"`
}

// Overview is the VM list. Message is set when libvirt itself is unavailable,
// so the page can still explain that instead of looking empty.
type Overview struct {
	VMs     []VM   `json:"vms"`
	Message string `json:"message,omitempty"`
}

// Detail is one VM opened for editing.
type Detail struct {
	VM       VM       `json:"vm"`
	Spec     Spec     `json:"spec"`
	Warnings []string `json:"warnings,omitempty"`
	XML      string   `json:"xml"`
}

// Settings is the VM Manager paths card.
type Settings struct {
	DomainsPath    string `json:"domainsPath"`
	ISOsPath       string `json:"isosPath"`
	DomainsDefault string `json:"domainsDefault"`
	ISOsDefault    string `json:"isosDefault"`
}

func (m *Manager) List(ctx context.Context) (Overview, error) {
	if !m.run.Has("virsh") {
		return Overview{VMs: []VM{}, Message: "virsh is not installed. Turn on libvirt in Settings to install it."}, nil
	}
	out, err := m.virsh(ctx, "list", "--all", "--name")
	if err != nil {
		return Overview{VMs: []VM{}, Message: err.Error()}, nil
	}
	var vms []VM
	for _, name := range strings.Fields(out) {
		if err := checkName(name); err != nil {
			continue
		}
		info, err := m.virsh(ctx, "dominfo", name)
		if err != nil {
			m.log.Warn("vm info", "name", name, "err", err)
			continue
		}
		vms = append(vms, parseDominfo(name, info))
	}
	if vms == nil {
		vms = []VM{}
	}
	return Overview{VMs: vms}, nil
}

func (m *Manager) Get(ctx context.Context, name string) (Detail, error) {
	if err := checkName(name); err != nil {
		return Detail{}, err
	}
	doc, err := m.virsh(ctx, "dumpxml", name)
	if err != nil {
		return Detail{}, err
	}
	names := make([]string, 0, len(m.shares()))
	for _, sh := range m.shares() {
		names = append(names, sh.Name)
	}
	spec, warnings := ParseDomain(doc, names)
	if spec.Name == "" {
		spec.Name = name
	}
	for i, d := range spec.Disks {
		if d.Path == "" {
			continue
		}
		if n, err := imageBytes(d.Path); err == nil {
			spec.Disks[i].SizeGB = roundGB(n)
		}
	}
	info, err := m.virsh(ctx, "dominfo", name)
	vm := VM{Name: name}
	if err == nil {
		vm = parseDominfo(name, info)
	}
	return Detail{VM: vm, Spec: spec, Warnings: warnings, XML: doc}, nil
}

func (m *Manager) XML(ctx context.Context, name string) (string, error) {
	if err := checkName(name); err != nil {
		return "", err
	}
	return m.virsh(ctx, "dumpxml", name)
}

func (m *Manager) DefineXML(ctx context.Context, name, doc string) error {
	if err := checkName(name); err != nil {
		return err
	}
	if err := vetXML(name, doc); err != nil {
		return err
	}
	st, err := m.stateOf(ctx, name)
	if err != nil {
		return err
	}
	if st != "stopped" {
		return invalid("Shut the VM down before replacing its XML.")
	}
	return m.define(ctx, doc)
}

func (m *Manager) Start(ctx context.Context, name string) error {
	if err := checkName(name); err != nil {
		return err
	}
	if ok, msg := m.kvm(); !ok {
		return invalid(msg)
	}
	if _, err := m.virsh(ctx, "start", name); err != nil {
		if strings.Contains(err.Error(), "/dev/kvm") || strings.Contains(strings.ToLower(err.Error()), "kvm") {
			if _, msg := m.kvm(); msg != "" {
				return invalid(msg)
			}
		}
		return err
	}
	return nil
}

func (m *Manager) Stop(ctx context.Context, name string) error {
	if err := checkName(name); err != nil {
		return err
	}
	_, err := m.virsh(ctx, "shutdown", name)
	return err
}

func (m *Manager) ForceStop(ctx context.Context, name string) error {
	if err := checkName(name); err != nil {
		return err
	}
	_, err := m.virsh(ctx, "destroy", name)
	return err
}

func (m *Manager) Pause(ctx context.Context, name string) error {
	if err := checkName(name); err != nil {
		return err
	}
	_, err := m.virsh(ctx, "suspend", name)
	return err
}

func (m *Manager) Resume(ctx context.Context, name string) error {
	if err := checkName(name); err != nil {
		return err
	}
	_, err := m.virsh(ctx, "resume", name)
	return err
}

func (m *Manager) SetAutostart(ctx context.Context, name string, on bool) error {
	if err := checkName(name); err != nil {
		return err
	}
	if on {
		_, err := m.virsh(ctx, "autostart", name)
		return err
	}
	_, err := m.virsh(ctx, "autostart", name, "--disable")
	return err
}

func (m *Manager) Delete(ctx context.Context, name string, disks bool) error {
	if err := checkName(name); err != nil {
		return err
	}
	if st, err := m.stateOf(ctx, name); err == nil && st != "stopped" {
		return invalid("Shut the VM down before deleting it.")
	}
	var files []string
	if disks {
		if doc, err := m.virsh(ctx, "dumpxml", name); err == nil {
			spec, _ := ParseDomain(doc, nil)
			dir := m.vmDir(name)
			for _, d := range spec.Disks {
				if d.Path != "" && within(dir, d.Path) {
					files = append(files, d.Path)
				}
			}
			seed := filepath.Join(dir, "cidata.iso")
			if within(dir, seed) {
				files = append(files, seed)
			}
		}
	}
	if _, err := m.virsh(ctx, "undefine", name, "--nvram", "--tpm", "--snapshots-metadata"); err != nil {
		if _, err2 := m.virsh(ctx, "undefine", name); err2 != nil {
			return err
		}
	}
	for _, f := range files {
		_ = os.Remove(f)
	}
	if disks {
		_ = os.Remove(m.vmDir(name))
	}
	return nil
}

// VNCAddr is the loopback address of a running guest's VNC server.
// The console proxy refuses anything that is not on this machine.
func (m *Manager) VNCAddr(ctx context.Context, name string) (string, error) {
	if err := checkName(name); err != nil {
		return "", err
	}
	out, err := m.virsh(ctx, "vncdisplay", name)
	if err != nil {
		return "", &OpError{"This VM has no VNC console. It may be stopped, or its display is a passed-through GPU."}
	}
	return parseVNC(out)
}

// Snapshot is one disk snapshot.
type Snapshot struct {
	Name  string `json:"name"`
	State string `json:"state,omitempty"`
}

func (m *Manager) Snapshots(ctx context.Context, name string) ([]Snapshot, error) {
	if err := checkName(name); err != nil {
		return nil, err
	}
	out, err := m.virsh(ctx, "snapshot-list", name)
	if err != nil {
		return nil, err
	}
	return parseSnapshots(out), nil
}

func (m *Manager) CreateSnapshot(ctx context.Context, name, snap, desc string) error {
	if err := checkName(name); err != nil {
		return err
	}
	if !snapRe.MatchString(snap) {
		return invalid("Name the snapshot with letters, numbers, dots or hyphens.")
	}
	desc = strings.TrimSpace(desc)
	if len(desc) > 200 {
		return invalid("The snapshot note is too long.")
	}
	args := []string{"snapshot-create-as", name, snap}
	if desc != "" {
		args = append(args, "--description", desc)
	}
	_, err := m.virsh(ctx, args...)
	return err
}

func (m *Manager) RevertSnapshot(ctx context.Context, name, snap string) error {
	if err := checkName(name); err != nil || !snapRe.MatchString(snap) {
		return invalid("That snapshot is not valid.")
	}
	_, err := m.virsh(ctx, "snapshot-revert", name, snap)
	return err
}

func (m *Manager) DeleteSnapshot(ctx context.Context, name, snap string) error {
	if err := checkName(name); err != nil || !snapRe.MatchString(snap) {
		return invalid("That snapshot is not valid.")
	}
	_, err := m.virsh(ctx, "snapshot-delete", name, snap)
	return err
}

func (m *Manager) SettingsView() Settings {
	var domains, isos string
	if m.st != nil {
		p := m.st.Snapshot().Prefs
		domains, isos = p.VMDomains, p.VMISOs
	}
	return Settings{
		DomainsPath: domains, ISOsPath: isos,
		DomainsDefault: m.defaultDir("domains"), ISOsDefault: m.defaultDir("isos"),
	}
}

func (m *Manager) SetSettings(domains, isos string) error {
	if m.st == nil {
		return &OpError{"VM settings cannot be saved."}
	}
	domains = strings.TrimSpace(domains)
	isos = strings.TrimSpace(isos)
	if domains != "" {
		if err := validateStoragePath(domains); err != nil {
			return err
		}
		if err := os.MkdirAll(domains, 0o755); err != nil {
			return &OpError{"Could not create the domains folder."}
		}
	}
	if isos != "" {
		if err := validateStoragePath(isos); err != nil {
			return err
		}
		if err := os.MkdirAll(isos, 0o755); err != nil {
			return &OpError{"Could not create the ISO folder."}
		}
	}
	return m.st.Update(func(st *store.State) error {
		st.Prefs.VMDomains = domains
		st.Prefs.VMISOs = isos
		return nil
	})
}

func (m *Manager) virsh(ctx context.Context, args ...string) (string, error) {
	if !m.run.Has("virsh") {
		return "", &OpError{"virsh is not installed. Turn on libvirt in Settings to install it."}
	}
	full := append([]string{"-c", "qemu:///system", "--quiet"}, args...)
	out, err := m.run.Run(ctx, 30*time.Second, "virsh", full...)
	if err != nil {
		msg := strings.TrimSpace(out)
		if msg == "" {
			msg = err.Error()
		}
		return out, &OpError{lastLine(msg)}
	}
	return out, nil
}

func (m *Manager) stateOf(ctx context.Context, name string) (string, error) {
	out, err := m.virsh(ctx, "domstate", name)
	if err != nil {
		return "", err
	}
	return normState(out), nil
}

func (m *Manager) define(ctx context.Context, doc string) error {
	f, err := os.CreateTemp("", "feldcore-domain-*.xml")
	if err != nil {
		return &OpError{"Could not write the domain XML."}
	}
	defer os.Remove(f.Name())
	_ = f.Chmod(0o600)
	if _, err := f.WriteString(doc); err != nil {
		f.Close()
		return &OpError{"Could not write the domain XML."}
	}
	if err := f.Close(); err != nil {
		return &OpError{"Could not write the domain XML."}
	}
	_, err = m.virsh(ctx, "define", f.Name())
	return err
}

func (m *Manager) domainsDir() string {
	if m.st != nil {
		if p := strings.TrimSpace(m.st.Snapshot().Prefs.VMDomains); p != "" {
			return p
		}
	}
	return m.defaultDir("domains")
}

func (m *Manager) isosDir() string {
	if m.st != nil {
		if p := strings.TrimSpace(m.st.Snapshot().Prefs.VMISOs); p != "" {
			return p
		}
	}
	return m.defaultDir("isos")
}

func (m *Manager) defaultDir(name string) string {
	for _, sh := range m.shares() {
		if strings.EqualFold(sh.Name, name) && sh.Path != "" {
			return sh.Path
		}
	}
	return filepath.Join(m.sharesRoot, name)
}

func (m *Manager) vmDir(name string) string {
	return filepath.Join(m.domainsDir(), name)
}

func (m *Manager) shareList() []ShareInfo {
	in := m.shares()
	if in == nil {
		return []ShareInfo{}
	}
	return in
}

func (m *Manager) shares() []ShareInfo {
	if m.shareFn == nil {
		return nil
	}
	return m.shareFn()
}

func parseDominfo(name, info string) VM {
	vm := VM{Name: name, State: "stopped"}
	for _, line := range strings.Split(info, "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		switch key {
		case "State":
			vm.State = normState(val)
		case "CPU(s)":
			vm.CPUs = int(atoi64(val))
		case "Max memory":
			vm.MaxMemoryMB = memInfoMB(val)
		case "Used memory":
			vm.MemoryMB = memInfoMB(val)
		case "Autostart":
			vm.Autostart = strings.EqualFold(val, "enable")
		}
	}
	if vm.MemoryMB == 0 {
		vm.MemoryMB = vm.MaxMemoryMB
	}
	return vm
}

func memInfoMB(v string) int {
	fields := strings.Fields(v)
	if len(fields) == 0 {
		return 0
	}
	n := atoi64(fields[0])
	unit := "KiB"
	if len(fields) > 1 {
		unit = fields[1]
	}
	return toMB(n, unit)
}

func normState(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "running":
		return "running"
	case "paused", "pmsuspended":
		return "paused"
	case "shut off", "shutoff":
		return "stopped"
	case "crashed":
		return "crashed"
	default:
		if s == "" {
			return "stopped"
		}
		return "other"
	}
}

func parseSnapshots(out string) []Snapshot {
	var snaps []Snapshot
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Name") || strings.HasPrefix(line, "---") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 || !snapRe.MatchString(fields[0]) {
			continue
		}
		st := ""
		if len(fields) > 1 {
			st = fields[len(fields)-1]
		}
		snaps = append(snaps, Snapshot{Name: fields[0], State: st})
	}
	if snaps == nil {
		snaps = []Snapshot{}
	}
	return snaps
}

func parseVNC(out string) (string, error) {
	line := ""
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			line = l
			break
		}
	}
	var host, disp string
	var ok bool
	if strings.HasPrefix(line, "[") {
		rest := strings.TrimPrefix(line, "[")
		host, disp, ok = strings.Cut(rest, "]:")
	} else {
		host, disp, ok = strings.Cut(line, ":")
	}
	if !ok {
		return "", &OpError{"This VM has no VNC console."}
	}
	n := int(atoi64(disp))
	port := n
	if n >= 0 && n < 512 {
		port = 5900 + n
	}
	switch host {
	case "", "127.0.0.1", "localhost", "0.0.0.0", "*":
		return "127.0.0.1:" + strconv.Itoa(port), nil
	case "::1":
		return "[::1]:" + strconv.Itoa(port), nil
	default:
		return "", &OpError{"VNC is not listening on localhost, so the console will not connect to it."}
	}
}

func vetXML(name, doc string) error {
	if len(doc) == 0 || len(doc) > 1<<20 {
		return invalid("The domain XML is empty or too large.")
	}
	low := strings.ToLower(doc)
	if strings.Contains(low, "<!doctype") || strings.Contains(low, "<!entity") {
		return invalid("The XML cannot contain a document type or entities.")
	}
	if !strings.Contains(low, "<domain") {
		return invalid("That is not a libvirt domain definition.")
	}
	spec, _ := ParseDomain(doc, nil)
	if spec.Name != name {
		return invalid("The domain name in the XML must stay %s.", name)
	}
	return vncIsLocal(doc)
}

func vncIsLocal(doc string) error {
	// VNC must not be published on the LAN. The browser console is already
	// authenticated, and the proxy only dials loopback.
	dec := xml.NewDecoder(strings.NewReader(doc))
	vnc := false
	listen := ""
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return invalid("The XML could not be parsed.")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "graphics" && attr(t, "type") == "vnc" {
				vnc = true
				listen = attr(t, "listen")
			}
			if vnc && t.Name.Local == "listen" {
				if a := attr(t, "address"); a != "" {
					listen = a
				}
			}
		case xml.EndElement:
			if t.Name.Local == "graphics" && vnc {
				switch listen {
				case "127.0.0.1", "localhost", "::1":
				default:
					return invalid("VNC must listen on 127.0.0.1. Open the console from this page; it is not published on the network.")
				}
				vnc = false
			}
		}
	}
	return nil
}
