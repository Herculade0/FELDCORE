package storage

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

func osMountList() []string {
	return []string{"/", "/boot", "/boot/efi", "/usr"}
}

// probeHost reads lsblk, /dev/disk/by-id and, when smartctl is installed, a
// SMART summary. It never writes.
func probeHost(ctx context.Context, run Executor, byIDDir string) ([]Disk, error) {
	if byIDDir == "" {
		byIDDir = "/dev/disk/by-id"
	}
	raw, err := run.Run(ctx, 8*time.Second, "lsblk", "-J", "-b", "-o",
		"NAME,PATH,TYPE,SIZE,MODEL,SERIAL,ROTA,TRAN,WWN,FSTYPE,UUID,MOUNTPOINTS,PKNAME")
	if err != nil {
		raw, err = run.Run(ctx, 8*time.Second, "lsblk", "-J", "-b", "-o",
			"NAME,PATH,TYPE,SIZE,MODEL,SERIAL,ROTA,TRAN,WWN,FSTYPE,UUID,MOUNTPOINT,PKNAME")
		if err != nil {
			return nil, opf("Could not list disks: %s", clip(err.Error(), 300))
		}
	}
	disks, err := parseLsblk([]byte(raw))
	if err != nil {
		return nil, op("Could not read the disk list.")
	}
	links := readByID(byIDDir)
	for i := range disks {
		d := &disks[i]
		if d.ByID == "" {
			d.ByID = links[d.Path]
		}
		if d.Serial == "" {
			d.Serial = sysfsText(d.Name, "device/serial", "serial")
		}
		if d.Model == "" {
			d.Model = sysfsText(d.Name, "device/model")
		}
		d.Serial = cleanID(d.Serial)
		d.Model = strings.TrimSpace(d.Model)
		if d.Model == "" && d.Type == "virtual" {
			d.Model = "Virtual disk"
		}
	}
	fillBlkid(ctx, run, disks)
	enrichSMART(ctx, run, disks)
	for i := range disks {
		applyIdentity(&disks[i])
		if t := sysfsTemp(disks[i].Name); t != nil && disks[i].TempC == nil {
			disks[i].TempC = t
			if disks[i].SMART.TempC == nil {
				disks[i].SMART.TempC = t
			}
		}
	}
	markOS(ctx, run, disks)
	sort.Slice(disks, func(i, j int) bool { return disks[i].Name < disks[j].Name })
	return disks, nil
}

func applyIdentity(d *Disk) {
	serial := cleanID(d.Serial)
	d.Serial = serial
	base := ""
	if d.ByID != "" {
		base = filepath.Base(d.ByID)
	}
	switch {
	case serial != "":
		d.Confirm = serial
		d.ConfirmKind = "serial"
		d.ID = "serial:" + serial
	case base != "" && base != "." && base != "/":
		d.Confirm = base
		d.ConfirmKind = "by-id"
		d.ID = "byid:" + base
	default:
		d.ID = "path:" + d.Path
	}
}

func cleanID(s string) string {
	s = strings.TrimSpace(s)
	if s == "-" || s == "null" {
		return ""
	}
	return s
}

type lsblkOut struct {
	Blockdevices []lsblkDev `json:"blockdevices"`
}

type lsblkDev struct {
	Name        string          `json:"name"`
	Path        string          `json:"path"`
	Type        string          `json:"type"`
	Size        json.RawMessage `json:"size"`
	Model       json.RawMessage `json:"model"`
	Serial      json.RawMessage `json:"serial"`
	Rota        json.RawMessage `json:"rota"`
	Tran        json.RawMessage `json:"tran"`
	FSType      json.RawMessage `json:"fstype"`
	UUID        json.RawMessage `json:"uuid"`
	Mountpoint  json.RawMessage `json:"mountpoint"`
	Mountpoints json.RawMessage `json:"mountpoints"`
	PKName      json.RawMessage `json:"pkname"`
	Children    []lsblkDev      `json:"children"`
}

func parseLsblk(raw []byte) ([]Disk, error) {
	var doc lsblkOut
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	var out []Disk
	for _, dev := range doc.Blockdevices {
		if d, ok := wholeDisk(dev); ok {
			out = append(out, d)
		}
	}
	return out, nil
}

func wholeDisk(dev lsblkDev) (Disk, bool) {
	kind := strings.ToLower(dev.Type)
	name := dev.Name
	if kind != "disk" && kind != "loop" {
		return Disk{}, false
	}
	if strings.HasPrefix(name, "zram") || strings.HasPrefix(name, "ram") {
		return Disk{}, false
	}
	if kind == "loop" && !loopBacked(name) {
		return Disk{}, false
	}
	path := dev.Path
	if path == "" {
		path = "/dev/" + name
	}
	d := Disk{
		Name:       name,
		Path:       path,
		Model:      jsonString(dev.Model),
		Serial:     jsonString(dev.Serial),
		SizeBytes:  jsonUint(dev.Size),
		Transport:  strings.ToLower(jsonString(dev.Tran)),
		FSType:     jsonString(dev.FSType),
		UUID:       jsonString(dev.UUID),
		Rotational: jsonBool(dev.Rota),
	}
	d.Type = mediaType(name, d.Transport, d.Rotational)
	var mounts []string
	mounts = append(mounts, mountList(dev.Mountpoint, dev.Mountpoints)...)
	for _, ch := range dev.Children {
		if strings.EqualFold(ch.Type, "part") || strings.EqualFold(ch.Type, "md") {
			d.Partitions++
		}
		for _, m := range mountList(ch.Mountpoint, ch.Mountpoints) {
			mounts = append(mounts, m)
		}
		if d.FSType == "" {
			d.FSType = jsonString(ch.FSType)
		}
	}
	own, children := splitMounts(mounts, dev)
	if len(own) > 0 {
		d.Mountpoint = own[0]
	}
	d.ChildMounts = children
	if osPath(append(own, children...)) {
		d.OS = true
	}
	return d, true
}

func splitMounts(mounts []string, dev lsblkDev) (own, children []string) {
	childSet := map[string]bool{}
	for _, ch := range dev.Children {
		for _, m := range mountList(ch.Mountpoint, ch.Mountpoints) {
			childSet[m] = true
		}
	}
	seen := map[string]bool{}
	for _, m := range mounts {
		if m == "" || seen[m] {
			continue
		}
		seen[m] = true
		if childSet[m] {
			children = append(children, m)
		} else {
			own = append(own, m)
		}
	}
	return own, children
}

func osPath(mounts []string) bool {
	for _, m := range mounts {
		for _, want := range osMountList() {
			if m == want {
				return true
			}
		}
	}
	return false
}

func mediaType(name, tran string, rota bool) string {
	switch {
	case strings.HasPrefix(name, "loop"):
		return "loop"
	case tran == "nvme" || strings.HasPrefix(name, "nvme"):
		return "nvme"
	case tran == "usb":
		if rota {
			return "hdd"
		}
		return "usb"
	case tran == "virtio" || strings.HasPrefix(name, "vd"):
		return "virtual"
	case rota:
		return "hdd"
	default:
		return "ssd"
	}
}

func mountList(one json.RawMessage, many json.RawMessage) []string {
	var out []string
	if s := jsonString(one); s != "" {
		out = append(out, s)
	}
	if len(many) == 0 {
		return out
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(many, &arr); err != nil {
		if s := jsonString(many); s != "" {
			out = append(out, s)
		}
		return out
	}
	for _, item := range arr {
		if s := jsonString(item); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func jsonString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	return strings.Trim(strings.TrimSpace(string(raw)), `"`)
}

func jsonUint(raw json.RawMessage) uint64 {
	s := jsonString(raw)
	if s == "" {
		return 0
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		f, ferr := strconv.ParseFloat(s, 64)
		if ferr != nil || f < 0 {
			return 0
		}
		return uint64(f)
	}
	return n
}

func jsonBool(raw json.RawMessage) bool {
	s := strings.ToLower(jsonString(raw))
	return s == "1" || s == "true"
}

func loopBacked(name string) bool {
	_, err := os.Stat(filepath.Join("/sys/block", name, "loop/backing_file"))
	return err == nil
}

func sysfsText(name string, rel ...string) string {
	for _, r := range rel {
		b, err := os.ReadFile(filepath.Join("/sys/block", name, r))
		if err == nil {
			if s := strings.TrimSpace(string(b)); s != "" {
				return s
			}
		}
	}
	return ""
}

func sysfsTemp(name string) *int {
	matches, _ := filepath.Glob(filepath.Join("/sys/block", name, "device/hwmon/hwmon*/temp*_input"))
	for _, p := range matches {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err != nil || n <= 0 {
			continue
		}
		c := n / 1000
		if c <= 0 || c > 150 {
			continue
		}
		return &c
	}
	return nil
}

func readByID(dir string) map[string]string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	type cand struct {
		path string
		rank int
	}
	best := map[string]cand{}
	for _, e := range entries {
		base := e.Name()
		if strings.Contains(base, "-part") {
			continue
		}
		full := filepath.Join(dir, base)
		target, err := filepath.EvalSymlinks(full)
		if err != nil || !strings.HasPrefix(target, "/dev/") {
			continue
		}
		rank := byIDRank(base)
		if cur, ok := best[target]; ok && cur.rank <= rank {
			continue
		}
		best[target] = cand{path: full, rank: rank}
	}
	out := make(map[string]string, len(best))
	for dev, c := range best {
		out[dev] = c.path
	}
	return out
}

func byIDRank(base string) int {
	switch {
	case strings.HasPrefix(base, "nvme-eui"):
		return 5
	case strings.HasPrefix(base, "nvme-"):
		return 0
	case strings.HasPrefix(base, "ata-"):
		return 1
	case strings.HasPrefix(base, "scsi-"):
		return 2
	case strings.HasPrefix(base, "wwn-"):
		return 3
	case strings.HasPrefix(base, "virtio-"):
		return 2
	case strings.HasPrefix(base, "usb-"):
		return 4
	default:
		return 6
	}
}

// fillBlkid reads a filesystem lsblk did not report. Some virtual disks keep
// their superblock out of the udev database, and blkid still sees it. This is
// read-only. A disk that already has a filesystem then shows a warning.
func fillBlkid(ctx context.Context, run Executor, disks []Disk) {
	if !run.Look("blkid") {
		return
	}
	for i := range disks {
		if disks[i].FSType != "" || disks[i].Path == "" {
			continue
		}
		out, err := run.Run(ctx, 5*time.Second, "blkid", "-o", "export", disks[i].Path)
		if err != nil && strings.TrimSpace(out) == "" {
			continue
		}
		fields := map[string]string{}
		for _, line := range strings.Split(out, "\n") {
			k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
			if ok && v != "" {
				fields[k] = v
			}
		}
		if disks[i].FSType == "" {
			disks[i].FSType = fields["TYPE"]
		}
		if disks[i].UUID == "" {
			disks[i].UUID = fields["UUID"]
		}
	}
}

func enrichSMART(ctx context.Context, run Executor, disks []Disk) {
	if !run.Look("smartctl") {
		msg := "smartctl is not installed. FELDCORE will not install it for you."
		for i := range disks {
			disks[i].SMART = SMART{Healthy: "unknown", Message: msg}
		}
		return
	}
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i := range disks {
		if disks[i].Type == "loop" {
			disks[i].SMART = SMART{Healthy: "unknown", Message: "Loop devices do not report SMART."}
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			sm, serial := readSMART(ctx, run, disks[i].Path, disks[i].Type)
			disks[i].SMART = sm
			if sm.TempC != nil {
				disks[i].TempC = sm.TempC
			}
			if disks[i].Serial == "" && serial != "" {
				disks[i].Serial = serial
			}
		}(i)
	}
	wg.Wait()
}

func readSMART(ctx context.Context, run Executor, dev, kind string) (SMART, string) {
	out, err := run.Run(ctx, 8*time.Second, "smartctl", "-n", "standby", "-j", "-H", "-i", "-A", dev)
	sm, serial, parsed := parseSMART([]byte(out))
	if !parsed {
		if kind == "virtual" {
			return SMART{Healthy: "unknown", Message: "This virtual disk does not report SMART."}, ""
		}
		msg := "SMART could not be read."
		if err != nil {
			msg = clip(err.Error(), 200)
		}
		return SMART{Healthy: "unknown", Message: msg}, ""
	}
	if !sm.Available && sm.Message == "" && kind == "virtual" {
		sm.Message = "This virtual disk does not report SMART."
		sm.Healthy = "unknown"
	}
	return sm, serial
}

type smartDoc struct {
	Smartctl struct {
		Messages []struct {
			String string `json:"string"`
		} `json:"messages"`
		Exit int `json:"exit_status"`
	} `json:"smartctl"`
	Serial      string `json:"serial_number"`
	Model       string `json:"model_name"`
	SmartStatus *struct {
		Passed bool `json:"passed"`
	} `json:"smart_status"`
	Temperature *struct {
		Current int `json:"current"`
	} `json:"temperature"`
	PowerOn *struct {
		Hours int64 `json:"hours"`
	} `json:"power_on_time"`
	ATA *struct {
		Table []struct {
			ID  int `json:"id"`
			Raw struct {
				Value int64 `json:"value"`
			} `json:"raw"`
		} `json:"table"`
	} `json:"ata_smart_attributes"`
	NVMe *struct {
		Temperature  int   `json:"temperature"`
		PowerOnHours int64 `json:"power_on_hours"`
		MediaErrors  int64 `json:"media_errors"`
	} `json:"nvme_smart_health_information_log"`
}

func parseSMART(raw []byte) (SMART, string, bool) {
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || raw[0] != '{' {
		return SMART{}, "", false
	}
	var doc smartDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return SMART{}, "", false
	}
	sm := SMART{Healthy: "unknown"}
	if doc.SmartStatus != nil {
		sm.Available = true
		if doc.SmartStatus.Passed {
			sm.Healthy = "passed"
		} else {
			sm.Healthy = "failed"
		}
	}
	if doc.Temperature != nil && doc.Temperature.Current > 0 && doc.Temperature.Current < 150 {
		c := doc.Temperature.Current
		sm.TempC = &c
	}
	if doc.PowerOn != nil && doc.PowerOn.Hours > 0 {
		h := doc.PowerOn.Hours
		sm.PowerOnHours = &h
	}
	if doc.ATA != nil {
		for _, row := range doc.ATA.Table {
			v := row.Raw.Value
			switch row.ID {
			case 5:
				sm.Reallocated = &v
				sm.Available = true
			case 9:
				if sm.PowerOnHours == nil && v > 0 {
					sm.PowerOnHours = &v
				}
			case 197:
				sm.Pending = &v
				sm.Available = true
			case 194:
				if sm.TempC == nil && v > 0 && v < 150 {
					c := int(v)
					sm.TempC = &c
				}
			}
		}
	}
	if doc.NVMe != nil {
		sm.Available = true
		if doc.NVMe.Temperature > 0 && doc.NVMe.Temperature < 150 {
			c := doc.NVMe.Temperature
			sm.TempC = &c
		}
		if doc.NVMe.PowerOnHours > 0 {
			h := doc.NVMe.PowerOnHours
			sm.PowerOnHours = &h
		}
		m := doc.NVMe.MediaErrors
		sm.MediaErrors = &m
	}
	if !sm.Available {
		for _, m := range doc.Smartctl.Messages {
			if strings.TrimSpace(m.String) != "" {
				sm.Message = clip(m.String, 200)
				break
			}
		}
		if sm.Message == "" {
			sm.Message = "This disk did not report a SMART status."
		}
	}
	return sm, cleanID(doc.Serial), true
}

func markOS(ctx context.Context, run Executor, disks []Disk) {
	sources := map[string]bool{}
	for _, m := range osMountList() {
		out, err := run.Run(ctx, 3*time.Second, "findmnt", "-n", "-o", "SOURCE", m)
		if err != nil {
			continue
		}
		src := strings.TrimSpace(strings.Split(out, "\n")[0])
		if src == "" {
			continue
		}
		if r, err := filepath.EvalSymlinks(src); err == nil {
			src = r
		}
		sources[src] = true
		if parent := parentDisk(src); parent != "" {
			sources[parent] = true
		}
	}
	if len(sources) == 0 {
		return
	}
	for i := range disks {
		if sources[disks[i].Path] {
			disks[i].OS = true
		}
	}
}

func parentDisk(dev string) string {
	name := filepath.Base(dev)
	// /sys/class/block/sda1/partition exists for partitions. The parent is the
	// first directory upward that is not a partition.
	link, err := os.Readlink(filepath.Join("/sys/class/block", name))
	if err != nil {
		return ""
	}
	abs := link
	if !strings.HasPrefix(abs, "/") {
		abs = filepath.Join("/sys/class/block", link)
	}
	abs = filepath.Clean(abs)
	// .../block/sda/sda1 -> sda
	parent := filepath.Base(filepath.Dir(abs))
	if parent == "" || parent == name || parent == "block" || parent == "." {
		return ""
	}
	return "/dev/" + parent
}
