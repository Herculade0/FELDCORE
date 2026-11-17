// Package compose stores Docker Compose stacks and deploys them with the
// Compose v2 CLI. Stack files live under the FELDCORE data directory so a
// backup can copy them. See docs/COMPOSE.md.
package compose

import (
	"time"

	"github.com/Herculade0/feldcore/internal/docker"
)

// DirName is the folder under the FELDCORE data directory that holds every
// stack. With the default data directory the path is /var/lib/feldcore/compose.
// Each stack is a subdirectory:
//
//	compose/<name>/docker-compose.yml
//	compose/<name>/.env
//	compose/<name>/feldcore.override.yml
//	compose/<name>/stack.json
const DirName = "compose"

const (
	composeFile  = "docker-compose.yml"
	envFile      = ".env"
	overrideFile = "feldcore.override.yml"
	metaFile     = "stack.json"
	readmeFile   = "README.txt"

	maxCompose = 512 << 10
	maxEnv     = 256 << 10
	maxLogTail = 1000
)

// Readme explains the on-disk layout to anyone reading a backup.
const Readme = `FELDCORE Compose stacks

Each subdirectory is one stack. docker-compose.yml is the file you edit in
the Stacks page. .env holds that stack's variables. feldcore.override.yml is
regenerated whenever the stack is saved or deployed: it labels the containers
and, unless the compose file already defines networks, attaches the stack to
its own bridge (feldcore-stack-<name>).

This folder is <data-dir>/compose. The default data directory is
/var/lib/feldcore, so the usual path is /var/lib/feldcore/compose.
Backups include this folder when it exists. See docs/COMPOSE.md.
`

// Finding is one risky setting on a service. It uses the same check as a
// single container.
type Finding struct {
	Service    string `json:"service"`
	ID         string `json:"id"`
	Severity   string `json:"severity"`
	Title      string `json:"title"`
	Reason     string `json:"reason"`
	Suggestion string `json:"suggestion"`
	Fix        bool   `json:"fix"`
}

// Fix asks the checker to remove one finding from the compose file.
type Fix struct {
	Service string `json:"service"`
	ID      string `json:"id"`
}

// ServiceInfo is one service as the editor lists it, before it is running.
type ServiceInfo struct {
	Name      string `json:"name"`
	Image     string `json:"image,omitempty"`
	Container string `json:"container,omitempty"`
}

// LiveService is one service as Docker Compose reports it.
type LiveService struct {
	Name      string   `json:"name"`
	Image     string   `json:"image,omitempty"`
	Container string   `json:"container,omitempty"`
	ID        string   `json:"id,omitempty"`
	State     string   `json:"state,omitempty"`
	Status    string   `json:"status,omitempty"`
	Ports     []string `json:"ports,omitempty"`
}

// Preview is a validated compose file that has not been deployed.
type Preview struct {
	Valid       bool          `json:"valid"`
	Error       string        `json:"error,omitempty"`
	Compose     string        `json:"compose"`
	Services    []ServiceInfo `json:"services"`
	Findings    []Finding     `json:"findings"`
	Notes       []string      `json:"notes,omitempty"`
	Diff        string        `json:"diff,omitempty"`
	Network     string        `json:"network,omitempty"`
	Project     string        `json:"project,omitempty"`
	Isolate     bool          `json:"isolate"`
	OwnNetworks bool          `json:"ownNetworks,omitempty"`
	Changed     bool          `json:"changed,omitempty"`
}

// Stack is one saved stack, including the files and the live containers.
type Stack struct {
	Name      string        `json:"name"`
	Compose   string        `json:"compose"`
	Env       string        `json:"env,omitempty"`
	Isolate   bool          `json:"isolate"`
	Source    string        `json:"source,omitempty"`
	GitURL    string        `json:"gitUrl,omitempty"`
	GitPath   string        `json:"gitPath,omitempty"`
	GitRef    string        `json:"gitRef,omitempty"`
	Replaces  string        `json:"replaces,omitempty"`
	CreatedAt time.Time     `json:"createdAt"`
	UpdatedAt time.Time     `json:"updatedAt"`
	State     string        `json:"state"`
	Network   string        `json:"network,omitempty"`
	Project   string        `json:"project"`
	Path      string        `json:"path"`
	Services  []LiveService `json:"services"`
	Notes     []string      `json:"notes,omitempty"`
}

// Summary is a stack without its file bodies, for the list.
type Summary struct {
	Name      string        `json:"name"`
	Isolate   bool          `json:"isolate"`
	Source    string        `json:"source,omitempty"`
	Replaces  string        `json:"replaces,omitempty"`
	UpdatedAt time.Time     `json:"updatedAt"`
	State     string        `json:"state"`
	Network   string        `json:"network,omitempty"`
	Project   string        `json:"project"`
	Path      string        `json:"path"`
	Services  []LiveService `json:"services"`
}

// Catalog is the Stacks page: every stack, and whether Compose is usable.
type Catalog struct {
	Root      string    `json:"root"`
	Compose   string    `json:"compose,omitempty"`
	ComposeOK bool      `json:"composeOk"`
	Stacks    []Summary `json:"stacks"`
}

// Imported is a compose file fetched from Git, not yet saved.
type Imported struct {
	Compose string   `json:"compose"`
	Env     string   `json:"env,omitempty"`
	Path    string   `json:"path"`
	URL     string   `json:"url"`
	Ref     string   `json:"ref,omitempty"`
	Notes   []string `json:"notes,omitempty"`
}

// Write is a request to store a stack. Deploy is done by the caller after
// Check accepts the file.
type Write struct {
	Name     string
	Compose  string
	Env      string
	Isolate  bool
	Source   string
	GitURL   string
	GitPath  string
	GitRef   string
	Replaces string
}

// Gate is a deploy the server will not start until the caller confirms the
// diff, acknowledges high-risk settings, or both.
type Gate struct {
	Msg      string
	Diff     string
	Findings []Finding
	Confirm  bool
	Ack      bool
}

func (e *Gate) Error() string {
	if e.Msg != "" {
		return e.Msg
	}
	if e.Ack {
		return "This stack has high-risk settings. Read the warnings or choose Deploy anyway."
	}
	return "Review the changes, then deploy."
}

// NeedsAck reports whether any finding is high severity.
func NeedsAck(in []Finding) bool {
	for _, f := range in {
		if f.Severity == docker.SeverityHigh {
			return true
		}
	}
	return false
}
