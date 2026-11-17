package storage

import (
	"context"

	"github.com/Herculade0/feldcore/internal/install"
)

// Controller is the surface the first-run wizard calls. *Service implements
// it. The wizard does not shell out, and none of these methods format a disk
// on their own: CreatePool, AddDisk and ReplaceDisk refuse until the typed
// confirmation matches every disk.
type Controller interface {
	Inventory(ctx context.Context) (View, error)
	DiskHealth(ctx context.Context) ([]DiskHealth, error)
	CreatePool(ctx context.Context, in CreatePool) (Pool, error)
	DestroyPool(ctx context.Context, name, confirm string) error
	Mount(ctx context.Context, name string) error
	Unmount(ctx context.Context, name string) error
	AddDisk(ctx context.Context, in AddDisk) (AddResult, error)
	ReplaceDisk(ctx context.Context, in ReplaceDisk) (install.Job, error)
	SetCache(ctx context.Context, pool string) (CacheResult, error)
	CachePath() string
	BindShare(ctx context.Context, share, pool string) (Binding, error)
	UnbindShare(ctx context.Context, share string) error
	SharePath(share string) (string, bool)
	SetSpinDown(ctx context.Context, id string, minutes int) error
	SpinDownNow(ctx context.Context, id string) error
	SetSchedule(ctx context.Context, in Schedule) (Schedule, error)
	RunJob(ctx context.Context, pool, kind string) (install.Job, error)
}

var _ Controller = (*Service)(nil)
