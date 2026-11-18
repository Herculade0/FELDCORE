// Package config holds FELDCORE daemon runtime configuration.
package config

import (
	"flag"
	"os"
	"strings"
)

// Config is the resolved runtime configuration for feldcored.
type Config struct {
	// ListenAddr is the host:port the HTTP/API server binds to.
	ListenAddr string
	// DataDir is where FELDCORE stores its own state.
	DataDir string
	// SharesRoot is the directory under which share folders are created.
	SharesRoot string

	// SambaConf is the system Samba configuration FELDCORE hooks into.
	SambaConf string
	// SambaInclude is the FELDCORE-owned file that SambaConf includes.
	SambaInclude string
	// ExportsFile is the FELDCORE-owned NFS exports drop-in.
	ExportsFile string

	// DockerSocket is the Docker Engine API unix socket.
	DockerSocket string

	// DefaultShares creates Unraid's appdata, system, domains and isos
	// shares when they are missing. The wizard asks first; this flag covers
	// a headless install that skips the wizard.
	DefaultShares bool
	// SkipWizard finishes first-start setup without the browser wizard.
	// The one-time setup code is still required to claim the server.
	SkipWizard bool
}

// envOr returns the value of the named env var, or def when unset/empty.
func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// envBool reads a boolean env var. Empty means def. Unrecognised values
// also mean def, so a typo does not silently turn a safety switch off.
func envBool(name string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "":
		return def
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}

// SharesRootForced reports whether the operator set the shares directory
// explicitly. A wizard choice in the state file is used only when they did not.
func SharesRootForced() bool {
	if os.Getenv("FELDCORE_SHARES_ROOT") != "" {
		return true
	}
	for _, a := range os.Args[1:] {
		if a == "-shares-root" || strings.HasPrefix(a, "-shares-root=") {
			return true
		}
	}
	return false
}

// Load resolves configuration from flags first, then environment, then defaults.
//
// Env vars: FELDCORE_LISTEN, FELDCORE_DATA_DIR, FELDCORE_SHARES_ROOT,
// FELDCORE_SAMBA_CONF, FELDCORE_SAMBA_INCLUDE, FELDCORE_EXPORTS_FILE,
// FELDCORE_DEFAULT_SHARES, FELDCORE_SKIP_WIZARD.
func Load() *Config {
	c := &Config{
		SambaConf:    envOr("FELDCORE_SAMBA_CONF", "/etc/samba/smb.conf"),
		SambaInclude: envOr("FELDCORE_SAMBA_INCLUDE", "/etc/samba/feldcore-shares.conf"),
		ExportsFile:  envOr("FELDCORE_EXPORTS_FILE", "/etc/exports.d/feldcore.exports"),
		DockerSocket: envOr("FELDCORE_DOCKER_SOCKET", "/var/run/docker.sock"),
	}
	flag.StringVar(&c.ListenAddr, "listen", envOr("FELDCORE_LISTEN", ":8787"),
		"address to listen on (host:port)")
	flag.StringVar(&c.DataDir, "data-dir", envOr("FELDCORE_DATA_DIR", "/var/lib/feldcore"),
		"directory for FELDCORE state")
	flag.StringVar(&c.SharesRoot, "shares-root", envOr("FELDCORE_SHARES_ROOT", "/mnt/feldcore/data"),
		"directory under which share folders are created")
	flag.BoolVar(&c.DefaultShares, "default-shares", envBool("FELDCORE_DEFAULT_SHARES", true),
		"create Unraid's default shares (appdata, system, domains, isos) when missing")
	flag.BoolVar(&c.SkipWizard, "skip-wizard", envBool("FELDCORE_SKIP_WIZARD", false),
		"finish first-start setup without the browser wizard (scripted installs)")
	flag.Parse()
	return c
}
