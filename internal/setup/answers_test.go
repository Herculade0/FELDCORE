package setup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Herculade0/feldcore/internal/auth"
	"github.com/Herculade0/feldcore/internal/backup"
	"github.com/Herculade0/feldcore/internal/notify"
	"github.com/Herculade0/feldcore/internal/remote"
	"github.com/Herculade0/feldcore/internal/store"
)

const (
	answerPass = "Correct Horse Battery 1"
	answerPSK  = "wifi-secret-value"
	answerKey  = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFakeKeyForTestsOnly answer"
)

type answerRig struct {
	s     *Service
	st    *store.Store
	dir   string
	etc   string
	efi   string
	cmd   *answerCmd
	logs  *bytes.Buffer
	pass  []string
	wifi  bool
	sshOn bool
	sudo  bool
}

func newAnswerRig(t *testing.T) *answerRig {
	t.Helper()
	dir := t.TempDir()
	etc := t.TempDir()
	efi := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	cmd := &answerCmd{}
	var logs bytes.Buffer
	rig := &answerRig{st: st, dir: dir, etc: etc, efi: efi, cmd: cmd, logs: &logs}
	s := New(Deps{
		Store: st, DataDir: dir, Cmd: cmd, Root: func() bool { return true },
		Log: slog.New(slog.NewTextHandler(&logs, nil)),
	})
	s.etcRoot = etc
	s.efiRoot = efi
	s.feat.wifi = &rig.wifi
	s.feat.ssh = &rig.sshOn
	s.feat.sudo = &rig.sudo
	s.unixPass = func(_ context.Context, user, pw string) error {
		rig.pass = append(rig.pass, user+"="+pw)
		return nil
	}
	s.UseAccounts(auth.New(st))
	rig.s = s
	return rig
}

func (r *answerRig) put(t *testing.T, body string) {
	t.Helper()
	path := filepath.Join(r.efi, "feldcore")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, answerName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (r *answerRig) joined() string {
	r.cmd.mu.Lock()
	defer r.cmd.mu.Unlock()
	return strings.Join(r.cmd.calls, "\n")
}

type answerCmd struct {
	mu        sync.Mutex
	calls     []string
	failMount bool
}

func (c *answerCmd) Run(_ context.Context, name string, args ...string) (string, error) {
	c.mu.Lock()
	c.calls = append(c.calls, name+" "+strings.Join(args, " "))
	fail := c.failMount && name == "mount"
	c.mu.Unlock()
	if fail {
		return "", errors.New("not a mount point")
	}
	if name == "id" {
		return "", errors.New("no such user")
	}
	if name == "systemctl" && len(args) >= 2 && args[0] == "is-active" && args[1] == "systemd-networkd" {
		return "active\n", nil
	}
	return "", nil
}

func TestAnswerFileMissingIsQuiet(t *testing.T) {
	rig := newAnswerRig(t)
	res, err := rig.s.ApplyInstallFile(context.Background())
	if err != nil || res != nil {
		t.Fatalf("res=%v err=%v", res, err)
	}
	if rig.joined() != "" {
		t.Fatal(rig.joined())
	}
}

func TestAnswerFileRejectsBadInputBeforeChanges(t *testing.T) {
	cases := []string{
		`not json`,
		`{"version":1,"rootPassword":"nope"}`,
		`{"version":2,"hostname":"feldbox","timezone":"UTC"}`,
		`{"version":1,"hostname":"feldbox","timezone":"UTC","ntp":"chrony"}`,
		`{"version":1,"admin":{"username":"admin","password":"` + answerPass + `","skipTotp":true},"setupCode":"ABCD2345"}`,
		`{"version":1,"admin":{"username":"admin","password":"` + answerPass + `","skipTotp":false}}`,
		`{"version":1,"ssh":{"enable":true,"authorizedKeys":["-----BEGIN OPENSSH PRIVATE KEY-----\nsecret"]}}`,
		`{"version":1,"hostname":"-bad","timezone":"UTC"}`,
	}
	for _, body := range cases {
		rig := newAnswerRig(t)
		rig.put(t, body)
		if _, err := rig.s.ApplyInstallFile(context.Background()); err == nil {
			t.Fatalf("accepted %s", body)
		}
		if strings.Contains(rig.joined(), "hostnamectl") || strings.Contains(rig.joined(), "useradd") {
			t.Fatalf("bad file changed the system: %s\n%s", body, rig.joined())
		}
		if rig.st.Snapshot().Admin != nil {
			t.Fatal("admin created from a rejected file")
		}
	}
}

func TestAnswerFileNeedsRoot(t *testing.T) {
	rig := newAnswerRig(t)
	rig.s.root = func() bool { return false }
	rig.put(t, `{"version":1,"hostname":"feldbox","timezone":"UTC"}`)
	if _, err := rig.s.ApplyInstallFile(context.Background()); err == nil || !strings.Contains(err.Error(), "root") {
		t.Fatal(err)
	}
	if strings.Contains(rig.joined(), "hostnamectl") {
		t.Fatal(rig.joined())
	}
}

func TestAnswerFileDHCPAndSetupCode(t *testing.T) {
	rig := newAnswerRig(t)
	rig.put(t, `{
		"version": 1,
		"hostname": "feldbox",
		"timezone": "UTC",
		"locale": "en_GB.UTF-8",
		"ntp": "systemd-timesyncd",
		"network": {"mode": "dhcp"},
		"setupCode": "ABCD-2345"
	}`)
	res, err := rig.s.ApplyInstallFile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Already || !containsStr(res.Applied, "host") || !containsStr(res.Applied, "setupCode") || !containsStr(res.Applied, "network") {
		t.Fatalf("%+v", res)
	}
	if _, err := os.Stat(filepath.Join(rig.etc, "systemd", "network", "10-feldcore.network")); !os.IsNotExist(err) {
		t.Fatal("dhcp wrote 10-feldcore.network")
	}
	code, err := rig.s.EnsureCode()
	if err != nil || code != "ABCD2345" {
		t.Fatalf("code %q err %v", code, err)
	}
	w := rig.st.Snapshot().Wizard
	if w == nil || w.Done || w.Hostname != "feldbox" || w.NetMode != "dhcp" {
		t.Fatalf("wizard %+v", w)
	}
	if _, err := os.Stat(filepath.Join(rig.efi, "feldcore", answerName)); !os.IsNotExist(err) {
		t.Fatal("answer file was left on the ESP")
	}
	calls := rig.joined()
	rig.put(t, `{"version":1,"hostname":"other","timezone":"UTC"}`)
	again, err := rig.s.ApplyInstallFile(context.Background())
	if err != nil || again == nil || !again.Already {
		t.Fatalf("second res=%v err=%v", again, err)
	}
	if rig.joined() != calls {
		t.Fatal("applied again on the next boot")
	}
	if rig.st.Snapshot().Wizard.Hostname != "feldbox" {
		t.Fatal("a second boot changed the hostname")
	}
	if strings.Contains(rig.logs.String(), "ABCD2345") {
		t.Fatal("setup code was written to the log")
	}
}

func TestAnswerFileAdminSurvivesNotifyFailure(t *testing.T) {
	rig := newAnswerRig(t)
	rig.s.UseMail(stubMail{err: errors.New("smtp refused")})
	rig.put(t, `{
		"version": 1,
		"hostname": "feldbox",
		"timezone": "Europe/London",
		"admin": {"username": "admin", "password": "`+answerPass+`", "skipTotp": true},
		"notify": {"kind": "email", "mailHost": "smtp.example", "mailTo": "ops@example.com"}
	}`)
	res, err := rig.s.ApplyInstallFile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !containsStr(res.Applied, "admin") || len(res.Warnings) == 0 || !strings.Contains(res.Warnings[0], "smtp refused") {
		t.Fatalf("%+v", res)
	}
	admin := rig.st.Snapshot().Admin
	if admin == nil || admin.Username != "admin" || admin.TOTP {
		t.Fatalf("admin %+v", admin)
	}
	w := rig.st.Snapshot().Wizard
	if w == nil || !w.Done || w.Step != "summary" || w.AdminUser != "admin" || w.TOTP {
		t.Fatalf("wizard %+v", w)
	}
	if _, err := os.Stat(filepath.Join(rig.dir, appliedName)); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.s.EnsureCode(); err != nil {
		t.Fatal(err)
	}
	if code, _ := os.ReadFile(rig.s.codePath()); len(code) != 0 {
		t.Fatalf("setup code still present: %q", code)
	}
	blob := readTree(t, rig.dir)
	if strings.Contains(blob, answerPass) {
		t.Fatal("admin password landed in the state directory")
	}
}

func TestAnswerFileBackupFailureDoesNotRollBackAdmin(t *testing.T) {
	rig := newAnswerRig(t)
	drive := &stubDrive{err: errors.New("proton unreachable")}
	rig.s.UseDrive(drive)
	body := `{
		"version": 1,
		"admin": {"username": "admin", "password": "` + answerPass + `", "skipTotp": true},
		"backup": {"kind": "proton", "user": "ada@example.com", "password": "proton-secret-value"}
	}`
	rig.put(t, body)
	res, err := rig.s.ApplyInstallFile(context.Background())
	if err == nil || res == nil || !containsStr(res.Applied, "admin") {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if rig.st.Snapshot().Admin == nil {
		t.Fatal("admin was rolled back")
	}
	if _, err := os.Stat(filepath.Join(rig.dir, appliedName)); !os.IsNotExist(err) {
		t.Fatal("marker written after a failed backup")
	}
	if _, err := os.Stat(filepath.Join(rig.efi, "feldcore", answerName)); err != nil {
		t.Fatal("answer file removed after a failed backup")
	}
	drive.err = nil
	res, err = rig.s.ApplyInstallFile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !containsStr(res.Applied, "backup") || rig.st.Snapshot().Wizard == nil || !rig.st.Snapshot().Wizard.Done {
		t.Fatalf("%+v wizard %+v", res, rig.st.Snapshot().Wizard)
	}
	if drive.got.Password != "proton-secret-value" {
		t.Fatal("backup password was not passed to the writer")
	}
	if strings.Contains(rig.logs.String(), "proton-secret-value") || strings.Contains(readTree(t, rig.dir), "proton-secret-value") {
		t.Fatal("backup password was stored in the log or state")
	}
}

func TestAnswerFileSkipsMissingWifiAndSSH(t *testing.T) {
	rig := newAnswerRig(t)
	rig.put(t, `{
		"version": 1,
		"wifi": {"ssid": "lab", "psk": "`+answerPSK+`", "country": "GB"},
		"ssh": {"enable": true, "permitRoot": true, "authorizedKeys": ["`+answerKey+`"]}
	}`)
	res, err := rig.s.ApplyInstallFile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !containsStr(res.Skipped, "wifi") || !containsStr(res.Skipped, "ssh") {
		t.Fatalf("%+v", res)
	}
	if containsStr(res.Applied, "wifi") || containsStr(res.Applied, "ssh") {
		t.Fatal("missing packages were recorded as applied")
	}
	if _, err := os.Stat(filepath.Join(rig.etc, "wpa_supplicant", "feldcore.conf")); !os.IsNotExist(err) {
		t.Fatal("wifi config written without wpa_supplicant")
	}
	if _, err := os.Stat(filepath.Join(rig.etc, "ssh", "authorized_keys", "root")); !os.IsNotExist(err) {
		t.Fatal("ssh key written without sshd")
	}
	if strings.Contains(rig.joined(), "sgdisk") || strings.Contains(rig.joined(), "mkfs") {
		t.Fatal(rig.joined())
	}
}

func TestAnswerFileWifiSSHAndUnixUser(t *testing.T) {
	rig := newAnswerRig(t)
	rig.wifi = true
	rig.sshOn = true
	rig.put(t, `{
		"version": 1,
		"network": {"mode": "static", "address": "192.168.1.20/24", "gateway": "192.168.1.1", "dns": "1.1.1.1"},
		"wifi": {"ssid": "lab", "psk": "`+answerPSK+`", "country": "gb"},
		"ssh": {"enable": true, "permitRoot": true, "authorizedKeys": ["`+answerKey+`"]},
		"user": {"name": "ada", "password": "`+answerPass+`", "sudo": true, "authorizedKeys": ["`+answerKey+`"]}
	}`)
	res, err := rig.s.ApplyInstallFile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"network", "wifi", "ssh", "user"} {
		if !containsStr(res.Applied, name) {
			t.Fatalf("missing %s in %+v", name, res)
		}
	}
	if !strings.Contains(strings.Join(res.Notes, " "), "sudo is not installed") {
		t.Fatalf("notes %v", res.Notes)
	}
	if _, err := os.Stat(filepath.Join(rig.etc, "sudoers.d", "feldcore-ada")); !os.IsNotExist(err) {
		t.Fatal("sudoers written without sudo")
	}
	conf := mustRead(t, filepath.Join(rig.etc, "wpa_supplicant", "feldcore.conf"))
	if !strings.Contains(conf, `ssid="lab"`) || !strings.Contains(conf, `psk="`+answerPSK+`"`) || !strings.Contains(conf, "country=GB") {
		t.Fatal(conf)
	}
	if mode := mustMode(t, filepath.Join(rig.etc, "wpa_supplicant", "feldcore.conf")); mode != 0o600 {
		t.Fatalf("wifi conf mode %o", mode)
	}
	wlan := mustRead(t, filepath.Join(rig.etc, "systemd", "network", "10-feldcore-wifi.network"))
	if !strings.Contains(wlan, "Type=wlan") || !strings.Contains(wlan, "Address=192.168.1.20/24") {
		t.Fatal(wlan)
	}
	drop := mustRead(t, filepath.Join(rig.etc, "ssh", "sshd_config.d", "00-feldcore.conf"))
	if !strings.Contains(drop, "PermitRootLogin prohibit-password") || !strings.Contains(drop, "PasswordAuthentication no") || strings.Contains(drop, "PermitRootLogin yes") {
		t.Fatal(drop)
	}
	if !strings.Contains(mustRead(t, filepath.Join(rig.etc, "ssh", "authorized_keys", "root")), answerKey) {
		t.Fatal("root key missing")
	}
	homeKey := mustRead(t, filepath.Join(rig.dir, "home", "ada", ".ssh", "authorized_keys"))
	if !strings.Contains(homeKey, answerKey) {
		t.Fatal(homeKey)
	}
	calls := rig.joined()
	if !strings.Contains(calls, "useradd --create-home --home-dir "+filepath.Join(rig.dir, "home", "ada")) {
		t.Fatal(calls)
	}
	if !strings.Contains(calls, "systemctl enable --now ssh") || !strings.Contains(calls, "systemctl enable --now feldcore-wifi.service") {
		t.Fatal(calls)
	}
	if strings.Contains(calls, answerPass) || strings.Contains(calls, answerPSK) {
		t.Fatal("secret on a command line")
	}
	if len(rig.pass) != 1 || rig.pass[0] != "ada="+answerPass {
		t.Fatalf("unix password calls %v", rig.pass)
	}
	if strings.Contains(rig.logs.String(), answerPSK) || strings.Contains(rig.logs.String(), answerPass) {
		t.Fatal("secret in the log")
	}
}

func TestAnswerFileSSHStaysOffWithoutRootLogin(t *testing.T) {
	rig := newAnswerRig(t)
	rig.sshOn = true
	rig.put(t, `{"version":1,"ssh":{"enable":false,"permitRoot":false}}`)
	if _, err := rig.s.ApplyInstallFile(context.Background()); err != nil {
		t.Fatal(err)
	}
	drop := mustRead(t, filepath.Join(rig.etc, "ssh", "sshd_config.d", "00-feldcore.conf"))
	if !strings.Contains(drop, "PermitRootLogin no") || strings.Contains(drop, "PermitRootLogin yes") || strings.Contains(drop, "PasswordAuthentication yes") {
		t.Fatal(drop)
	}
	if !strings.Contains(rig.joined(), "systemctl disable --now ssh") {
		t.Fatal(rig.joined())
	}
}

func TestAnswerFileNTPServers(t *testing.T) {
	rig := newAnswerRig(t)
	rig.put(t, `{"version":1,"ntpServers":"time.cloudflare.com 1.1.1.1"}`)
	res, err := rig.s.ApplyInstallFile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !containsStr(res.Applied, "ntp") {
		t.Fatalf("%+v", res)
	}
	body := mustRead(t, filepath.Join(rig.etc, "systemd", "timesyncd.conf.d", "feldcore.conf"))
	if !strings.Contains(body, "NTP=time.cloudflare.com 1.1.1.1") {
		t.Fatal(body)
	}
	if got := rig.st.Snapshot().Prefs.NTPServers; got != "time.cloudflare.com 1.1.1.1" {
		t.Fatal(got)
	}
}

func TestAnswerFileStaticInterface(t *testing.T) {
	rig := newAnswerRig(t)
	rig.put(t, `{
		"version": 1,
		"network": {"mode": "static", "iface": "eth0", "address": "192.168.1.20/24", "gateway": "192.168.1.1", "dns": "1.1.1.1"}
	}`)
	if _, err := rig.s.ApplyInstallFile(context.Background()); err != nil {
		t.Fatal(err)
	}
	body := mustRead(t, filepath.Join(rig.etc, "systemd", "network", "10-feldcore.network"))
	if !strings.Contains(body, "Name=eth0") || !strings.Contains(body, "Address=192.168.1.20/24") || !strings.Contains(body, "DNS=1.1.1.1") {
		t.Fatal(body)
	}
	if !strings.Contains(rig.joined(), "networkctl reconfigure eth0") {
		t.Fatal(rig.joined())
	}
}

func TestAnswerFileDataPoolIsReserved(t *testing.T) {
	rig := newAnswerRig(t)
	rig.put(t, `{"version":1,"dataPool":false}`)
	res, err := rig.s.ApplyInstallFile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !containsStr(res.Applied, "dataPool") || !strings.Contains(strings.Join(res.Notes, " "), "declined") {
		t.Fatalf("%+v", res)
	}
	got := mustRead(t, filepath.Join(rig.dir, dataPoolName))
	if strings.TrimSpace(got) != `{"declined": true}` {
		t.Fatal(got)
	}
	if strings.Contains(rig.joined(), "sgdisk") || strings.Contains(rig.joined(), "mkfs") {
		t.Fatal(rig.joined())
	}

	rig = newAnswerRig(t)
	if err := os.WriteFile(filepath.Join(rig.dir, dataPoolName), []byte("{\"declined\": true}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rig.put(t, `{"version":1,"dataPool":true}`)
	res, err = rig.s.ApplyInstallFile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(res.Notes, " "), "not created") {
		t.Fatalf("%+v", res)
	}
	if _, err := os.Stat(filepath.Join(rig.dir, dataPoolName)); !os.IsNotExist(err) {
		t.Fatal("declined marker left in place when dataPool is true")
	}
}

func TestAnswerFileRemoteTokenIsNotLogged(t *testing.T) {
	rig := newAnswerRig(t)
	tunnel := &stubTunnel{}
	rig.s.UseTunnel(tunnel)
	rig.put(t, `{
		"version": 1,
		"remote": {"mode": "cloudflare", "cfMode": "token", "cfToken": "cloudflare-token-value"}
	}`)
	if _, err := rig.s.ApplyInstallFile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if tunnel.got.TunnelToken != "cloudflare-token-value" || tunnel.got.TunnelName != "feldcore" {
		t.Fatalf("%+v", tunnel.got)
	}
	if strings.Contains(rig.logs.String(), "cloudflare-token-value") || strings.Contains(readTree(t, rig.dir), "cloudflare-token-value") {
		t.Fatal("tunnel token stored in the log or state")
	}
}

type stubMail struct{ err error }

func (m stubMail) Save(context.Context, notify.Settings) error { return nil }
func (m stubMail) Test(context.Context) error                  { return m.err }

type stubDrive struct {
	err error
	n   int
	got backup.Settings
}

func (d *stubDrive) Save(in backup.Settings) error {
	d.n++
	d.got = in
	if d.n == 1 && d.err != nil {
		return d.err
	}
	return nil
}

type stubTunnel struct{ got remote.SaveInput }

func (t *stubTunnel) Save(_ context.Context, in remote.SaveInput) error {
	t.got = in
	return nil
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func mustMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return st.Mode().Perm()
}

func TestAnswerProbeAndVolumeRule(t *testing.T) {
	good := []string{
		`{"version":1}`,
		`{"version":1.0,"hostname":"feldbox"}`,
		" \n{\"version\":1}\n",
	}
	for _, body := range good {
		if err := answerProbe([]byte(body)); err != nil {
			t.Fatalf("rejected %q: %v", body, err)
		}
	}
	bad := []string{
		``,
		`[]`,
		`{"version":"1"}`,
		`{"version":2}`,
		`{"version":1}{"version":1}`,
		`{"hostname":"feldbox"}`,
		`not json`,
	}
	for _, body := range bad {
		if err := answerProbe([]byte(body)); err == nil {
			t.Fatalf("accepted %q", body)
		}
	}
	if !answerVolumeEligible("vfat", "", "1", "") ||
		!answerVolumeEligible("exfat", "OTHER", "0", "usb") ||
		!answerVolumeEligible("vfat", "FC-ANSWERS", "0", "virtio") ||
		!answerVolumeEligible("vfat", "FELDCORE-ANSWERS", "0", "virtio") {
		t.Fatal("eligible volume was rejected")
	}
	if answerVolumeEligible("vfat", "OTHER", "0", "virtio") ||
		answerVolumeEligible("iso9660", "FELDCORE", "1", "usb") ||
		answerVolumeEligible("ext4", "", "1", "usb") {
		t.Fatal("ineligible volume was accepted")
	}
}

func TestAnswerScanStaysOffWhenEFIRootIsSet(t *testing.T) {
	rig := newAnswerRig(t)
	called := false
	rig.s.answerVolumes = func(context.Context) ([]answerVolume, error) {
		called = true
		return nil, nil
	}
	res, err := rig.s.ApplyInstallFile(context.Background())
	if err != nil || res != nil || called {
		t.Fatalf("res=%v err=%v called=%v", res, err, called)
	}
}

func TestAnswerESPBeatsScan(t *testing.T) {
	rig := newAnswerRig(t)
	rig.put(t, `{"version":1,"hostname":"fromesp","timezone":"UTC"}`)
	other := t.TempDir()
	writeSideAnswer(t, other, "feldcore/"+answerName, `{"version":1,"hostname":"fromstick","timezone":"UTC"}`)
	called := false
	rig.s.answerVolumes = func(context.Context) ([]answerVolume, error) {
		called = true
		return []answerVolume{{Dir: other, Dev: "/dev/vdb1"}}, nil
	}
	res, err := rig.s.ApplyInstallFile(context.Background())
	if err != nil || res == nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("scan ran even though the ESP file was present")
	}
	if rig.st.Snapshot().Wizard == nil || rig.st.Snapshot().Wizard.Hostname != "fromesp" {
		t.Fatalf("hostname %+v", rig.st.Snapshot().Wizard)
	}
}

func TestAnswerScanAppliesSideFile(t *testing.T) {
	rig := newAnswerRig(t)
	dir := t.TempDir()
	path := writeSideAnswer(t, dir, "feldcore/"+answerName, "{\"version\":1}\n")
	if err := rig.useAnswerVolumes(t, dir); err != nil {
		t.Fatal(err)
	}
	res, err := rig.s.ApplyInstallFile(context.Background())
	if err != nil || res == nil || res.Already {
		t.Fatalf("res=%v err=%v", res, err)
	}
	if _, err := os.Stat(filepath.Join(rig.dir, appliedName)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("side answer file was left in place after a successful apply")
	}
}

func TestAnswerScanPrefersNestedPath(t *testing.T) {
	rig := newAnswerRig(t)
	dir := t.TempDir()
	writeSideAnswer(t, dir, "feldcore/"+answerName, `{"version":1,"hostname":"nested","timezone":"UTC"}`)
	writeSideAnswer(t, dir, answerName, `{"version":1,"hostname":"atroot","timezone":"UTC"}`)
	if err := rig.useAnswerVolumes(t, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.s.ApplyInstallFile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rig.st.Snapshot().Wizard == nil || rig.st.Snapshot().Wizard.Hostname != "nested" {
		t.Fatalf("hostname %+v", rig.st.Snapshot().Wizard)
	}
}

func TestAnswerScanRejectsTwoFiles(t *testing.T) {
	rig := newAnswerRig(t)
	a := t.TempDir()
	b := t.TempDir()
	writeSideAnswer(t, a, answerName, `{"version":1}`)
	writeSideAnswer(t, b, "feldcore/"+answerName, `{"version":1}`)
	if err := rig.useAnswerVolumes(t, a, b); err != nil {
		t.Fatal(err)
	}
	_, err := rig.s.ApplyInstallFile(context.Background())
	if err == nil || !strings.Contains(err.Error(), "/dev/vda1") || !strings.Contains(err.Error(), "/dev/vdb1") {
		t.Fatal(err)
	}
	if rig.st.Snapshot().Wizard != nil {
		t.Fatal("two answer files changed the system")
	}
	if _, err := os.Stat(filepath.Join(rig.dir, appliedName)); !os.IsNotExist(err) {
		t.Fatal("marker written for an ambiguous answer file")
	}
}

func TestAnswerScanDoesNotSkipABadFile(t *testing.T) {
	rig := newAnswerRig(t)
	bad := t.TempDir()
	good := t.TempDir()
	writeSideAnswer(t, bad, "feldcore/"+answerName, `{"version":2}`)
	goodPath := writeSideAnswer(t, good, answerName, `{"version":1,"hostname":"kept","timezone":"UTC"}`)
	if err := rig.useAnswerVolumes(t, bad, good); err != nil {
		t.Fatal(err)
	}
	_, err := rig.s.ApplyInstallFile(context.Background())
	if err == nil || !strings.Contains(err.Error(), "/dev/vda1") {
		t.Fatal(err)
	}
	if _, err := os.Stat(goodPath); err != nil {
		t.Fatal(err)
	}
	if rig.st.Snapshot().Wizard != nil {
		t.Fatal("a bad answer file fell through to another volume")
	}
}

func TestAnswerSideDeleteFailureIsAWarning(t *testing.T) {
	rig := newAnswerRig(t)
	dir := t.TempDir()
	path := writeSideAnswer(t, dir, "feldcore/"+answerName, `{"version":1}`)
	parent := filepath.Dir(path)
	if err := os.Chmod(parent, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })
	rig.cmd.failMount = true
	rig.s.answerSrc = path
	rig.s.answerMount = dir
	rig.s.removeAnswerCopies(context.Background())
	_, statErr := os.Stat(path)
	warn := strings.Contains(rig.logs.String(), "could not remove the install answer file")
	if statErr == nil && !warn {
		t.Fatal("the answer file is still there and no warning was logged")
	}
	if statErr != nil && warn {
		t.Fatal("the answer file was removed and a warning was still logged")
	}
}

func (r *answerRig) useAnswerVolumes(t *testing.T, dirs ...string) error {
	t.Helper()
	if _, err := os.Stat("/dev/disk/by-label/FC-ESP"); err == nil {
		t.Skip("this machine has an FC-ESP volume")
	}
	if _, err := os.Stat("/efi/feldcore/" + answerName); err == nil {
		t.Skip("this machine already has an answer file on /efi")
	}
	r.s.efiRoot = ""
	vols := make([]answerVolume, 0, len(dirs))
	for i, dir := range dirs {
		vols = append(vols, answerVolume{Dir: dir, Dev: fmt.Sprintf("/dev/vd%c1", 'a'+i)})
	}
	r.s.answerVolumes = func(context.Context) ([]answerVolume, error) {
		return vols, nil
	}
	return nil
}

func writeSideAnswer(t *testing.T, dir, rel, body string) string {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readTree(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		b.Write(body)
		b.WriteByte('\n')
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}
