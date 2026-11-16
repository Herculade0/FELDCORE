package docker

import "errors"

// asAPIError is errors.As specialised for *APIError.
func asAPIError(err error, target **APIError) bool {
	return errors.As(err, target)
}

// --- Wire types: the subset of Engine API fields FELDCORE reads. ---

// apiPort is a port mapping as the daemon reports it on a container.
type apiPort struct {
	IP          string `json:"IP"`
	PrivatePort int    `json:"PrivatePort"`
	PublicPort  int    `json:"PublicPort"`
	Type        string `json:"Type"`
}

// apiContainer is one entry from GET /containers/json.
type apiContainer struct {
	ID         string            `json:"Id"`
	Names      []string          `json:"Names"`
	Image      string            `json:"Image"`
	ImageID    string            `json:"ImageID"`
	Command    string            `json:"Command"`
	Created    int64             `json:"Created"`
	State      string            `json:"State"`
	Status     string            `json:"Status"`
	Ports      []apiPort         `json:"Ports"`
	Labels     map[string]string `json:"Labels"`
	HostConfig struct {
		NetworkMode string `json:"NetworkMode"`
	} `json:"HostConfig"`
	NetworkSettings struct {
		Networks map[string]struct {
			IPAddress string `json:"IPAddress"`
		} `json:"Networks"`
	} `json:"NetworkSettings"`
}

// apiImage is one entry from GET /images/json.
type apiImage struct {
	ID         string   `json:"Id"`
	RepoTags   []string `json:"RepoTags"`
	Size       int64    `json:"Size"`
	Created    int64    `json:"Created"`
	Containers int64    `json:"Containers"`
}

// inspectContainer is GET /containers/{id}/json. Config and HostConfig are
// kept as raw maps so that recreating a container preserves every setting,
// including the many FELDCORE does not model.
type inspectContainer struct {
	ID    string `json:"Id"`
	Name  string `json:"Name"`
	Image string `json:"Image"` // image id
	State struct {
		Running bool `json:"Running"`
	} `json:"State"`
	Config          map[string]any `json:"Config"`
	HostConfig      map[string]any `json:"HostConfig"`
	NetworkSettings struct {
		Networks map[string]map[string]any `json:"Networks"`
	} `json:"NetworkSettings"`
	Mounts []struct {
		Type        string `json:"Type"`
		Name        string `json:"Name"`
		Source      string `json:"Source"`
		Destination string `json:"Destination"`
	} `json:"Mounts"`
}

// --- View types: what the API hands the front-end. ---

// Port is a published port mapping.
type Port struct {
	Host      int    `json:"host"`
	Container int    `json:"container"`
	Proto     string `json:"proto"`
}

// Container is a container in a shape the UI can render directly.
type Container struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Image      string   `json:"image"`
	State      string   `json:"state"`  // running, exited, paused, created, ...
	Status     string   `json:"status"` // human text, e.g. "Up 3 hours"
	Created    int64    `json:"created"`
	Ports      []Port   `json:"ports"`
	Managed    bool     `json:"managed"`        // created by FELDCORE
	Compose    bool     `json:"compose"`        // owned by Docker Compose
	GPU        bool     `json:"gpu"`            // has GPU access granted by FELDCORE
	WebURL     string   `json:"webUrl"`         // best guess at a browser URL, if any
	Icon       string   `json:"icon,omitempty"` // http(s) icon, when the container has one
	Network    string   `json:"network"`        // network name(s)
	Isolated   bool     `json:"isolated,omitempty"`
	IPs        []string `json:"ips"` // address(es) the container answers on
	VPN        string   `json:"vpn,omitempty"`
	VPNGateway bool     `json:"vpnGateway,omitempty"`
	CPUPct     float64  `json:"cpuPct"` // live, 0 when not running
	MemUsed    int64    `json:"memUsed"`
	MemLimit   int64    `json:"memLimit"`
	// Role is set for containers FELDCORE runs for itself (tunnel, proxy).
	Role string `json:"role,omitempty"`
	// Hostname and PublicHost are filled in by the API from proxy and tunnel
	// routes so the container list can show the names a person can open.
	Hostname   string `json:"hostname,omitempty"`
	PublicHost string `json:"publicHost,omitempty"`
}

// Image is an available local image.
type Image struct {
	ID      string `json:"id"`
	Tag     string `json:"tag"`
	Size    int64  `json:"size"`
	Created int64  `json:"created"`
	InUse   bool   `json:"inUse"`
}

// Status describes whether Docker is usable on this machine.
type Status struct {
	Installed  bool   `json:"installed"`
	Running    bool   `json:"running"`
	CanInstall bool   `json:"canInstall"`
	Version    string `json:"version,omitempty"`
	APIVersion string `json:"apiVersion,omitempty"`
	Message    string `json:"message,omitempty"` // why it is not usable, if so
}

// Config types, mirroring Unraid's "Add Configuration" dialog.
const (
	TypePath     = "path"
	TypePort     = "port"
	TypeVariable = "variable"
	TypeLabel    = "label"
	TypeDevice   = "device"
)

// Config is one container setting. Which fields matter depends on Type:
//
//	path      Host, Container, Mode ("rw" / "ro")
//	port      HostPort, ContainerPort, Proto, HostIP
//	variable  Key, Value
//	label     Key, Value
//	device    Host, Container, Mode (cgroup permissions, default "rwm")
type Config struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Description string `json:"description"`

	Host      string `json:"host"`
	Container string `json:"container"`
	Mode      string `json:"mode"`

	HostPort      int    `json:"hostPort"`
	ContainerPort int    `json:"containerPort"`
	Proto         string `json:"proto"`
	HostIP        string `json:"hostIp"`

	Key   string `json:"key"`
	Value string `json:"value"`

	// Display, Required and Mask come from an Unraid template. Display is
	// "always", "advanced" or "hidden". Mask keeps a value out of the UI.
	Display  string `json:"display,omitempty"`
	Required bool   `json:"required,omitempty"`
	Mask     bool   `json:"mask,omitempty"`
}

// Spec is the editable definition of a container: what the create/edit dialog
// shows and submits.
type Spec struct {
	Name          string   `json:"name"`
	Image         string   `json:"image"`
	Network       string   `json:"network"`       // bridge, host, none or a named network
	RestartPolicy string   `json:"restartPolicy"` // no, unless-stopped, always, on-failure
	GPU           bool     `json:"gpu"`
	Configs       []Config `json:"configs"`
	Autostart     bool     `json:"autostart"` // start after creating

	// Filled in from an Unraid template. Empty for a container created by hand.
	WebUI       string `json:"webUi,omitempty"`
	Icon        string `json:"icon,omitempty"`
	ExtraParams string `json:"extraParams,omitempty"`
	Privileged  bool   `json:"privileged,omitempty"`

	// Isolate is "private" or "shared". Empty means private for a new app on
	// the bridge, and "leave it where it is" when editing. Links are other
	// containers that join this app's network. DeployAnyway acknowledges
	// high-risk settings from the security check.
	Isolate      string   `json:"isolate,omitempty"`
	Links        []string `json:"links,omitempty"`
	DeployAnyway bool     `json:"deployAnyway,omitempty"`

	// VPN is the tunnel id this container is routed through. Empty keeps the
	// container on Network. Gateway marks a tunnel's own gateway container
	// and is not accepted from the HTTP API.
	VPN     string `json:"vpn,omitempty"`
	Gateway bool   `json:"-"`
}

// SpecInfo is a container's current Spec plus whether it may be edited.
type SpecInfo struct {
	Spec     Spec   `json:"spec"`
	Editable bool   `json:"editable"`
	Reason   string `json:"reason,omitempty"` // why not, when Editable is false
	Running  bool   `json:"running"`
}

// GPU is a graphics device found on the host.
type GPU struct {
	Vendor    string `json:"vendor"` // amd, intel, nvidia or generic
	Name      string `json:"name"`
	Supported bool   `json:"supported"`      // FELDCORE can share it with containers
	Note      string `json:"note,omitempty"` // why not, or what is needed
}

// Options are the choices the container dialog offers.
type Options struct {
	Networks     []string    `json:"networks"`
	GPUs         []GPU       `json:"gpus"`
	GPUAvailable bool        `json:"gpuAvailable"`
	VPNs         []VPNChoice `json:"vpns"`
	// Peers are containers the form can link to this one.
	Peers []Peer `json:"peers"`
}

// Peer is a container that can join another app's network.
type Peer struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

// VPNChoice is one tunnel the container form can route through. The API
// fills it; Docker itself does not know about tunnels.
type VPNChoice struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Server   string `json:"server,omitempty"`
	Country  string `json:"country,omitempty"`
	Enabled  bool   `json:"enabled"`
}

// --- Wire types for creating a container (POST /containers/create). ---

type portBinding struct {
	HostIP   string `json:"HostIp"`
	HostPort string `json:"HostPort"`
}

type deviceMapping struct {
	PathOnHost        string `json:"PathOnHost"`
	PathInContainer   string `json:"PathInContainer"`
	CgroupPermissions string `json:"CgroupPermissions"`
}

type createdResponse struct {
	ID       string   `json:"Id"`
	Warnings []string `json:"Warnings"`
}

type apiStats struct {
	CPUStats struct {
		CPUUsage struct {
			TotalUsage int64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemCPUUsage int64 `json:"system_cpu_usage"`
		OnlineCPUs     int   `json:"online_cpus"`
	} `json:"cpu_stats"`
	PreCPUStats struct {
		CPUUsage struct {
			TotalUsage int64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemCPUUsage int64 `json:"system_cpu_usage"`
	} `json:"precpu_stats"`
	MemoryStats struct {
		Usage int64            `json:"usage"`
		Limit int64            `json:"limit"`
		Stats map[string]int64 `json:"stats"`
	} `json:"memory_stats"`
}
