// Package config holds FELDCORE daemon runtime configuration.
package config

import (
	"flag"
	"os"
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
}

// envOr returns the value of the named env var, or def when unset/empty.
func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// Load resolves configuration from flags first, then environment, then defaults.
//
// Env vars: FELDCORE_LISTEN, FELDCORE_DATA_DIR, FELDCORE_SHARES_ROOT,
// FELDCORE_SAMBA_CONF, FELDCORE_SAMBA_INCLUDE, FELDCORE_EXPORTS_FILE.
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
	flag.StringVar(&c.SharesRoot, "shares-root", envOr("FELDCORE_SHARES_ROOT", "/srv/feldcore"),
		"directory under which share folders are created")
	flag.Parse()
	return c
}
