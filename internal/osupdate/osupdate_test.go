package osupdate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Herculade0/feldcore/internal/store"
)

func TestMinisignRoundTrip(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	pub := key.Public()
	data := []byte("{\"version\":\"0.2.0\"}\n")
	sig, err := Sign(key, "feldcore-os.json", data)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(pub, data, sig); err != nil {
		t.Fatal(err)
	}
	again, err := ParsePrivate(MarshalPrivate(key))
	if err != nil {
		t.Fatal(err)
	}
	if again.ID() != key.ID() {
		t.Fatalf("key id %s != %s", again.ID(), key.ID())
	}
	parsed, err := ParsePublic(MarshalPublic(pub))
	if err != nil || parsed.ID() != pub.ID() {
		t.Fatalf("public key: %v %s", err, parsed.ID())
	}
	bad := append([]byte{}, data...)
	bad[2] ^= 0x01
	if err := Verify(pub, bad, sig); err == nil {
		t.Fatal("tampered manifest verified")
	}
	other, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(other.Public(), data, sig); err == nil {
		t.Fatal("a different key verified the signature")
	}
}

func TestMinisignCLI(t *testing.T) {
	bin, err := exec.LookPath("minisign")
	if err != nil {
		t.Skip("minisign is not installed")
	}
	dir := t.TempDir()
	key, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	pubPath := filepath.Join(dir, "update.pub")
	if err := os.WriteFile(pubPath, []byte(MarshalPublic(key.Public())), 0o644); err != nil {
		t.Fatal(err)
	}
	msg := filepath.Join(dir, "feldcore-os.json")
	body := []byte("{\"version\":\"0.2.0\",\"notes\":\"hello\"}\n")
	if err := os.WriteFile(msg, body, 0o644); err != nil {
		t.Fatal(err)
	}
	sig, err := Sign(key, "feldcore-os.json", body)
	if err != nil {
		t.Fatal(err)
	}
	sigPath := msg + ".minisig"
	if err := os.WriteFile(sigPath, sig, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-V", "-p", pubPath, "-m", msg, "-x", sigPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minisign -V: %v\n%s", err, out)
	}

	sec := filepath.Join(dir, "minisign.key")
	theirPub := filepath.Join(dir, "minisign.pub")
	gen := exec.Command(bin, "-G", "-W", "-p", theirPub, "-s", sec)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("minisign -G: %v\n%s", err, out)
	}
	theirMsg := filepath.Join(dir, "theirs.json")
	if err := os.WriteFile(theirMsg, []byte("signed by minisign\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sign := exec.Command(bin, "-S", "-W", "-s", sec, "-m", theirMsg, "-x", theirMsg+".minisig")
	if out, err := sign.CombinedOutput(); err != nil {
		t.Fatalf("minisign -S: %v\n%s", err, out)
	}
	raw, _ := os.ReadFile(theirMsg)
	sigRaw, _ := os.ReadFile(theirMsg + ".minisig")
	pubRaw, _ := os.ReadFile(theirPub)
	pub, err := ParsePublic(string(pubRaw))
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(pub, raw, sigRaw); err != nil {
		t.Fatalf("our verifier rejected minisign: %v\n%s", err, sigRaw)
	}
}

func TestNextBootFallback(t *testing.T) {
	a := ParseEntryBody(mustEntry(t, "feldcore-a.conf"), EntryBody("a", "0.1.0"))
	fresh := ParseEntryBody(mustEntry(t, "feldcore-b+3.conf"), EntryBody("b", "0.2.0"))
	failed := ParseEntryBody(mustEntry(t, "feldcore-b+0-3.conf"), EntryBody("b", "0.2.0"))
	got, ok := NextBoot("", []Entry{a, fresh})
	if !ok || got.Name != "feldcore-b+3.conf" {
		t.Fatalf("new image should boot first: %+v", got)
	}
	got, ok = NextBoot("feldcore-b+3.conf", []Entry{a, failed})
	if !ok || got.Slot != "a" {
		t.Fatalf("a failed default must fall back: %+v", got)
	}
	got, ok = NextBoot("feldcore-a.conf", []Entry{a, fresh})
	if !ok || got.Name != "feldcore-a.conf" {
		t.Fatalf("rollback pin must win: %+v", got)
	}
	if compareVersion("0.10.0", "0.9.0") <= 0 {
		t.Fatal("0.10 should sort after 0.9")
	}
}

func mustEntry(t *testing.T, name string) Entry {
	t.Helper()
	e, ok := ParseEntryName(name)
	if !ok {
		t.Fatal(name)
	}
	return e
}

func TestUpdateURLBlocked(t *testing.T) {
	blocked := []string{
		"http://127.0.0.1/feldcore-os.json",
		"http://169.254.169.254/latest",
		"https://169.254.169.254/latest",
		"https://metadata.google.internal/x",
		"file:///etc/passwd",
		"https://user:pass@github.com/x",
		"https://localhost/x",
	}
	for _, u := range blocked {
		if err := validateUpdateURL(u, false); err == nil {
			t.Errorf("accepted %s", u)
		}
	}
	if err := validateUpdateURL("http://127.0.0.1:9/feldcore-os.json", true); err != nil {
		t.Fatal(err)
	}
	if err := validateUpdateURL("https://github.com/Herculade0/FELDCORE/releases/latest/download/feldcore-os.json", false); err != nil {
		t.Fatal(err)
	}
}

func TestStageAndRollback(t *testing.T) {
	svc, _ := newAppliance(t, "0.1.0", "a")
	seedEntry(t, svc.ESP, "a", "0.1.0", 0)
	files := releaseFiles(t, "0.2.0", "notes for 0.2.0")
	srv := httptest.NewServer(files.handler())
	defer srv.Close()
	svc.ManifestURL = srv.URL + "/feldcore-os.json"
	svc.AllowLocal = true
	if err := os.WriteFile(filepath.Join(svc.DataDir, "update.pub"), []byte(MarshalPublic(files.pub)), 0o644); err != nil {
		t.Fatal(err)
	}

	rel, err := svc.Stage(context.Background(), func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if rel.Version != "0.2.0" || !strings.Contains(rel.Notes, "0.2.0") {
		t.Fatalf("%+v", rel)
	}
	entry, err := os.ReadFile(filepath.Join(svc.ESP, "loader", "entries", "feldcore-b+3.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(entry), "feldcore.slot=b") || !strings.Contains(string(entry), "version 0.2.0") {
		t.Fatalf("entry:\n%s", entry)
	}
	loader, _ := os.ReadFile(filepath.Join(svc.ESP, "loader", "loader.conf"))
	if strings.Contains(string(loader), "default ") {
		t.Fatalf("stage pinned a default:\n%s", loader)
	}
	root, _ := os.ReadFile(svc.SlotB)
	if !bytes.HasPrefix(root, files.bodies["feldcore-root.img"]) {
		t.Fatal("slot B does not start with the image")
	}
	st := svc.Status(context.Background())
	if st.NextBoot != "b" || st.NextVersion != "0.2.0" || st.NextTries == nil || *st.NextTries != 3 {
		t.Fatalf("status next %+v tries %v", st, st.NextTries)
	}
	if st.Available == nil || st.Available.Version != "0.2.0" {
		t.Fatalf("available %+v", st.Available)
	}

	info, err := svc.Rollback(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != "a" {
		t.Fatalf("rollback target %+v", info)
	}
	loader, _ = os.ReadFile(filepath.Join(svc.ESP, "loader", "loader.conf"))
	if !strings.Contains(string(loader), "default feldcore-a.conf") {
		t.Fatalf("loader:\n%s", loader)
	}
	st = svc.Status(context.Background())
	if st.NextBoot != "a" {
		t.Fatalf("next boot %s", st.NextBoot)
	}
}

func TestBadHashDoesNotPublish(t *testing.T) {
	svc, _ := newAppliance(t, "0.1.0", "a")
	files := releaseFiles(t, "0.2.0", "bad")
	files.bodies["feldcore-root.img"] = []byte("this is not the signed image")
	srv := httptest.NewServer(files.handler())
	defer srv.Close()
	svc.ManifestURL = srv.URL + "/feldcore-os.json"
	svc.AllowLocal = true
	if err := os.WriteFile(filepath.Join(svc.DataDir, "update.pub"), []byte(MarshalPublic(files.pub)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Stage(context.Background(), func(string) {}); err == nil {
		t.Fatal("expected a hash mismatch")
	}
	if _, err := os.Stat(filepath.Join(svc.ESP, "loader", "entries", "feldcore-b+3.conf")); !os.IsNotExist(err) {
		t.Fatal("a bad download published a boot entry")
	}
}

func TestBadSignature(t *testing.T) {
	svc, _ := newAppliance(t, "0.1.0", "a")
	files := releaseFiles(t, "0.2.0", "signed")
	raw := files.bodies["feldcore-os.json"]
	raw[10] ^= 0x01
	files.bodies["feldcore-os.json"] = raw
	srv := httptest.NewServer(files.handler())
	defer srv.Close()
	svc.ManifestURL = srv.URL + "/feldcore-os.json"
	svc.AllowLocal = true
	if err := os.WriteFile(filepath.Join(svc.DataDir, "update.pub"), []byte(MarshalPublic(files.pub)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Check(context.Background()); err == nil {
		t.Fatal("expected a bad signature")
	}
}

func TestScheduleOnce(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	svc := &Service{Store: st, Now: func() time.Time { return now }}
	if err := svc.SetSchedule("not-a-time"); err == nil {
		t.Fatal("accepted a bad time")
	}
	if err := svc.SetSchedule(now.Add(-time.Minute).Format(time.RFC3339)); err == nil {
		t.Fatal("accepted the past")
	}
	when := now.Add(time.Hour).Format(time.RFC3339)
	if err := svc.SetSchedule(when); err != nil {
		t.Fatal(err)
	}
	if svc.TakeSchedule() {
		t.Fatal("fired early")
	}
	now = now.Add(2 * time.Hour)
	if !svc.TakeSchedule() {
		t.Fatal("did not fire")
	}
	if svc.TakeSchedule() {
		t.Fatal("fired twice")
	}
	if st.OSConfig().ScheduleAt != "" {
		t.Fatal("schedule was kept")
	}
}

func TestSysextInstallAndRemove(t *testing.T) {
	svc, _ := newAppliance(t, "0.1.0", "a")
	files := releaseFiles(t, "0.2.0", "ext")
	blob := []byte("raw-sysext-bytes")
	sum := sha256.Sum256(blob)
	files.manifest.Sysexts = []SysextArtifact{{
		ID: "libvirt", Version: "1", Name: "libvirt.raw",
		SHA256: hex.EncodeToString(sum[:]), Size: int64(len(blob)),
		Notes: "userspace",
	}}
	raw, err := MarshalManifest(files.manifest)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := Sign(files.key, "feldcore-os.json", raw)
	if err != nil {
		t.Fatal(err)
	}
	files.bodies["feldcore-os.json"] = raw
	files.bodies["feldcore-os.json.minisig"] = sig
	files.bodies["libvirt.raw"] = blob
	srv := httptest.NewServer(files.handler())
	defer srv.Close()
	svc.ManifestURL = srv.URL + "/feldcore-os.json"
	svc.AllowLocal = true
	if err := os.WriteFile(filepath.Join(svc.DataDir, "update.pub"), []byte(MarshalPublic(files.pub)), 0o644); err != nil {
		t.Fatal(err)
	}
	var refreshed int
	svc.RefreshFn = func(context.Context) error {
		refreshed++
		return nil
	}
	if err := svc.InstallSysext(context.Background(), "libvirt", func(string) {}); err != nil {
		t.Fatal(err)
	}
	if refreshed != 1 {
		t.Fatalf("refresh %d", refreshed)
	}
	got, err := os.ReadFile(filepath.Join(svc.DataDir, "sysexts", "libvirt.raw"))
	if err != nil || !bytes.Equal(got, blob) {
		t.Fatalf("raw %v %q", err, got)
	}
	list := svc.listSysexts("6.12.0")
	var found bool
	for _, sx := range list {
		if sx.ID == "libvirt" && sx.Installed && sx.Available && sx.Source == "state" {
			found = true
		}
	}
	if !found {
		t.Fatalf("list %+v", list)
	}
	if err := svc.RemoveSysext(context.Background(), "libvirt"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(svc.DataDir, "sysexts", "libvirt.raw")); !os.IsNotExist(err) {
		t.Fatal("extension file remains")
	}
	// A relocated root must not call the host's systemd-sysext.
	svc.RefreshFn = nil
	if err := svc.InstallSysext(context.Background(), "libvirt", func(string) {}); err != nil {
		t.Fatal(err)
	}
}

func TestKernelMismatch(t *testing.T) {
	svc, _ := newAppliance(t, "0.1.0", "a")
	if err := os.WriteFile(filepath.Join(svc.Root, "proc", "sys", "kernel", "osrelease"), []byte("6.12.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files := releaseFiles(t, "0.2.0", "gpu")
	blob := []byte("nvidia")
	sum := sha256.Sum256(blob)
	files.manifest.Sysexts = []SysextArtifact{{
		ID: "nvidia", Version: "1", Kernel: "9.9.9", Name: "nvidia.raw",
		SHA256: hex.EncodeToString(sum[:]), Size: int64(len(blob)),
	}}
	raw, err := MarshalManifest(files.manifest)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := Sign(files.key, "feldcore-os.json", raw)
	if err != nil {
		t.Fatal(err)
	}
	files.bodies["feldcore-os.json"] = raw
	files.bodies["feldcore-os.json.minisig"] = sig
	files.bodies["nvidia.raw"] = blob
	srv := httptest.NewServer(files.handler())
	defer srv.Close()
	svc.ManifestURL = srv.URL + "/feldcore-os.json"
	svc.AllowLocal = true
	if err := os.WriteFile(filepath.Join(svc.DataDir, "update.pub"), []byte(MarshalPublic(files.pub)), 0o644); err != nil {
		t.Fatal(err)
	}
	err = svc.InstallSysext(context.Background(), "nvidia", func(string) {})
	if err == nil || !strings.Contains(err.Error(), "9.9.9") {
		t.Fatalf("got %v", err)
	}
}

type releaseSet struct {
	key      PrivateKey
	pub      PublicKey
	manifest Manifest
	bodies   map[string][]byte
}

func (r releaseSet) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		name := filepath.Base(req.URL.Path)
		b, ok := r.bodies[name]
		if !ok {
			http.NotFound(w, req)
			return
		}
		_, _ = w.Write(b)
	})
}

func releaseFiles(t *testing.T, version, notes string) releaseSet {
	t.Helper()
	key, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	bodies := map[string][]byte{
		"feldcore-root.img": []byte("root-image-" + version),
		"vmlinuz":           []byte("kernel-" + version),
		"initrd.img":        []byte("initrd-" + version),
	}
	m := Manifest{Release: Release{Version: version, Notes: notes, Arch: "amd64", Channel: "stable"}}
	for name, kind := range map[string]string{
		"feldcore-root.img": kindRoot,
		"vmlinuz":           kindKernel,
		"initrd.img":        kindInitrd,
	} {
		sum := sha256.Sum256(bodies[name])
		m.Files = append(m.Files, Artifact{
			Name: name, Kind: kind, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(bodies[name])),
		})
	}
	raw, err := MarshalManifest(m)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := Sign(key, "feldcore-os.json", raw)
	if err != nil {
		t.Fatal(err)
	}
	bodies["feldcore-os.json"] = raw
	bodies["feldcore-os.json.minisig"] = sig
	return releaseSet{key: key, pub: key.Public(), manifest: m, bodies: bodies}
}

func seedEntry(t *testing.T, esp, slot, version string, tries int) {
	t.Helper()
	dir := filepath.Join(esp, "loader", "entries")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, EntryName(slot, tries)), []byte(EntryBody(slot, version)), 0o644); err != nil {
		t.Fatal(err)
	}
	loader := "timeout 3\nconsole-mode max\neditor no\nauto-entries no\nauto-firmware no\n"
	if tries == 0 {
		loader += "default " + EntryName(slot, 0) + "\n"
	}
	if err := os.WriteFile(filepath.Join(esp, "loader", "loader.conf"), []byte(loader), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newAppliance(t *testing.T, version, slot string) (*Service, *store.Store) {
	t.Helper()
	root := t.TempDir()
	data := filepath.Join(root, "state")
	esp := filepath.Join(root, "esp")
	if err := os.MkdirAll(filepath.Join(root, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "proc", "sys", "kernel"), 0o755); err != nil {
		t.Fatal(err)
	}
	rel := "ID=feldcore\nVERSION_ID=" + version + "\nSYSEXT_LEVEL=1\n"
	if err := os.WriteFile(filepath.Join(root, "etc", "feldcore-os-release"), []byte(rel), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := "root=LABEL=feldcore-" + slot + " ro feldcore.slot=" + slot + " feldcore.version=" + version + "\n"
	if err := os.WriteFile(filepath.Join(root, "proc", "cmdline"), []byte(cmd), 0o644); err != nil {
		t.Fatal(err)
	}
	slotA := filepath.Join(root, "slot-a")
	slotB := filepath.Join(root, "slot-b")
	for _, p := range []string{slotA, slotB} {
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Truncate(1 << 20); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	st, err := store.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	return &Service{
		Root: root, ESP: esp, DataDir: data,
		SlotA: slotA, SlotB: slotB, Store: st, AllowLocal: true,
	}, st
}
