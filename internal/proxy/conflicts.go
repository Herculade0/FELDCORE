package proxy

import (
	"fmt"
	"strings"

	"github.com/Herculade0/feldcore/internal/docker"
)

// Conflict is a port the proxy needs and something else already has.
type Conflict struct {
	Port        int    `json:"port"`
	Owner       string `json:"owner"`
	Explanation string `json:"explanation"`
}

// Explain reports why Caddy cannot bind 80 and 443. Our own proxy container
// is not a conflict, and listeners are ignored while that container is already
// running (they are Caddy, or Docker's proxy in front of it).
func Explain(published []docker.PublishedPort, listening []int, caddyRunning bool) []Conflict {
	var out []Conflict
	for _, port := range []int{80, 443} {
		var owners []string
		for _, p := range published {
			if p.Port != port {
				continue
			}
			if p.Proto != "" && p.Proto != "tcp" {
				continue
			}
			if p.Role == "proxy" || p.Container == docker.ProxyContainer {
				continue
			}
			owners = append(owners, p.Container)
		}
		if len(owners) > 0 {
			out = append(out, Conflict{
				Port: port, Owner: owners[0],
				Explanation: explainContainer(port, owners),
			})
			continue
		}
		if !caddyRunning && contains(listening, port) {
			out = append(out, Conflict{
				Port: port, Owner: "another program",
				Explanation: fmt.Sprintf("Port %d is already used by another program on this server. Caddy needs ports 80 and 443 to serve friendly names and HTTPS. Find the program with ss -ltnp and stop it, or leave web addresses off.", port),
			})
		}
	}
	return out
}

func explainContainer(port int, owners []string) string {
	who := "the container \"" + owners[0] + "\""
	if len(owners) > 1 {
		who = "the containers \"" + strings.Join(owners, "\", \"") + "\""
	}
	return fmt.Sprintf("Port %d is already used by %s. Caddy needs ports 80 and 443 to serve friendly names and HTTPS. Stop that container, or change the host port it publishes, then turn web addresses on again.", port, who)
}

func contains(list []int, n int) bool {
	for _, v := range list {
		if v == n {
			return true
		}
	}
	return false
}
