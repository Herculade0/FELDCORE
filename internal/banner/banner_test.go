package banner

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestHostIPv4sAreGlobal(t *testing.T) {
	for _, ip := range HostIPv4s() {
		parsed := net.ParseIP(ip)
		if parsed == nil || parsed.To4() == nil || !parsed.IsGlobalUnicast() || parsed.IsLoopback() {
			t.Fatalf("bad address %q", ip)
		}
	}
}

func TestRenderShowsCodeAndURL(t *testing.T) {
	got := Render("ABCD2345", []string{"192.168.1.20"}, 8787)
	want := "FELDCORE OS \\S (\\l)\n\n" +
		"Web UI: http://192.168.1.20:8787/\n" +
		"\n" +
		"Setup code: ABCD-2345\n" +
		"It works once.\n"
	if got != want {
		t.Fatalf("banner:\n%q", got)
	}
}

func TestRenderPlaceholderAndSeveralAddresses(t *testing.T) {
	waiting := Render("abcd2345", nil, 0)
	if !strings.Contains(waiting, "http://<no address yet>:8787/") || !strings.Contains(waiting, "Waiting for a network address.") {
		t.Fatalf("placeholder:\n%s", waiting)
	}
	if !strings.Contains(waiting, "Setup code: ABCD-2345") || strings.Contains(waiting, "ABCD2345") {
		t.Fatalf("code format:\n%s", waiting)
	}
	many := Render("ABCD2345", []string{"10.0.2.15", "192.168.1.20", "10.0.2.15", ""}, 8787)
	want := "FELDCORE OS \\S (\\l)\n\n" +
		"Web UI:\n" +
		"  http://10.0.2.15:8787/\n" +
		"  http://192.168.1.20:8787/\n" +
		"\n" +
		"Setup code: ABCD-2345\n" +
		"It works once.\n"
	if many != want {
		t.Fatalf("many:\n%q", many)
	}
}

func TestRenderOmitsAUsedOrInvalidCode(t *testing.T) {
	got := Render("", []string{"10.0.2.15"}, 8787)
	if strings.Contains(got, "Setup code") {
		t.Fatalf("empty code still shown:\n%s", got)
	}
	if !strings.Contains(got, "http://10.0.2.15:8787/") {
		t.Fatalf("url missing:\n%s", got)
	}
	bad := Render("short", []string{"10.0.2.15"}, 99999)
	if strings.Contains(bad, "Setup code") || !strings.Contains(bad, ":8787/") {
		t.Fatalf("invalid code or port:\n%s", bad)
	}
}

func TestPreferListenPutsTheBoundAddressFirst(t *testing.T) {
	in := []string{"10.0.2.15", "192.168.5.5"}
	got := preferListen(in, "192.168.5.5:8787")
	if len(got) != 2 || got[0] != "192.168.5.5" || got[1] != "10.0.2.15" {
		t.Fatalf("bound address: %v", got)
	}
	if back := preferListen(in, ":8787"); len(back) != 2 || back[0] != "10.0.2.15" {
		t.Fatalf("wildcard changed the order: %v", back)
	}
	if back := preferListen(in, "127.0.0.1:8787"); back[0] != "10.0.2.15" {
		t.Fatalf("loopback changed the order: %v", back)
	}
	extra := preferListen(in, "192.168.9.9:8787")
	if extra[0] != "192.168.9.9" || len(extra) != 3 {
		t.Fatalf("missing bind address: %v", extra)
	}
}

func TestDefaultRoutePicksTheLowestMetric(t *testing.T) {
	table := "" +
		"Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n" +
		"eth0\t00000000\t0100A8C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n" +
		"eth1\t00000000\t0101A8C0\t0003\t0\t0\t50\t00000000\t0\t0\t0\n" +
		"eth0\t0000A8C0\t00000000\t0001\t0\t0\t100\t00FFFFFF\t0\t0\t0\n"
	if got := defaultRouteInterface(table); got != "eth1" {
		t.Fatalf("default route %q", got)
	}
	if got := defaultRouteInterface("Iface Destination Gateway Flags RefCnt Use Metric Mask\neth0 0000A8C0 00000000 0001 0 0 100 00FFFFFF\n"); got != "" {
		t.Fatalf("connected route treated as default: %q", got)
	}
}

func TestSelectIPv4OrdersTheDefaultRouteAndSkipsVirtual(t *testing.T) {
	nics := []nic{
		{name: "lo", up: true, ips: []net.IP{net.ParseIP("127.0.0.1")}},
		{name: "docker0", up: true, ips: []net.IP{net.ParseIP("172.17.0.1")}},
		{name: "eth1", up: true, ips: []net.IP{net.ParseIP("192.168.5.5"), net.ParseIP("169.254.1.1")}},
		{name: "eth0", up: true, ips: []net.IP{net.ParseIP("192.168.1.20"), net.ParseIP("10.0.2.15")}},
		{name: "eth2", up: false, ips: []net.IP{net.ParseIP("192.168.8.8")}},
		{name: "virbr0", up: true, ips: []net.IP{net.ParseIP("192.168.122.1")}},
	}
	got := selectIPv4(nics, "eth0")
	want := []string{"10.0.2.15", "192.168.1.20", "192.168.5.5"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v want %v", got, want)
	}
	// The default route is listed even when the interface is virtual.
	docker := selectIPv4(nics, "docker0")
	if len(docker) == 0 || docker[0] != "172.17.0.1" {
		t.Fatalf("virtual default route: %v", docker)
	}
}

func TestWatchRewritesWhenTheAddressAndCodeChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "issue")
	var mu sync.Mutex
	code := "ABCD2345"
	var addrs []string
	var refreshes int
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		Watch(ctx, Options{
			Path:     path,
			Port:     8787,
			Interval: 15 * time.Millisecond,
			Log:      log,
			Code: func() string {
				mu.Lock()
				defer mu.Unlock()
				return code
			},
			Addrs: func() []string {
				mu.Lock()
				defer mu.Unlock()
				return append([]string(nil), addrs...)
			},
			Refresh: func(context.Context) error {
				mu.Lock()
				refreshes++
				mu.Unlock()
				return nil
			},
		})
	}()

	waitFor(t, path, func(body string) bool {
		return strings.Contains(body, "http://<no address yet>:8787/") &&
			strings.Contains(body, "Setup code: ABCD-2345")
	})
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("mode %o", info.Mode().Perm())
	}

	mu.Lock()
	addrs = []string{"10.0.2.15"}
	mu.Unlock()
	waitFor(t, path, func(body string) bool {
		return strings.Contains(body, "http://10.0.2.15:8787/") &&
			strings.Contains(body, "Setup code: ABCD-2345")
	})

	mu.Lock()
	code = ""
	mu.Unlock()
	waitFor(t, path, func(body string) bool {
		return strings.Contains(body, "http://10.0.2.15:8787/") &&
			!strings.Contains(body, "Setup code")
	})

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watch did not stop")
	}
	mu.Lock()
	n := refreshes
	mu.Unlock()
	if n < 3 {
		t.Fatalf("refreshes = %d, want one per banner change", n)
	}
	if strings.Contains(buf.String(), "ABCD") {
		t.Fatalf("log contains the setup code:\n%s", buf.String())
	}
}

func TestWatchRetriesRefreshUntilThePromptExists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "issue")
	var mu sync.Mutex
	ready := false
	calls := 0
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		Watch(ctx, Options{
			Path:     path,
			Port:     8787,
			Interval: 15 * time.Millisecond,
			Code:     func() string { return "ABCD2345" },
			Addrs:    func() []string { return []string{"10.0.2.15"} },
			Refresh: func(context.Context) error {
				mu.Lock()
				defer mu.Unlock()
				calls++
				if !ready {
					return errNoLoginPrompt
				}
				return nil
			},
		})
	}()
	waitCalls := func(atLeast int) int {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			mu.Lock()
			n := calls
			mu.Unlock()
			if n >= atLeast {
				return n
			}
			time.Sleep(5 * time.Millisecond)
		}
		mu.Lock()
		n := calls
		mu.Unlock()
		t.Fatalf("refresh calls = %d, want at least %d", n, atLeast)
		return n
	}
	waitFor(t, path, func(body string) bool {
		return strings.Contains(body, "Setup code: ABCD-2345")
	})
	before := waitCalls(2)
	mu.Lock()
	ready = true
	mu.Unlock()
	after := waitCalls(before + 1)
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	stopped := calls
	mu.Unlock()
	if stopped != after {
		t.Fatalf("refresh kept going: %d then %d", after, stopped)
	}
	cancel()
	<-done
}

func TestWatchRedrawsAgainWhenTheAddressAppears(t *testing.T) {
	prevLook, prevKick, prevUnits := lookGetty, kickGetty, gettyUnits
	t.Cleanup(func() {
		lookGetty, kickGetty, gettyUnits = prevLook, prevKick, prevUnits
	})
	gettyUnits = []string{"getty@tty1.service"}
	lookGetty = func(context.Context, string) gettyStatus { return gettyPrompt }
	var mu sync.Mutex
	var kicked int
	kickGetty = func(context.Context, string) error {
		mu.Lock()
		kicked++
		mu.Unlock()
		return nil
	}
	next, refresh := NewRefresher()
	dir := t.TempDir()
	path := filepath.Join(dir, "issue")
	var addrs []string
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Watch(ctx, Options{
		Path:       path,
		Port:       8787,
		Interval:   15 * time.Millisecond,
		Code:       func() string { return "ABCD2345" },
		NextRedraw: next,
		Refresh:    refresh,
		Addrs: func() []string {
			mu.Lock()
			defer mu.Unlock()
			return append([]string(nil), addrs...)
		},
	})
	waitKicks := func(atLeast int) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			mu.Lock()
			n := kicked
			mu.Unlock()
			if n >= atLeast {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("getty restarts = waiting for %d", atLeast)
	}
	waitKicks(1)
	mu.Lock()
	addrs = []string{"10.0.2.15"}
	mu.Unlock()
	waitFor(t, path, func(body string) bool {
		return strings.Contains(body, "http://10.0.2.15:8787/")
	})
	waitKicks(2)
	time.Sleep(40 * time.Millisecond)
	mu.Lock()
	n := kicked
	mu.Unlock()
	if n != 2 {
		t.Fatalf("getty restarted %d times, want 2", n)
	}
}

func TestWatchWaitsForTheDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "later", "issue")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Watch(ctx, Options{
		Path:     path,
		Port:     9,
		Interval: 15 * time.Millisecond,
		Code:     func() string { return "" },
		Addrs:    func() []string { return []string{"10.1.1.1"} },
		Refresh:  func(context.Context) error { return nil },
	})
	time.Sleep(30 * time.Millisecond)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) && err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	waitFor(t, path, func(body string) bool {
		return strings.Contains(body, "http://10.1.1.1:9/") && !strings.Contains(body, "Setup code")
	})
}

func TestRefresherRestartsEachPromptOnce(t *testing.T) {
	prevLook, prevKick, prevUnits := lookGetty, kickGetty, gettyUnits
	t.Cleanup(func() {
		lookGetty, kickGetty, gettyUnits = prevLook, prevKick, prevUnits
	})
	gettyUnits = []string{"getty@tty1.service", "serial-getty@ttyS0.service"}
	state := map[string]gettyStatus{
		"getty@tty1.service":         gettyAbsent,
		"serial-getty@ttyS0.service": gettyPrompt,
	}
	var kicked []string
	lookGetty = func(_ context.Context, unit string) gettyStatus { return state[unit] }
	kickGetty = func(_ context.Context, unit string) error {
		kicked = append(kicked, unit)
		return nil
	}
	next, refresh := NewRefresher()
	if err := refresh(context.Background()); !errors.Is(err, errNoLoginPrompt) {
		t.Fatalf("tty1 absent: %v", err)
	}
	if strings.Join(kicked, ",") != "serial-getty@ttyS0.service" {
		t.Fatalf("kicked %v", kicked)
	}
	if err := refresh(context.Background()); !errors.Is(err, errNoLoginPrompt) {
		t.Fatal(err)
	}
	if len(kicked) != 1 {
		t.Fatalf("serial restarted again: %v", kicked)
	}
	state["getty@tty1.service"] = gettyPrompt
	if err := refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Join(kicked, ",") != "serial-getty@ttyS0.service,getty@tty1.service" {
		t.Fatalf("kicked %v", kicked)
	}
	if err := refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(kicked) != 2 {
		t.Fatalf("extra kick %v", kicked)
	}
	// A new banner (the address arrived) redraws a prompt already showing
	// the previous text. A retry of that same redraw does not.
	next()
	if err := refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(kicked) != 4 {
		t.Fatalf("banner change did not redraw: %v", kicked)
	}
	if err := refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(kicked) != 4 {
		t.Fatalf("redraw repeated: %v", kicked)
	}
}

func TestRefresherLeavesALoggedInSession(t *testing.T) {
	prevLook, prevKick, prevUnits := lookGetty, kickGetty, gettyUnits
	t.Cleanup(func() {
		lookGetty, kickGetty, gettyUnits = prevLook, prevKick, prevUnits
	})
	gettyUnits = []string{"getty@tty1.service"}
	var kicked []string
	lookGetty = func(context.Context, string) gettyStatus { return gettyBusy }
	kickGetty = func(context.Context, string) error {
		kicked = append(kicked, "x")
		return nil
	}
	_, refresh := NewRefresher()
	if err := refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(kicked) != 0 {
		t.Fatalf("logged-in session restarted: %v", kicked)
	}
}

func waitFor(t *testing.T, path string, cond func(string) bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(path)
		if err == nil {
			last = string(b)
			if cond(last) {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("banner did not match, last:\n%s", last)
}
