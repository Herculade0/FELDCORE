package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// answerVolume is one FAT or exFAT directory the answer-file scan may read.
// Dev is the block device, for error text. Mount is set only when this
// process mounted Dir and must unmount it. Tests leave Mount empty.
type answerVolume struct {
	Dir   string
	Dev   string
	Mount string
}

const (
	answerNone = iota
	answerBad
	answerOK
)

// answerVolumeEligible is the volume rule shared by the installer and the
// daemon. ISO 9660 is not a candidate. A virtio disk in the QEMU test is not
// removable and not USB, so it uses the FC-ANSWERS label. FAT stores 11
// characters and exFAT stores 15, so the design's FELDCORE-ANSWERS cannot be
// written; it is still accepted when a filesystem reports that exact string.
func answerVolumeEligible(fsType, label, removable, bus string) bool {
	switch fsType {
	case "vfat", "exfat":
	default:
		return false
	}
	return removable == "1" || bus == "usb" || label == "FC-ANSWERS" || label == "FELDCORE-ANSWERS"
}

// answerProbe accepts one JSON object whose version is the number 1.
// Unknown fields are allowed here. parseAnswer rejects them later.
func answerProbe(b []byte) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var obj map[string]json.RawMessage
	if err := dec.Decode(&obj); err != nil {
		return err
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return errTrailing
		}
		return err
	}
	raw, ok := obj["version"]
	if !ok || !jsonNumber(raw) {
		return errVersion
	}
	var num json.Number
	if err := json.Unmarshal(raw, &num); err != nil {
		return errVersion
	}
	f, err := num.Float64()
	if err != nil || f != 1 {
		return errVersion
	}
	return nil
}

var (
	errVersion  = &InputError{"The install answer file is not a version 1 JSON object."}
	errTrailing = &InputError{"The install answer file has more than one JSON value."}
)

func jsonNumber(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	if raw[0] == '-' {
		return len(raw) > 1 && raw[1] >= '0' && raw[1] <= '9'
	}
	return raw[0] >= '0' && raw[0] <= '9'
}

func answerCandidate(dir string) (path, rel string, status int) {
	nested := filepath.Join(dir, "feldcore", answerName)
	root := filepath.Join(dir, answerName)
	if st, err := os.Stat(nested); err == nil && !st.IsDir() {
		path, rel = nested, "feldcore/"+answerName
	} else if st, err := os.Stat(root); err == nil && !st.IsDir() {
		path, rel = root, answerName
	} else {
		return "", "", answerNone
	}
	b, err := os.ReadFile(path)
	if err != nil || answerProbe(b) != nil {
		return path, rel, answerBad
	}
	return path, rel, answerOK
}

func (s *Service) readAnswerVolumes(ctx context.Context) ([]byte, string, error) {
	var vols []answerVolume
	var err error
	if s.answerVolumes != nil {
		vols, err = s.answerVolumes(ctx)
	} else {
		vols, err = s.scanSysfsAnswerVolumes(ctx)
	}
	if err != nil {
		s.releaseAnswerVols(ctx, vols, "")
		return nil, "", err
	}
	type hit struct {
		vol  answerVolume
		path string
		rel  string
	}
	var hits []hit
	for _, vol := range vols {
		path, rel, st := answerCandidate(vol.Dir)
		switch st {
		case answerNone:
			continue
		case answerBad:
			s.releaseAnswerVols(ctx, vols, "")
			dev := vol.Dev
			if dev == "" {
				dev = vol.Dir
			}
			return nil, "", &InputError{"The install answer file on " + dev + " is not a version 1 JSON object (" + rel + ")."}
		default:
			hits = append(hits, hit{vol: vol, path: path, rel: rel})
		}
	}
	if len(hits) > 1 {
		s.releaseAnswerVols(ctx, vols, "")
		names := make([]string, 0, len(hits))
		for _, h := range hits {
			if h.vol.Dev != "" {
				names = append(names, h.vol.Dev)
			} else {
				names = append(names, h.path)
			}
		}
		return nil, "", &InputError{"More than one install answer file was found: " + strings.Join(names, " and ") + "."}
	}
	if len(hits) == 0 {
		s.releaseAnswerVols(ctx, vols, "")
		return nil, "", nil
	}
	h := hits[0]
	b, err := os.ReadFile(h.path)
	if err != nil {
		s.releaseAnswerVols(ctx, vols, "")
		return nil, "", err
	}
	s.answerSrc = h.path
	s.answerMount = h.vol.Mount
	s.releaseAnswerVols(ctx, vols, h.vol.Mount)
	return b, h.path, nil
}

// releaseAnswerVols unmounts mounts this process created. keep stays mounted
// so a later delete can still reach the file.
func (s *Service) releaseAnswerVols(ctx context.Context, vols []answerVolume, keep string) {
	for _, vol := range vols {
		if vol.Mount == "" || vol.Mount == keep {
			continue
		}
		s.unmountPath(ctx, vol.Mount)
	}
}

func (s *Service) unmountAnswer(ctx context.Context) {
	if s.answerMount == "" {
		return
	}
	mnt := s.answerMount
	s.answerMount = ""
	s.unmountPath(ctx, mnt)
}

func (s *Service) unmountPath(ctx context.Context, mnt string) {
	if mnt == "" || mnt == "/" {
		return
	}
	if s.cmd != nil {
		_, _ = s.cmd.Run(ctx, "umount", mnt)
	}
	_ = os.Remove(mnt)
}

func (s *Service) scanSysfsAnswerVolumes(ctx context.Context) ([]answerVolume, error) {
	ents, err := os.ReadDir("/sys/class/block")
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	skip := s.isoVolumeDisks(ctx)
	var names []string
	for _, ent := range ents {
		name := ent.Name()
		if skipBlock(name) {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	if err := os.MkdirAll("/run/feldcore/answer-scan", 0o755); err != nil {
		return nil, err
	}
	var mounted []answerVolume
	for _, name := range names {
		vol, ok, err := s.probeAnswerBlock(ctx, name, skip)
		if err != nil {
			s.releaseAnswerVols(ctx, mounted, "")
			return nil, err
		}
		if ok {
			mounted = append(mounted, vol)
		}
	}
	return mounted, nil
}

func skipBlock(name string) bool {
	switch {
	case strings.HasPrefix(name, "loop"),
		strings.HasPrefix(name, "ram"),
		strings.HasPrefix(name, "dm-"),
		strings.HasPrefix(name, "sr"),
		strings.HasPrefix(name, "fd"),
		strings.HasPrefix(name, "nbd"),
		strings.HasPrefix(name, "zram"):
		return true
	default:
		return false
	}
}

// isoVolumeDisks is the ISO 9660 device the live initrd mounted. A loop's
// backing disk is not included: that is the Ventoy stick, and the answer
// file may be on its data partition.
func (s *Service) isoVolumeDisks(ctx context.Context) map[string]bool {
	out := map[string]bool{}
	src := cutDev(s.cmdText(ctx, "findmnt", "-n", "-o", "SOURCE", "/run/feldcore/medium"))
	if src == "" {
		return out
	}
	if strings.HasPrefix(filepath.Base(src), "loop") {
		out[src] = true
		return out
	}
	out[s.wholeDisk(ctx, src)] = true
	return out
}

func (s *Service) probeAnswerBlock(ctx context.Context, name string, skip map[string]bool) (answerVolume, bool, error) {
	parent := blockParent(name)
	dev := "/dev/" + name
	parentDev := "/dev/" + parent
	if skip[dev] || skip[parentDev] {
		return answerVolume{}, false, nil
	}
	fs, label, ok := s.blkidType(ctx, dev)
	if !ok {
		return answerVolume{}, false, nil
	}
	rem := sysfsLine("/sys/class/block/" + parent + "/removable")
	bus := s.udevBus(ctx, parentDev)
	if !answerVolumeEligible(fs, label, rem, bus) {
		return answerVolume{}, false, nil
	}
	mnt := filepath.Join("/run/feldcore/answer-scan", safeBlock(name))
	if err := os.MkdirAll(mnt, 0o755); err != nil {
		return answerVolume{}, false, err
	}
	if _, err := s.cmd.Run(ctx, "mount", "-o", "ro", "-t", fs, dev, mnt); err != nil {
		_ = os.Remove(mnt)
		if s.log != nil {
			s.log.Warn("could not read a volume while looking for the install answer file", "dev", dev)
		}
		return answerVolume{}, false, nil
	}
	return answerVolume{Dir: mnt, Dev: dev, Mount: mnt}, true, nil
}

func (s *Service) blkidType(ctx context.Context, dev string) (fs, label string, ok bool) {
	text, err := s.cmd.Run(ctx, "blkid", "-o", "export", "-c", "/dev/null", dev)
	if err != nil {
		return "", "", false
	}
	for _, line := range strings.Split(text, "\n") {
		key, val, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		switch key {
		case "TYPE":
			fs = val
		case "LABEL":
			label = val
		}
	}
	return fs, label, fs != ""
}

func (s *Service) udevBus(ctx context.Context, dev string) string {
	text := s.cmdText(ctx, "udevadm", "info", "-q", "property", "-n", dev)
	for _, line := range strings.Split(text, "\n") {
		if bus, ok := strings.CutPrefix(line, "ID_BUS="); ok {
			return bus
		}
	}
	return ""
}

func (s *Service) wholeDisk(ctx context.Context, dev string) string {
	base := filepath.Base(dev)
	if base == "" || base == "." || base == "/" {
		return dev
	}
	if _, err := os.Stat("/sys/class/block/" + base + "/partition"); err != nil {
		return dev
	}
	parent := blockParent(base)
	if parent == "" || parent == base {
		pk := s.cmdText(ctx, "lsblk", "-no", "PKNAME", dev)
		pk = strings.TrimSpace(strings.Split(pk, "\n")[0])
		if pk != "" {
			return "/dev/" + pk
		}
		return dev
	}
	return "/dev/" + parent
}

func (s *Service) cmdText(ctx context.Context, name string, args ...string) string {
	if s.cmd == nil {
		return ""
	}
	out, err := s.cmd.Run(ctx, name, args...)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func blockParent(name string) string {
	if _, err := os.Stat("/sys/class/block/" + name + "/partition"); err != nil {
		return name
	}
	link, err := filepath.EvalSymlinks("/sys/class/block/" + name)
	if err != nil {
		return name
	}
	parent := filepath.Base(filepath.Dir(link))
	if parent == "" || parent == "." || parent == string(filepath.Separator) {
		return name
	}
	return parent
}

func sysfsLine(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func cutDev(src string) string {
	src = strings.TrimSpace(src)
	if i := strings.IndexAny(src, " ["); i >= 0 {
		src = src[:i]
	}
	return src
}

func safeBlock(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "vol"
	}
	return b.String()
}
