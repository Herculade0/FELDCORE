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
	// DataDir is where FELDCORE stores its own state (db, generated configs).
	DataDir string
}

// envOr returns the value of the named env var, or def when unset/empty.
func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// Load resolves configuration from flags first, then environment, then defaults.
// Env vars: FELDCORE_LISTEN, FELDCORE_DATA_DIR.
func Load() *Config {
	c := &Config{}
	flag.StringVar(&c.ListenAddr, "listen", envOr("FELDCORE_LISTEN", ":8787"),
		"address to listen on (host:port)")
	flag.StringVar(&c.DataDir, "data-dir", envOr("FELDCORE_DATA_DIR", "/var/lib/feldcore"),
		"directory for FELDCORE state")
	flag.Parse()
	return c
}
