package setup

import (
	"context"
	"strings"
	"testing"

	"github.com/Herculade0/feldcore/internal/store"
)

func TestClaimCodeOnce(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := New(Deps{Store: st, DataDir: dir, Root: func() bool { return false }})
	code, err := s.EnsureCode()
	if err != nil || len(code) != 8 {
		t.Fatalf("code %q err %v", code, err)
	}
	again, err := s.EnsureCode()
	if err != nil || again != code {
		t.Fatalf("restart generated a new code: %q vs %q", again, code)
	}
	if FormatCode(code) != code[:4]+"-"+code[4:] {
		t.Fatalf("format %s", FormatCode(code))
	}
	if err := s.CheckCode("client", "nope-nope"); err == nil {
		t.Fatal("wrong code accepted")
	}
	if err := s.CheckCode("client", FormatCode(code)); err != nil {
		t.Fatal(err)
	}
	s.ConsumeCode()
	if err := s.CheckCode("other", FormatCode(code)); err == nil {
		t.Fatal("code still worked after it was used")
	}
}

func TestWizardResumesAndPreviewsWithoutChanges(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	cmd := &fakeCmd{}
	s := New(Deps{Store: st, DataDir: dir, Cmd: cmd, Root: func() bool { return true }})
	if err := s.Begin(false); err != nil {
		t.Fatal(err)
	}
	code, err := s.EnsureCode()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, _, err := s.Apply(ctx, Request{Step: "welcome", Confirm: true, Code: code}); err != nil {
		t.Fatal(err)
	}
	var view View
	view, _, err = s.Apply(ctx, Request{
		Step: "host", Code: code, Hostname: "feldbox", Timezone: "UTC", Locale: "en_US.UTF-8", NTP: "systemd-timesyncd",
	})
	if err != nil {
		t.Fatal(err)
	}
	if view.Step != "host" || len(view.Preview) == 0 {
		t.Fatalf("preview: %+v", view)
	}
	for _, c := range cmd.calls {
		if strings.Contains(c, "hostnamectl") || strings.Contains(c, "timedatectl") || strings.Contains(c, "apt-get") {
			t.Fatalf("preview changed the system: %v", cmd.calls)
		}
	}

	view, _, err = s.Apply(ctx, Request{
		Step: "host", Confirm: true, Code: code, Hostname: "feldbox", Timezone: "UTC", Locale: "en_US.UTF-8", NTP: "systemd-timesyncd",
	})
	if err != nil {
		t.Fatal(err)
	}
	if view.Step != "network" {
		t.Fatalf("step %s", view.Step)
	}
	joined := strings.Join(cmd.calls, "\n")
	if !strings.Contains(joined, "hostnamectl set-hostname feldbox") || !strings.Contains(joined, "timedatectl set-timezone UTC") {
		t.Fatal(joined)
	}

	// Interrupted: the saved step is where the next visit starts.
	again, err := s.Status(ctx, nil)
	if err != nil || again.Step != "network" {
		t.Fatalf("resume %+v %v", again.Step, err)
	}
	if s.Pending() {
		t.Fatal("pending before an admin exists")
	}
}

func TestApplyRefusesWithoutRoot(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(dir)
	s := New(Deps{Store: st, DataDir: dir, Root: func() bool { return false }})
	_ = s.Begin(false)
	code, err := s.EnsureCode()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Apply(context.Background(), Request{Step: "welcome", Confirm: true, Code: code}); err != nil {
		t.Fatal(err)
	}
	_, _, err = s.Apply(context.Background(), Request{
		Step: "host", Confirm: true, Code: code, Hostname: "feldbox", Timezone: "UTC", NTP: "systemd-timesyncd",
	})
	if err == nil {
		t.Fatal("expected a root error")
	}
}

func TestSkipWizard(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(dir)
	s := New(Deps{Store: st, DataDir: dir})
	if err := s.Begin(true); err != nil {
		t.Fatal(err)
	}
	if s.st.Snapshot().Wizard == nil || !s.st.Snapshot().Wizard.Done {
		t.Fatal("skip should finish the wizard")
	}
}

func TestNetworkValidation(t *testing.T) {
	if err := ValidateNet(NetInput{Mode: "dhcp"}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateNet(NetInput{Mode: "static", Iface: "eth0", Address: "192.168.1.20/24", Gateway: "192.168.1.1", DNS: "1.1.1.1"}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateNet(NetInput{Mode: "static", Iface: "eth0", Address: "192.168.1.20/24", Gateway: "10.0.0.1"}); err == nil {
		t.Fatal("gateway on another network accepted")
	}
	if NewURL("192.168.1.20/24", 8787) != "http://192.168.1.20:8787/" {
		t.Fatal(NewURL("192.168.1.20/24", 8787))
	}
}

func TestFirewallAlwaysAllowsSSH(t *testing.T) {
	ports := sshPortsFrom("", "")
	if len(ports) != 1 || ports[0] != 22 {
		t.Fatalf("fallback %v", ports)
	}
	ports = sshPortsFrom("port 22\nport 2200\n", "LISTEN 0 128 0.0.0.0:22 0.0.0.0:* users:((\"sshd\",pid=1,fd=3))")
	if len(ports) != 2 || ports[0] != 22 || ports[1] != 2200 {
		t.Fatalf("%v", ports)
	}
	cmds := UFWCommands(8787, ports, false, false)
	if !AllowsSSH(cmds, ports) {
		t.Fatalf("ssh not allowed: %v", cmds)
	}
	joined := strings.Join(flatten(cmds), "\n")
	if strings.Contains(joined, "disable") || strings.Contains(joined, "ufw deny") {
		t.Fatal(joined)
	}
	if !strings.Contains(joined, "8787/tcp") {
		t.Fatal(joined)
	}
	// Empty detection still opens 22 before enable.
	if !AllowsSSH(UFWCommands(8787, nil, false, false), []int{22}) {
		t.Fatal("missing fallback ssh")
	}
}

func TestVirtAndOSDisk(t *testing.T) {
	if !VirtAvailable("flags : vmx") || !VirtAvailable("flags : svm") || VirtAvailable("flags : sse") {
		t.Fatal("virt detection")
	}
	mounts := []Mount{{Path: "/", Device: "/dev/sda1", OS: true}, {Path: "/mnt/data", Device: "/dev/sdb1"}}
	if !OnOSDisk("/srv/feldcore", mounts) || OnOSDisk("/mnt/data/shares", mounts) {
		t.Fatal("os disk warning")
	}
}

func TestHostPlanRejectsBadNames(t *testing.T) {
	if _, err := HostPlan("ok", "UTC", "systemd-timesyncd", "en_US.UTF-8"); err != nil {
		t.Fatal(err)
	}
	if _, err := HostPlan("ok", "UTC", "systemd-timesyncd", "C.UTF-8"); err != nil {
		t.Fatal(err)
	}
	if _, err := HostPlan("-bad", "UTC", "systemd-timesyncd", ""); err == nil {
		t.Fatal("accepted a bad host name")
	}
	if _, err := HostPlan("ok", "../etc", "systemd-timesyncd", ""); err == nil {
		t.Fatal("accepted a bad time zone")
	}
	if _, err := HostPlan("ok", "UTC", "systemd-timesyncd", "../locale"); err == nil {
		t.Fatal("accepted a bad locale")
	}
}

type fakeCmd struct{ calls []string }

func (f *fakeCmd) Run(_ context.Context, name string, args ...string) (string, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	if name == "systemctl" {
		return "inactive", errInactive
	}
	return "", nil
}

type inactive string

func (e inactive) Error() string { return string(e) }

const errInactive inactive = "inactive"

func flatten(cmds [][]string) []string {
	var out []string
	for _, c := range cmds {
		out = append(out, strings.Join(c, " "))
	}
	return out
}
