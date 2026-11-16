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
	ID      string            `json:"Id"`
	Names   []string          `json:"Names"`
	Image   string            `json:"Image"`
	ImageID string            `json:"ImageID"`
	Command string            `json:"Command"`
	Created int64             `json:"Created"`
	State   string            `json:"State"`
	Status  string            `json:"Status"`
	Ports   []apiPort         `json:"Ports"`
	Labels  map[string]string `json:"Labels"`
}

// apiImage is one entry from GET /images/json.
type apiImage struct {
	ID         string   `json:"Id"`
	RepoTags   []string `json:"RepoTags"`
	Size       int64    `json:"Size"`
	Created    int64    `json:"Created"`
	Containers int64    `json:"Containers"`
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
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Image    string  `json:"image"`
	State    string  `json:"state"`  // running, exited, paused, created, ...
	Status   string  `json:"status"` // human text, e.g. "Up 3 hours"
	Created  int64   `json:"created"`
	Ports    []Port  `json:"ports"`
	Managed  bool    `json:"managed"` // created by FELDCORE
	WebURL   string  `json:"webUrl"`  // best guess at a browser URL, if any
	CPUPct   float64 `json:"cpuPct"`  // live, 0 when not running
	MemUsed  int64   `json:"memUsed"`
	MemLimit int64   `json:"memLimit"`
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

// CreateRequest is the UI's request to run a new container.
type CreateRequest struct {
	Name          string            `json:"name"`
	Image         string            `json:"image"`
	Ports         []PortMapping     `json:"ports"`
	Volumes       []VolumeMapping   `json:"volumes"`
	Env           []EnvVar          `json:"env"`
	RestartPolicy string            `json:"restartPolicy"` // no, unless-stopped, always, on-failure
	Autostart     bool              `json:"autostart"`     // start immediately after create
	Labels        map[string]string `json:"labels,omitempty"`
}

// PortMapping publishes a container port on the host.
type PortMapping struct {
	Host      int    `json:"host"`
	Container int    `json:"container"`
	Proto     string `json:"proto"` // tcp or udp
}

// VolumeMapping bind-mounts a host path into the container.
type VolumeMapping struct {
	Host      string `json:"host"`
	Container string `json:"container"`
	ReadOnly  bool   `json:"readOnly"`
}

// EnvVar is one environment variable.
type EnvVar struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// --- Wire types for creating a container (POST /containers/create). ---

type createBody struct {
	Image        string              `json:"Image"`
	Env          []string            `json:"Env,omitempty"`
	Labels       map[string]string   `json:"Labels,omitempty"`
	ExposedPorts map[string]struct{} `json:"ExposedPorts,omitempty"`
	HostConfig   createHostConfig    `json:"HostConfig"`
}

type createHostConfig struct {
	Binds         []string                 `json:"Binds,omitempty"`
	PortBindings  map[string][]portBinding `json:"PortBindings,omitempty"`
	RestartPolicy restartPolicy            `json:"RestartPolicy"`
}

type portBinding struct {
	HostIP   string `json:"HostIp"`
	HostPort string `json:"HostPort"`
}

type restartPolicy struct {
	Name string `json:"Name"`
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
