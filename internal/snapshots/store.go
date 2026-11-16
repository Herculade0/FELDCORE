package snapshots

import (
	"github.com/Herculade0/feldcore/internal/store"
)

// stateStore adapts the FELDCORE state file to the snapshot service.
type stateStore struct{ st *store.Store }

func (a stateStore) Snapshot() (int, int, bool, string) {
	c := a.st.Snapshot().Snapshots
	return c.KeepN, c.KeepDays, c.Configured, c.Dir
}

func (a stateStore) SetRetention(keepN, keepDays int, dir string) error {
	return a.st.Update(func(st *store.State) error {
		st.Snapshots.KeepN = keepN
		st.Snapshots.KeepDays = keepDays
		st.Snapshots.Configured = true
		st.Snapshots.Dir = dir
		return nil
	})
}

// Open returns a service that keeps retention in the state file.
func Open(st *store.Store, rt Runtime, dataDir string, locate func() string) *Service {
	return &Service{Runtime: rt, Store: stateStore{st}, DataDir: dataDir, Locate: locate}
}
