package backup

import (
	"context"
	"time"
)

// WhyRestic is the short reason the Backups page shows.
const WhyRestic = "Copies are restic repositories: encrypted, deduplicated, and kept as daily, weekly and monthly versions. Restic is used instead of Borg because it reports progress as JSON, and it can write through rclone to Proton Drive."

// Retention is how many versions to keep. Zero in every field keeps them all.
type Retention struct {
	Daily   int
	Weekly  int
	Monthly int
}

// Repo is one restic repository. Password is passed in the environment, never
// on the command line.
type Repo struct {
	Location     string
	Password     string
	SFTPArgs     string // extra ssh arguments, parsed by restic, not a shell
	RcloneConfig string
	Hidden       []string
}

// BackupReq is one restic backup invocation.
type BackupReq struct {
	Paths    []string
	Excludes []string
	Tags     []string
}

// Summary is the result of one backup.
type Summary struct {
	SnapshotID string
	Bytes      int64
	Files      int64
}

// Progress is one progress line from restic.
type Progress struct {
	Percent    float64
	BytesDone  int64
	BytesTotal int64
	FilesDone  int64
	FilesTotal int64
	SnapshotID string
	Line       string
	Err        string
}

// ProgressFunc receives progress. It may be nil.
type ProgressFunc func(Progress)

// Snapshot is one restic snapshot.
type Snapshot struct {
	ID       string    `json:"id"`
	ShortID  string    `json:"shortId"`
	Time     time.Time `json:"time"`
	Hostname string    `json:"hostname,omitempty"`
	Paths    []string  `json:"paths,omitempty"`
	Tags     []string  `json:"tags,omitempty"`
}

// Node is one file or directory inside a snapshot.
type Node struct {
	Name  string
	Type  string
	Path  string
	Size  int64
	Mtime time.Time
}

// Engine talks to a restic repository.
type Engine interface {
	Available() error
	Ensure(ctx context.Context, repo Repo) error
	Probe(ctx context.Context, repo Repo) error
	Backup(ctx context.Context, repo Repo, req BackupReq, on ProgressFunc) (Summary, error)
	Forget(ctx context.Context, repo Repo, destID string, policy Retention) error
	Check(ctx context.Context, repo Repo, readData bool, on ProgressFunc) error
	Snapshots(ctx context.Context, repo Repo, destID string) ([]Snapshot, error)
	List(ctx context.Context, repo Repo, snap string) ([]Node, error)
	Restore(ctx context.Context, repo Repo, snap string, paths []string, target string, on ProgressFunc) error
}

// Commander runs a host program such as virsh. Look reports whether it exists.
type Commander interface {
	Look(name string) (string, error)
	Run(ctx context.Context, name string, args ...string) (string, error)
}

// MountSpec is an SMB or NFS attach. Password is written to a credentials
// file by the mounter; it is not placed on the command line.
type MountSpec struct {
	Kind       string
	Source     string
	Mountpoint string
	Username   string
	Password   string
	Port       int
}

// Mounter attaches and detaches a network destination.
type Mounter interface {
	Mount(ctx context.Context, spec MountSpec) error
	Unmount(ctx context.Context, mountpoint string) error
}
