package storage

import (
	"time"

	"github.com/Herculade0/feldcore/internal/install"
)

// Filesystems and layouts a pool can use. ZFS layouts are offered only when
// the zpool command can talk to the kernel module.
const (
	FSBtrfs = "btrfs"
	FSZFS   = "zfs"
	FSXFS   = "xfs"

	LayoutSingle = "single"
	LayoutRAID1  = "raid1"
	LayoutRAID10 = "raid10"
	LayoutMirror = "mirror"
	LayoutRAIDZ1 = "raidz1"
	LayoutRAIDZ2 = "raidz2"

	KindScrub   = "scrub"
	KindBalance = "balance"
	KindTrim    = "trim"
)

// SMART is the summary another part of FELDCORE (notifications) can alert on.
// This package records it and does not send mail.
type SMART struct {
	Available    bool   `json:"available"`
	Healthy      string `json:"healthy"`
	TempC        *int   `json:"tempC,omitempty"`
	PowerOnHours *int64 `json:"powerOnHours,omitempty"`
	Reallocated  *int64 `json:"reallocated,omitempty"`
	Pending      *int64 `json:"pending,omitempty"`
	MediaErrors  *int64 `json:"mediaErrors,omitempty"`
	Message      string `json:"message,omitempty"`
}

// Disk is one whole disk. Confirm is the string that must be typed before
// FELDCORE will erase it: the hardware serial, or the by-id name when the
// disk has no serial.
type Disk struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Path        string   `json:"path"`
	ByID        string   `json:"byId,omitempty"`
	Model       string   `json:"model,omitempty"`
	Serial      string   `json:"serial,omitempty"`
	Confirm     string   `json:"confirm,omitempty"`
	ConfirmKind string   `json:"confirmKind,omitempty"`
	SizeBytes   uint64   `json:"sizeBytes"`
	Type        string   `json:"type"`
	Transport   string   `json:"transport,omitempty"`
	TempC       *int     `json:"tempC,omitempty"`
	SMART       SMART    `json:"smart"`
	UsedFor     string   `json:"usedFor"`
	Mountpoint  string   `json:"mountpoint,omitempty"`
	ChildMounts []string `json:"childMounts,omitempty"`
	FSType      string   `json:"fsType,omitempty"`
	UUID        string   `json:"uuid,omitempty"`
	Pool        string   `json:"pool,omitempty"`
	OS          bool     `json:"os"`
	Eligible    bool     `json:"eligible"`
	Blocked     string   `json:"blocked,omitempty"`
	Partitions  int      `json:"partitions"`
	Warnings    []string `json:"warnings,omitempty"`
	Rotational  bool     `json:"rotational"`
}

// DiskHealth is the slice the notifications agent should read. Storage does
// not decide that a disk is alarming and does not send mail.
type DiskHealth struct {
	ID           string `json:"id"`
	Model        string `json:"model,omitempty"`
	Serial       string `json:"serial,omitempty"`
	Path         string `json:"path"`
	ByID         string `json:"byId,omitempty"`
	TempC        *int   `json:"tempC,omitempty"`
	SMARTHealthy string `json:"smartHealthy"`
	Reallocated  *int64 `json:"reallocated,omitempty"`
	Pending      *int64 `json:"pending,omitempty"`
	MediaErrors  *int64 `json:"mediaErrors,omitempty"`
	UsedFor      string `json:"usedFor"`
	Message      string `json:"message,omitempty"`
}

// Member is one disk that belongs to a pool, remembered by serial and by-id
// so a reboot that renumbers /dev nodes still finds it.
type Member struct {
	ID        string `json:"id"`
	Serial    string `json:"serial,omitempty"`
	ByID      string `json:"byId,omitempty"`
	Path      string `json:"path"`
	SizeBytes uint64 `json:"sizeBytes"`
}

// Pool is a filesystem FELDCORE created, mounted by UUID under the mount root.
type Pool struct {
	Name         string    `json:"name"`
	FS           string    `json:"fs"`
	Layout       string    `json:"layout"`
	UUID         string    `json:"uuid"`
	Mount        string    `json:"mount"`
	Members      []Member  `json:"members"`
	Cache        bool      `json:"cache"`
	CreatedAt    time.Time `json:"createdAt"`
	Mounted      bool      `json:"mounted"`
	TotalBytes   uint64    `json:"totalBytes"`
	UsedBytes    uint64    `json:"usedBytes"`
	UsedPercent  float64   `json:"usedPercent"`
	Health       string    `json:"health"`
	HealthDetail string    `json:"healthDetail,omitempty"`
	BootMount    bool      `json:"bootMount"`
}

// LayoutInfo describes one filesystem layout the create form can offer.
type LayoutInfo struct {
	FS        string `json:"fs"`
	Layout    string `json:"layout"`
	Label     string `json:"label"`
	Min       int    `json:"min"`
	Max       int    `json:"max,omitempty"`
	Even      bool   `json:"even,omitempty"`
	Available bool   `json:"available"`
}

// Caps says which tools are actually installed. A missing tool is not installed
// by FELDCORE.
type Caps struct {
	Btrfs    bool         `json:"btrfs"`
	XFS      bool         `json:"xfs"`
	XFSScrub bool         `json:"xfsScrub"`
	ZFS      bool         `json:"zfs"`
	SMART    bool         `json:"smart"`
	Hdparm   bool         `json:"hdparm"`
	Trim     bool         `json:"trim"`
	Layouts  []LayoutInfo `json:"layouts"`
}

// Schedule is a scrub, balance or trim on the install job runner.
type Schedule struct {
	Pool       string    `json:"pool"`
	Kind       string    `json:"kind"`
	Enabled    bool      `json:"enabled"`
	EveryHours int       `json:"everyHours"`
	LastRun    time.Time `json:"lastRun,omitempty"`
	NextRun    time.Time `json:"nextRun,omitempty"`
	LastResult string    `json:"lastResult,omitempty"`
}

// SpinRule is the idle time before a spinning disk parks its heads.
// Minutes 0 means spin-down is off.
type SpinRule struct {
	ID      string `json:"id"`
	Serial  string `json:"serial,omitempty"`
	Minutes int    `json:"minutes"`
	Applied bool   `json:"applied"`
	Note    string `json:"note,omitempty"`
}

// Binding places one share on one pool. The files stay where they are until
// someone moves them; FELDCORE only creates the folder and a link.
type Binding struct {
	Share string `json:"share"`
	Pool  string `json:"pool"`
	Path  string `json:"path"`
}

// ShareRow is a share and the pool it is placed on, if any.
type ShareRow struct {
	Name string `json:"name"`
	Pool string `json:"pool,omitempty"`
	Path string `json:"path"`
}

// CreatePool is a request to format disks into a new pool.
// Disks are disk ids from Inventory. Confirm is the typed serial (or by-id
// name) of each disk, in the same order. A mismatch formats nothing.
type CreatePool struct {
	Name    string   `json:"name"`
	FS      string   `json:"fs"`
	Layout  string   `json:"layout"`
	Disks   []string `json:"disks"`
	Confirm []string `json:"confirm"`
}

// AddDisk adds one blank disk to an existing pool.
type AddDisk struct {
	Pool    string `json:"pool"`
	Disk    string `json:"disk"`
	Confirm string `json:"confirm"`
}

// ReplaceDisk copies a pool member onto a new disk. The new disk is erased.
type ReplaceDisk struct {
	Pool    string `json:"pool"`
	Old     string `json:"old"`
	Disk    string `json:"disk"`
	Confirm string `json:"confirm"`
}

// AddResult is a pool after a disk was added. Job is set when a balance was
// started so a btrfs raid layout actually uses the new disk.
type AddResult struct {
	Pool Pool         `json:"pool"`
	Job  *install.Job `json:"job,omitempty"`
}

// CacheResult is where appdata lives after the cache pool changes.
// Existing files are not moved.
type CacheResult struct {
	Pool string `json:"pool,omitempty"`
	Path string `json:"path,omitempty"`
	Note string `json:"note"`
}

// View is the storage page: disks, pools, and the choices around them.
type View struct {
	Privileged bool          `json:"privileged"`
	Caps       Caps          `json:"caps"`
	Disks      []Disk        `json:"disks"`
	Pools      []Pool        `json:"pools"`
	Schedules  []Schedule    `json:"schedules"`
	SpinDown   []SpinRule    `json:"spinDown"`
	Shares     []ShareRow    `json:"shares"`
	CachePool  string        `json:"cachePool,omitempty"`
	CachePath  string        `json:"cachePath,omitempty"`
	MountRoot  string        `json:"mountRoot"`
	Jobs       []install.Job `json:"jobs"`
}
