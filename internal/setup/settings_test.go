package setup

import (
	"context"
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
