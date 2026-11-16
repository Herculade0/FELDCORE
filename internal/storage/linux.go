package storage

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var poolName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,31}$`)

// Exec runs a system command. Tests substitute a fake.
type Exec interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
}

// Linux reads disks from sysfs and, when asked, formats one empty disk.
type Linux struct {
	root string
	cmd  Exec
}

// New reads disks under root (usually "/") and runs commands with cmd.
// A nil cmd uses the real programs on PATH.
func New(root string, cmd Exec) *Linux {
	if root == "" {
		root = "/"
	}
	if cmd == nil {
		cmd = realExec{}
	}
	return &Linux{root: root, cmd: cmd}
}

func (l *Linux) Disks(ctx context.Context) ([]Disk, error) {
	return readDisks(l.root)
}

func (l *Linux) Plan(ctx context.Context, in Choice) (Plan, error) {
	disks, err := l.Disks(ctx)
	if err != nil {
		return Plan{}, err
	}
	return Describe(disks, in)
}

func (l *Linux) Apply(ctx context.Context, in Choice) (string, error) {
	disks, err := l.Disks(ctx)
	if err != nil {
		return "", err
	}
	mount, err := Confirm(disks, in)
	if err != nil {
		return "", err
	}
	if !in.Format || strings.TrimSpace(in.Mode) != "disk" {
		if err := l.ensureDir(ctx, mount); err != nil {
			return "", err
		}
		return mount, nil
	}
	p, err := Describe(disks, in)
	if err != nil {
		return "", err
	}
	device := strings.TrimSpace(in.Device)
	if !strings.HasPrefix(device, "/dev/") {
		device = "/dev/" + device
	}
	if _, err := l.cmd.Run(ctx, "mkfs.ext4", "-F", "-L", "feldcore", device); err != nil {
		return "", op("Could not format " + device + ": " + err.Error())
	}
	if err := l.ensureDir(ctx, mount); err != nil {
		return "", err
	}
	if _, err := l.cmd.Run(ctx, "mount", device, mount); err != nil {
		return "", op("Could not mount " + device + " at " + mount + ": " + err.Error())
	}
	spec := device
	if out, err := l.cmd.Run(ctx, "blkid", "-s", "UUID", "-o", "value", device); err == nil {
		if id := strings.TrimSpace(out); id != "" && !strings.ContainsAny(id, " \t\n/") {
			spec = "UUID=" + id
		}
	}
	if err := l.appendFstab(spec, mount); err != nil {
		return "", err
	}
	_ = p
	return mount, nil
}

func (l *Linux) ensureDir(ctx context.Context, mount string) error {
	if l.root != "/" && l.root != "" {
		// Tests must not create folders on the real machine. The command
		// log is enough to show what production would run.
		_, err := l.cmd.Run(ctx, "mkdir", "-p", mount)
		if err != nil {
			return op("Could not create " + mount + ": " + err.Error())
		}
		return nil
	}
	if err := os.MkdirAll(mount, 0o755); err != nil {
		return op("Could not create " + mount + ": " + err.Error())
	}
	return nil
}

func (l *Linux) appendFstab(spec, mount string) error {
	path := filepath.Join(l.root, "etc", "fstab")
	if l.root == "/" {
		path = "/etc/fstab"
	}
	existing, _ := os.ReadFile(path)
	if strings.Contains(string(existing), " "+mount+" ") || strings.Contains(string(existing), "\t"+mount+"\t") {
		return nil
	}
	line := spec + " " + mount + " ext4 defaults,nofail 0 2\n"
	body := string(existing)
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	body += "# feldcore data disk\n" + line
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return op("Could not update fstab: " + err.Error())
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return op("Could not update fstab: " + err.Error())
	}
	return nil
}

func readDisks(root string) ([]Disk, error) {
	block := filepath.Join(root, "sys", "block")
	entries, err := os.ReadDir(block)
	if err != nil {
		if os.IsNotExist(err) {
			return []Disk{}, nil
		}
		return nil, op("Could not list disks.")
	}
	mounts := parseMounts(readFile(filepath.Join(root, "proc", "mounts")))
	var out []Disk
	for _, e := range entries {
		name := e.Name()
		if skipDisk(name) {
			continue
		}
		base := filepath.Join(block, name)
		d := Disk{
			Name:       name,
			Path:       "/dev/" + name,
			Size:       sectorSize(readFile(filepath.Join(base, "size"))),
			Model:      diskModel(base),
			Rotational: strings.TrimSpace(readFile(filepath.Join(base, "queue", "rotational"))) == "1",
			Removable:  strings.TrimSpace(readFile(filepath.Join(base, "removable"))) == "1",
		}
		if m, ok := mounts["/dev/"+name]; ok {
			d.Mount = m.mount
			d.FS = m.fs
			d.OS = m.mount == "/"
		}
		kids, _ := os.ReadDir(base)
		for _, k := range kids {
			if !strings.HasPrefix(k.Name(), name) {
				continue
			}
			p := Part{
				Name: k.Name(),
				Path: "/dev/" + k.Name(),
				Size: sectorSize(readFile(filepath.Join(base, k.Name(), "size"))),
			}
			if m, ok := mounts[p.Path]; ok {
				p.Mount = m.mount
				p.FS = m.fs
				p.OS = m.mount == "/"
				if p.OS {
					d.OS = true
				}
			}
			d.Children = append(d.Children, p)
		}
		out = append(out, d)
	}
	return out, nil
}

type mnt struct{ mount, fs string }

func parseMounts(text string) map[string]mnt {
	out := map[string]mnt{}
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || !strings.HasPrefix(f[0], "/dev/") {
			continue
		}
		// The first mount of a device wins. Later binds are not the data disk.
		if _, ok := out[f[0]]; ok {
			continue
		}
		out[f[0]] = mnt{mount: f[1], fs: f[2]}
	}
	return out
}

func skipDisk(name string) bool {
	switch {
	case strings.HasPrefix(name, "loop"), strings.HasPrefix(name, "ram"),
		strings.HasPrefix(name, "fd"), strings.HasPrefix(name, "sr"),
		strings.HasPrefix(name, "zram"), strings.HasPrefix(name, "nbd"),
		strings.HasPrefix(name, "dm-"):
		return true
	default:
		return false
	}
}

func diskModel(base string) string {
	model := strings.TrimSpace(readFile(filepath.Join(base, "device", "model")))
	vendor := strings.TrimSpace(readFile(filepath.Join(base, "device", "vendor")))
	model = strings.Join(strings.Fields(vendor+" "+model), " ")
	return model
}

func sectorSize(text string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n * 512
}

func readFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

type realExec struct{}

func (realExec) Run(ctx context.Context, name string, args ...string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return text, err
	}
	return text, nil
}
