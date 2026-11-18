package health

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
)

// ReadHost samples the processor, memory, and load.
func ReadHost(ctx context.Context) (HostSample, error) {
	var h HostSample
	if pct, err := cpu.PercentWithContext(ctx, 200*time.Millisecond, false); err == nil && len(pct) > 0 {
		h.CPUPercent = pct[0]
	}
	if n, err := cpu.CountsWithContext(ctx, true); err == nil {
		h.Cores = n
	}
	if l, err := load.AvgWithContext(ctx); err == nil {
		h.Load1 = l.Load1
	}
	if vm, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		h.MemTotal, h.MemUsed, h.MemPercent = vm.Total, vm.Used, vm.UsedPercent
	}
	return h, nil
}

// ReadSpace returns mounted filesystems and, when zpool is installed, ZFS pools.
func ReadSpace(ctx context.Context) ([]Space, error) {
	var out []Space
	parts, err := disk.PartitionsWithContext(ctx, false)
	if err == nil {
		seen := map[string]bool{}
		seenDev := map[string]bool{}
		for _, p := range parts {
			if skipFS(p.Fstype) || skipMount(p.Mountpoint) || seen[p.Mountpoint] {
				continue
			}
			seen[p.Mountpoint] = true
			if strings.HasPrefix(p.Device, "/dev/") {
				if seenDev[p.Device] {
					continue
				}
				seenDev[p.Device] = true
			}
			u, err := disk.UsageWithContext(ctx, p.Mountpoint)
			if err != nil || u.Total == 0 {
				continue
			}
			kind := "disk"
			if p.Fstype == "zfs" || p.Fstype == "btrfs" {
				kind = "pool"
			}
			out = append(out, Space{
				Name: p.Mountpoint, Kind: kind, Total: u.Total, Used: u.Used, UsedPercent: u.UsedPercent,
			})
		}
	}
	out = append(out, readZPools(ctx)...)
	return out, nil
}

func skipFS(fs string) bool {
	switch fs {
	case "tmpfs", "devtmpfs", "overlay", "squashfs", "proc", "sysfs",
		"cgroup", "cgroup2", "devpts", "mqueue", "debugfs", "tracefs",
		"ramfs", "autofs", "binfmt_misc", "fusectl", "configfs", "none",
		"nsfs", "rpc_pipefs", "securityfs":
		return true
	}
	return false
}

func skipMount(mount string) bool {
	return mount == "" || strings.HasPrefix(mount, "/run") || strings.HasPrefix(mount, "/snap") ||
		strings.HasPrefix(mount, "/var/lib/docker") || strings.HasPrefix(mount, "/var/lib/containerd")
}

// ParseZPool reads `zpool list -Hp`. A short or odd line is skipped.
func ParseZPool(text string) []Space {
	var out []Space
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) < 10 {
			continue
		}
		capPct, err := strconv.ParseFloat(f[7], 64)
		if err != nil {
			continue
		}
		total, _ := strconv.ParseUint(f[1], 10, 64)
		used, _ := strconv.ParseUint(f[2], 10, 64)
		out = append(out, Space{
			Name: f[0], Kind: "pool", Total: total, Used: used, UsedPercent: capPct, Health: f[9],
		})
	}
	return out
}

func readZPools(ctx context.Context) []Space {
	if _, err := exec.LookPath("zpool"); err != nil {
		return nil
	}
	out, err := runCmd(ctx, 5*time.Second, "zpool", "list", "-Hp")
	if err != nil && strings.TrimSpace(out) == "" {
		return nil
	}
	return ParseZPool(out)
}

// ReadTemps reads hwmon, thermal zones, and nvidia-smi when they exist.
func ReadTemps(ctx context.Context) ([]Temp, error) {
	var out []Temp
	nvidia := readNVIDIA(ctx)
	out = append(out, nvidia...)
	haveNVIDIA := len(nvidia) > 0
	out = append(out, readHwmon("/sys/class/hwmon", haveNVIDIA)...)
	out = append(out, readThermal("/sys/class/thermal")...)
	return dedupeTemps(out), nil
}

func dedupeTemps(in []Temp) []Temp {
	seen := map[string]bool{}
	var out []Temp
	for _, t := range in {
		key := t.Kind + ":" + strings.ToLower(t.Name)
		if t.Name == "" || seen[key] || t.Celsius < 1 || t.Celsius > 150 {
			continue
		}
		seen[key] = true
		out = append(out, t)
	}
	return out
}

func readHwmon(root string, skipNVIDIA bool) []Temp {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []Temp
	for _, e := range entries {
		dir := filepath.Join(root, e.Name())
		name := strings.TrimSpace(readSmall(filepath.Join(dir, "name")))
		kind := sensorKind(name)
		if kind == "" || (skipNVIDIA && strings.Contains(strings.ToLower(name), "nvidia")) {
			continue
		}
		for i := 1; i <= 8; i++ {
			raw := readSmall(filepath.Join(dir, "temp"+strconv.Itoa(i)+"_input"))
			if raw == "" {
				continue
			}
			c, ok := milliC(raw)
			if !ok {
				continue
			}
			label := strings.TrimSpace(readSmall(filepath.Join(dir, "temp"+strconv.Itoa(i)+"_label")))
			sensor := name
			if label != "" {
				sensor = name + " " + label
			}
			out = append(out, Temp{Name: sensor, Kind: kind, Celsius: c})
		}
	}
	return out
}

func readThermal(root string) []Temp {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []Temp
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "thermal_zone") {
			continue
		}
		dir := filepath.Join(root, e.Name())
		kindName := strings.TrimSpace(readSmall(filepath.Join(dir, "type")))
		kind := sensorKind(kindName)
		if kind != "cpu" {
			continue
		}
		c, ok := milliC(readSmall(filepath.Join(dir, "temp")))
		if !ok {
			continue
		}
		out = append(out, Temp{Name: kindName, Kind: "cpu", Celsius: c})
	}
	return out
}

func sensorKind(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "amdgpu"), strings.Contains(n, "i915"), strings.Contains(n, "xe"),
		strings.Contains(n, "nouveau"), strings.Contains(n, "nvidia"), strings.Contains(n, "radeon"):
		return "gpu"
	case strings.Contains(n, "nvme"), strings.Contains(n, "drivetemp"), strings.Contains(n, "hdd"):
		return "disk"
	case strings.Contains(n, "coretemp"), strings.Contains(n, "k10temp"), strings.Contains(n, "zenpower"),
		strings.Contains(n, "k8temp"), strings.Contains(n, "cpu"), strings.Contains(n, "acpitz"),
		strings.Contains(n, "x86_pkg"):
		return "cpu"
	default:
		return ""
	}
}

func milliC(raw string) (float64, bool) {
	n, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return 0, false
	}
	c := n / 1000
	if c < 1 || c > 150 {
		return 0, false
	}
	return c, true
}

func readNVIDIA(ctx context.Context) []Temp {
	if _, err := exec.LookPath("nvidia-smi"); err != nil {
		return nil
	}
	out, err := runCmd(ctx, 4*time.Second, "nvidia-smi", "--query-gpu=name,temperature.gpu", "--format=csv,noheader,nounits")
	if err != nil && strings.TrimSpace(out) == "" {
		return nil
	}
	var temps []Temp
	for i, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, temp, ok := strings.Cut(line, ",")
		if !ok {
			continue
		}
		c, err := strconv.ParseFloat(strings.TrimSpace(temp), 64)
		if err != nil || c < 1 || c > 150 {
			continue
		}
		label := strings.TrimSpace(name)
		if label == "" {
			label = "NVIDIA " + strconv.Itoa(i)
		}
		temps = append(temps, Temp{Name: label, Kind: "gpu", Celsius: c})
	}
	return temps
}

// ReadSMART runs smartctl against whole disks. A missing smartctl is Unavailable.
// Standby disks are not woken.
func ReadSMART(ctx context.Context) ([]DiskSMART, error) {
	bin, err := exec.LookPath("smartctl")
	if err != nil {
		return nil, Unavailable("smartctl is not installed, so disk health is not being read.")
	}
	devs := listBlock("/sys/block")
	if len(devs) == 0 {
		return nil, Unavailable("No whole disks were found under /sys/block.")
	}
	var out []DiskSMART
	for _, dev := range devs {
		out = append(out, readOneSMART(ctx, bin, dev))
	}
	return out, nil
}

func readOneSMART(ctx context.Context, bin, dev string) DiskSMART {
	d := DiskSMART{Device: dev, Available: false}
	out, err := runCmd(ctx, 8*time.Second, bin, "-j", "-n", "standby", "-a", dev)
	if strings.TrimSpace(out) == "" {
		d.Message = dev + " did not answer smartctl."
		if err != nil {
			d.Message = dev + " could not be read."
		}
		return d
	}
	parsed, perr := ParseSMART([]byte(out))
	if perr != nil {
		d.Message = dev + " returned SMART data FELDCORE could not read."
		return d
	}
	if parsed.Device == "" {
		parsed.Device = dev
	}
	return parsed
}

// ParseSMART reads one smartctl JSON document.
func ParseSMART(raw []byte) (DiskSMART, error) {
	var doc smartDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return DiskSMART{}, err
	}
	d := DiskSMART{
		Device: doc.Device.Name, Model: doc.ModelName, Available: true, Attributes: []Attribute{},
	}
	for _, m := range doc.Smartctl.Messages {
		if strings.Contains(strings.ToLower(m.String), "standby") {
			d.Asleep = true
			d.Available = true
			return d, nil
		}
	}
	if doc.SmartStatus != nil {
		passed := doc.SmartStatus.Passed
		d.Passed = &passed
	}
	if doc.Temperature != nil && doc.Temperature.Current > 0 {
		c := float64(doc.Temperature.Current)
		d.TempC = &c
	}
	for _, a := range doc.ATA.Table {
		raw := a.Raw.String
		if raw == "" {
			raw = rawString(a.Raw.Value)
		}
		attr := Attribute{Name: a.Name, Value: strconv.Itoa(a.Value), Raw: raw, Threshold: a.Thresh}
		n := strings.ToLower(a.Name)
		switch {
		case n == "reallocated_sector_ct" || a.ID == 5:
			d.Reallocated = rawInt(a.Raw.Value)
			d.Attributes = append(d.Attributes, attr)
		case n == "current_pending_sector" || a.ID == 197:
			d.Pending = rawInt(a.Raw.Value)
			d.Attributes = append(d.Attributes, attr)
		case n == "offline_uncorrectable" || a.ID == 198:
			d.Offline = rawInt(a.Raw.Value)
			d.Attributes = append(d.Attributes, attr)
		case strings.Contains(n, "temperature") || a.ID == 194:
			if d.TempC == nil {
				c := float64(rawInt(a.Raw.Value))
				if c > 0 && c < 150 {
					d.TempC = &c
				}
			}
			d.Attributes = append(d.Attributes, attr)
		case strings.Contains(n, "wear") || strings.Contains(n, "media_wearout") || a.ID == 177 || a.ID == 233 || a.ID == 231:
			d.HasWear = true
			// These report remaining life in Value, not raw. Prefer the lower of value-as-used and raw.
			used := 100 - a.Value
			if used < 0 {
				used = 0
			}
			if strings.Contains(n, "percentage") || strings.Contains(n, "used") {
				used = int(rawInt(a.Raw.Value))
			}
			d.Wear = used
			d.Attributes = append(d.Attributes, attr)
		}
		if a.WhenFailed != "" && !hasAttr(d.Attributes, a.Name) {
			d.Attributes = append(d.Attributes, attr)
			if d.Passed == nil || *d.Passed {
				failed := false
				d.Passed = &failed
			}
		}
	}
	if doc.NVMe != nil {
		nv := doc.NVMe
		if d.TempC == nil && nv.Temperature > 0 {
			c := float64(nv.Temperature)
			d.TempC = &c
		}
		d.MediaErrors = nv.MediaErrors
		d.HasSpare = true
		d.Spare = nv.AvailableSpare
		d.SpareFloor = nv.AvailableSpareThreshold
		d.HasWear = true
		d.Wear = nv.PercentageUsed
		d.Attributes = append(d.Attributes,
			Attribute{Name: "Temperature", Raw: strconv.Itoa(nv.Temperature)},
			Attribute{Name: "Available spare", Value: strconv.Itoa(nv.AvailableSpare), Threshold: nv.AvailableSpareThreshold},
			Attribute{Name: "Percentage used", Value: strconv.Itoa(nv.PercentageUsed)},
			Attribute{Name: "Media errors", Raw: strconv.FormatInt(nv.MediaErrors, 10)},
		)
	}
	if len(d.Attributes) > 16 {
		d.Attributes = d.Attributes[:16]
	}
	if d.Passed == nil && doc.NVMe == nil && len(doc.ATA.Table) == 0 {
		d.Available = false
		if d.Device == "" {
			d.Device = "disk"
		}
		d.Message = d.Device + " did not report SMART status."
	}
	return d, nil
}

func hasAttr(attrs []Attribute, name string) bool {
	for _, a := range attrs {
		if a.Name == name {
			return true
		}
	}
	return false
}

type smartDoc struct {
	Device struct {
		Name string `json:"name"`
	} `json:"device"`
	ModelName   string `json:"model_name"`
	SmartStatus *struct {
		Passed bool `json:"passed"`
	} `json:"smart_status"`
	Temperature *struct {
		Current int `json:"current"`
	} `json:"temperature"`
	ATA struct {
		Table []struct {
			ID         int    `json:"id"`
			Name       string `json:"name"`
			Value      int    `json:"value"`
			Worst      int    `json:"worst"`
			Thresh     int    `json:"thresh"`
			WhenFailed string `json:"when_failed"`
			Raw        struct {
				Value  any    `json:"value"`
				String string `json:"string"`
			} `json:"raw"`
		} `json:"table"`
	} `json:"ata_smart_attributes"`
	NVMe *struct {
		Temperature             int   `json:"temperature"`
		AvailableSpare          int   `json:"available_spare"`
		AvailableSpareThreshold int   `json:"available_spare_threshold"`
		PercentageUsed          int   `json:"percentage_used"`
		MediaErrors             int64 `json:"media_errors"`
	} `json:"nvme_smart_health_information_log"`
	Smartctl struct {
		Messages []struct {
			String string `json:"string"`
		} `json:"messages"`
	} `json:"smartctl"`
}

func rawInt(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	case string:
		f := strings.Fields(n)
		if len(f) == 0 {
			return 0
		}
		i, _ := strconv.ParseInt(f[0], 10, 64)
		return i
	default:
		return 0
	}
}

func rawString(v any) string {
	switch n := v.(type) {
	case float64:
		return strconv.FormatInt(int64(n), 10)
	case string:
		return n
	default:
		return ""
	}
}

// listBlock returns whole-disk device paths. root is /sys/block on a real machine.
func listBlock(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if !wholeDisk(name) {
			continue
		}
		dev := "/dev/" + name
		if !devRe.MatchString(dev) {
			continue
		}
		out = append(out, dev)
		if len(out) >= 32 {
			break
		}
	}
	return out
}

func wholeDisk(name string) bool {
	switch {
	case strings.HasPrefix(name, "loop"), strings.HasPrefix(name, "ram"), strings.HasPrefix(name, "fd"),
		strings.HasPrefix(name, "sr"), strings.HasPrefix(name, "dm-"), strings.HasPrefix(name, "md"),
		strings.HasPrefix(name, "zram"), strings.HasPrefix(name, "nbd"), strings.HasPrefix(name, "zd"):
		return false
	case strings.HasPrefix(name, "sd"), strings.HasPrefix(name, "nvme"), strings.HasPrefix(name, "vd"),
		strings.HasPrefix(name, "hd"), strings.HasPrefix(name, "xvd"), strings.HasPrefix(name, "mmcblk"):
		return !strings.Contains(name, "p") || strings.HasPrefix(name, "sd") || strings.HasPrefix(name, "vd") || strings.HasPrefix(name, "hd") || strings.HasPrefix(name, "xvd")
	default:
		return false
	}
}

// nvme0n1 is a disk; nvme0n1p1 is a partition. mmcblk0 is a disk; mmcblk0p1 is not.
// The rule above treats any name containing "p" as a partition except sd/vd/hd/xvd
// (sdp is a disk). mmcblk0boot0 contains no "p" between... "mmcblk0boot0" doesn't match prefix only.
// nvme0n1 has no "p". nvme0n1p1 has "p". Good.
// mmcblk0 has no p. mmcblk0p1 has p. Good.

var devRe = regexp.MustCompile(`^/dev/(sd[a-z]+|nvme[0-9]+n[0-9]+|vd[a-z]+|hd[a-z]+|xvd[a-z]+|mmcblk[0-9]+)$`)

// ReadUnits lists failed systemd units. A missing systemctl is Unavailable.
func ReadUnits(ctx context.Context) ([]Unit, error) {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return nil, Unavailable("systemd is not available on this machine, so failed units are not checked.")
	}
	out, err := runCmd(ctx, 8*time.Second, "systemctl", "list-units", "--failed", "--plain", "--no-legend", "--no-pager")
	if err != nil && strings.TrimSpace(out) == "" {
		return nil, Unavailable("Failed units could not be listed.")
	}
	units := ParseUnits(out)
	active, _ := runCmd(ctx, 4*time.Second, "systemctl", "is-active", "nfs-server.service")
	return DropInactiveNFSHelpers(units, strings.TrimSpace(active) == "active"), nil
}

// nfsHelperUnits are NFS support daemons that fail harmlessly at boot when no
// export is configured. FELDCORE starts nfs-server on demand (see
// internal/shares), which pulls these in when it needs them, so their failure
// while nfs-server is not active is noise rather than a fault.
var nfsHelperUnits = map[string]bool{
	"nfsdcld.service":          true,
	"rpc-statd.service":        true,
	"rpc-statd-notify.service": true,
	"rpc-gssd.service":         true,
	"nfs-idmapd.service":       true,
	"nfs-mountd.service":       true,
	"fsidd.service":            true,
}

func nfsHelperUnit(name string) bool {
	name = strings.TrimSpace(name)
	if !strings.Contains(name, ".") {
		name += ".service"
	}
	return nfsHelperUnits[name]
}

// DropInactiveNFSHelpers removes NFS support units from a failed-unit list when
// nfs-server is not active. When NFS is in use, every failure is kept so a real
// fault still surfaces. nfs-server.service itself is never dropped.
func DropInactiveNFSHelpers(units []Unit, nfsActive bool) []Unit {
	if nfsActive {
		return units
	}
	out := make([]Unit, 0, len(units))
	for _, u := range units {
		if nfsHelperUnit(u.Name) {
			continue
		}
		out = append(out, u)
	}
	return out
}

// ParseUnits reads `systemctl list-units --failed --plain --no-legend`.
func ParseUnits(text string) []Unit {
	var out []Unit
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "UNIT ") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		// UNIT LOAD ACTIVE SUB DESCRIPTION...
		desc := ""
		if len(f) > 4 {
			desc = strings.Join(f[4:], " ")
		}
		if f[2] != "failed" && f[3] != "failed" {
			continue
		}
		out = append(out, Unit{Name: f[0], Description: desc})
	}
	return out
}

// ReadCerts walks dirs for PEM certificates. Missing directories are ignored.
func ReadCerts(ctx context.Context, dirs []string) ([]Cert, error) {
	var out []Cert
	seen := map[string]bool{}
	for _, dir := range dirs {
		if ctx.Err() != nil {
			break
		}
		_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if len(out) >= 40 {
				return filepath.SkipAll
			}
			ext := strings.ToLower(filepath.Ext(path))
			if ext != ".crt" && ext != ".pem" && ext != ".cer" {
				return nil
			}
			info, err := d.Info()
			if err != nil || info.Size() > 1<<20 {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			for _, c := range parsePEMs(raw) {
				name := certName(c, path)
				if seen[name] {
					continue
				}
				seen[name] = true
				out = append(out, Cert{Name: name, NotAfter: c.NotAfter})
			}
			return nil
		})
	}
	return out, nil
}

func parsePEMs(raw []byte) []*x509.Certificate {
	var out []*x509.Certificate
	rest := raw
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			continue
		}
		out = append(out, c)
	}
	return out
}

func certName(c *x509.Certificate, path string) string {
	if c.Subject.CommonName != "" {
		return c.Subject.CommonName
	}
	if len(c.DNSNames) > 0 {
		return c.DNSNames[0]
	}
	return filepath.Base(path)
}

func readSmall(path string) string {
	b, err := os.ReadFile(path)
	if err != nil || len(b) > 256 {
		return ""
	}
	return string(b)
}

func runCmd(ctx context.Context, limit time.Duration, name string, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	cmd := exec.CommandContext(cctx, name, args...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "LANG=C", "LC_ALL=C"}
	out, err := cmd.Output()
	return string(out), err
}
