// Package banner keeps the appliance login screen readable. agetty prints
// /etc/issue before the prompt and reads that file again each time it
// draws one, so the setup code and the web UI address stay on screen
// after kernel and journal lines have scrolled away.
package banner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Herculade0/feldcore/internal/setup"
)

// Options selects what the login banner says and where it is written.
type Options struct {
	// Path is the issue file. Empty means /etc/issue.
	Path string
	// Port is the web UI port. Zero and out-of-range values mean 8787.
	Port int
	// ListenAddr is the daemon listen address (host:port). A specific
	// IPv4 is listed first, because that is the address the UI bound.
	ListenAddr string
	// Code returns the current one-time setup code, or "" once it is gone.
	// It must not create a code.
	Code func() string
	// Addrs returns non-loopback IPv4 addresses, primary first.
	// Nil uses HostIPv4s.
	Addrs func() []string
	// Refresh redraws the login prompt after the file changes.
	// Nil only writes the file. The appliance passes NewRefresher.
	Refresh func(context.Context) error
	// NextRedraw runs when the banner text changes, before Refresh.
	// NewRefresher uses it so a prompt already drawn is drawn again
	// when an address or the setup code appears. Nil is safe.
	NextRedraw func()
	// Interval is how often the file is checked. Zero means 2s.
	Interval time.Duration
	Log      *slog.Logger
}

// Render is the text agetty prints above the login prompt.
// code is the raw or dashed setup code; an empty or invalid code is omitted.
// addrs are IPv4 addresses with the one to browse listed first.
func Render(code string, addrs []string, port int) string {
	port = portOr(port)
	var b strings.Builder
	b.WriteString("FELDCORE OS \\S (\\l)\n\n")
	writeURLs(&b, cleanAddrs(addrs), port)
	if pretty := setup.FormatCode(code); pretty != "" {
		fmt.Fprintf(&b, "\nSetup code: %s\nIt works once.\n", pretty)
	}
	return b.String()
}

func portOr(port int) int {
	if port < 1 || port > 65535 {
		return 8787
	}
	return port
}

func writeURLs(b *strings.Builder, addrs []string, port int) {
	if len(addrs) == 0 {
		fmt.Fprintf(b, "Web UI: http://<no address yet>:%d/\n", port)
		b.WriteString("Waiting for a network address.\n")
		return
	}
	if len(addrs) == 1 {
		fmt.Fprintf(b, "Web UI: %s\n", urlFor(addrs[0], port))
		return
	}
	b.WriteString("Web UI:\n")
	for _, ip := range addrs {
		fmt.Fprintf(b, "  %s\n", urlFor(ip, port))
	}
}

func urlFor(ip string, port int) string {
	return "http://" + net.JoinHostPort(ip, strconv.Itoa(port)) + "/"
}

func cleanAddrs(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, a := range in {
		a = strings.TrimSpace(a)
		if a == "" || seen[a] {
			continue
		}
		seen[a] = true
		out = append(out, a)
	}
	return out
}

// Watch writes the banner until ctx is cancelled. It rewrites the file when
// the code or the addresses change, and it asks Refresh to redraw a prompt
// that is already on screen. The first pass runs immediately.
func Watch(ctx context.Context, opt Options) {
	if opt.Interval <= 0 {
		opt.Interval = 2 * time.Second
	}
	if opt.Path == "" {
		opt.Path = "/etc/issue"
	}
	if opt.Addrs == nil {
		opt.Addrs = HostIPv4s
	}
	var (
		shown       string
		painted     bool
		loggedWrite bool
		loggedKick  bool
	)
	tick := func() {
		if ctx.Err() != nil {
			return
		}
		addrs := opt.addresses()
		code := ""
		if opt.Code != nil {
			code = opt.Code()
		}
		text := Render(code, addrs, opt.Port)
		if text != shown {
			if err := writeAtomic(opt.Path, text); err != nil {
				if !loggedWrite && opt.Log != nil {
					opt.Log.Warn("login banner was not written", "err", err)
					loggedWrite = true
				}
				return
			}
			loggedWrite = false
			shown = text
			painted = false
			loggedKick = false
			if opt.NextRedraw != nil {
				opt.NextRedraw()
			}
			if opt.Log != nil {
				opt.Log.Info("login banner updated",
					"addresses", len(cleanAddrs(addrs)),
					"setup_code", setup.FormatCode(code) != "")
			}
		}
		if painted || opt.Refresh == nil {
			return
		}
		err := opt.Refresh(ctx)
		if err == nil {
			painted = true
			return
		}
		if ctx.Err() != nil || errors.Is(err, errNoLoginPrompt) {
			return
		}
		if !loggedKick && opt.Log != nil {
			opt.Log.Warn("login prompt was not refreshed", "err", err)
			loggedKick = true
		}
	}
	tick()
	ticker := time.NewTicker(opt.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tick()
		}
	}
}

func (o Options) addresses() []string {
	var addrs []string
	if o.Addrs != nil {
		addrs = append(addrs, o.Addrs()...)
	}
	return preferListen(addrs, o.ListenAddr)
}

// preferListen puts the address the daemon bound in front of the list.
// A wildcard or loopback listen address does not change the order.
func preferListen(addrs []string, listenAddr string) []string {
	host := listenAddr
	if h, _, err := net.SplitHostPort(listenAddr); err == nil {
		host = h
	}
	ip := net.ParseIP(strings.TrimSpace(host)).To4()
	if ip == nil || !ip.IsGlobalUnicast() {
		return addrs
	}
	want := ip.String()
	out := make([]string, 0, len(addrs)+1)
	out = append(out, want)
	for _, a := range addrs {
		if a != want {
			out = append(out, a)
		}
	}
	return out
}

func writeAtomic(path, text string) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".issue-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := io.WriteString(f, text); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(0o644); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
