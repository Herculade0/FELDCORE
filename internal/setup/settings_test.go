package setup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Herculade0/feldcore/internal/store"
)

func TestSettingsValidationAndSSHRules(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	cmd := &recordCmd{}
	s := New(Deps{Store: st, DataDir: dir, Cmd: cmd, Root: func() bool { return false }, ListenPort: 8787})
	ctx := context.Background()

	if err := s.SaveSystem(ctx, "ok", "UTC", "not a server!"); err == nil {
		t.Fatal("bad ntp server accepted")
	}
	if err := s.SaveSystem(ctx, "feldbox", "UTC", "pool.ntp.org"); err == nil {
		t.Fatal("system change without root")
	}
	if err := s.SaveNetwork(ctx, "static", "eth0", "10.0.0.5/24", "192.168.1.1", ""); err == nil {
		t.Fatal("gateway on another network accepted")
	}
	if err := s.SaveService(ctx, "nope", true); err == nil {
		t.Fatal("unknown service accepted")
	}
	for _, c := range cmd.calls {
		if strings.Contains(c, "hostnamectl") || strings.Contains(c, "ufw") {
			t.Fatalf("rejected settings ran a command: %s", c)
		}
	}

	rooted := New(Deps{Store: st, DataDir: dir, Cmd: cmd, Root: func() bool { return true }, ListenPort: 8787})
	if err := rooted.SaveFirewall(ctx, true); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(cmd.calls, "\n")
	if !strings.Contains(joined, "ufw allow 22/tcp") {
		t.Fatalf("ssh was not allowed:\n%s", joined)
	}
	if strings.Contains(joined, "ufw deny") || strings.Contains(joined, "ufw disable") {
		t.Fatalf("ssh or the firewall was closed:\n%s", joined)
	}
	if !st.Snapshot().Prefs.Firewall || !st.Snapshot().Prefs.Touched {
		t.Fatal("firewall choice was not saved")
	}
}

type recordCmd struct{ calls []string }

func (f *recordCmd) Run(_ context.Context, name string, args ...string) (string, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	return "", nil
}

// networkdCmd reports systemd-networkd as the active backend.
type networkdCmd struct{}

func (networkdCmd) Run(_ context.Context, name string, args ...string) (string, error) {
	if name == "systemctl" && len(args) >= 2 && args[0] == "is-active" && args[1] == "systemd-networkd" {
		return "active\n", nil
	}
	return "", nil
}

func TestNetModeReadsLiveConfig(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	// networkd: a feldcore .network with Address= is static; DHCP=yes is dhcp.
	net := New(Deps{Store: st, DataDir: dir, Cmd: networkdCmd{}, Root: func() bool { return true }})
	net.etcRoot = t.TempDir()
	if got := net.netMode(); got != "dhcp" {
		t.Fatalf("no config should read as dhcp, got %q", got)
	}
	path := net.etcPath("systemd", "network", "10-feldcore.network")
	writeFile(t, path, "[Match]\nName=eth0\n\n[Network]\nAddress=192.168.0.16/24\nGateway=192.168.0.1\n")
	if got := net.netMode(); got != "static" {
		t.Fatalf("a pinned Address should read as static, got %q", got)
	}
	writeFile(t, path, "[Match]\nName=eth0\n\n[Network]\nDHCP=yes\n")
	if got := net.netMode(); got != "dhcp" {
		t.Fatalf("DHCP=yes should read as dhcp, got %q", got)
	}

	// ifupdown: "inet static" is static.
	ip := New(Deps{Store: st, DataDir: dir, Cmd: &recordCmd{}, Root: func() bool { return true }})
	ip.etcRoot = t.TempDir()
	writeFile(t, ip.etcPath("network", "interfaces"), "auto lo\n")
	if got := ip.netMode(); got != "dhcp" {
		t.Fatalf("ifupdown with no feldcore file should read as dhcp, got %q", got)
	}
	writeFile(t, ip.etcPath("network", "interfaces.d", "feldcore"),
		"auto eth0\niface eth0 inet static\n    address 192.168.0.16/24\n    gateway 192.168.0.1\n")
	if got := ip.netMode(); got != "static" {
		t.Fatalf("inet static should read as static, got %q", got)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
