package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Herculade0/feldcore/internal/auth"
	"github.com/Herculade0/feldcore/internal/backup"
	"github.com/Herculade0/feldcore/internal/secrets"
	"github.com/Herculade0/feldcore/internal/setup"
	"github.com/Herculade0/feldcore/internal/store"
	"github.com/Herculade0/feldcore/internal/system"
)

func TestBackupAPIScopesAndAudit(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	box, err := secrets.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	shares := filepath.Join(dir, "shares")
	if err := os.MkdirAll(filepath.Join(shares, "internal-affairs"), 0o755); err != nil {
		t.Fatal(err)
	}
	b := &backup.Service{
		Store: st, Secrets: box, DataDir: dir, SharesRoot: shares,
		Engine:  apiMemEngine{},
		Compose: func(context.Context) []string { return nil },
		Cmd:     apiAbsentCmd{},
	}
	accounts := auth.New(st)
	if err := accounts.Setup("admin", "correct horse"); err != nil {
		t.Fatal(err)
	}
	srv := New(Deps{
		Auth: accounts, System: system.NewCollector(), Backups: b,
		Setup: setup.New(setup.Deps{Store: st, DataDir: dir, Root: func() bool { return false }}),
	})
	cookie := loginCookie(t, srv)
	body := `{"name":"Nightly","kind":"local","enabled":true,"path":"` + filepath.Join(dir, "dest") + `","scheduleMode":"manual","keepDaily":7,"keepWeekly":4,"keepMonthly":6}`
	rec := call(srv, http.MethodPost, "/api/v1/backup/destinations", body, "", cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create %d %s", rec.Code, rec.Body.String())
	}
	var dest struct {
		ID   string `json:"id"`
		Sets struct {
			InternalAffairs bool `json:"internalAffairs"`
		} `json:"sets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &dest); err != nil {
		t.Fatal(err)
	}
	if !dest.Sets.InternalAffairs {
		t.Fatal("default set dropped Internal Affairs")
	}

	readTok, _, err := accounts.CreateToken("reader", []string{auth.ScopeBackupsRead}, 0)
	if err != nil {
		t.Fatal(err)
	}
	writeTok, _, err := accounts.CreateToken("writer", []string{auth.ScopeBackupsWrite}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if rec := call(srv, http.MethodGet, "/api/v1/backup", "", readTok, nil); rec.Code != http.StatusOK {
		t.Fatalf("read overview %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(srv, http.MethodPost, "/api/v1/backup/destinations/"+dest.ID+"/run", "", readTok, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("read run %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(srv, http.MethodGet, "/api/v1/backups", "", writeTok, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("tar.gz card %d %s", rec.Code, rec.Body.String())
	}
	rec = call(srv, http.MethodPost, "/api/v1/backup/destinations/"+dest.ID+"/run", "", writeTok, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("write run %d %s", rec.Code, rec.Body.String())
	}
	var job struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	waitAPIJob(t, srv, cookie, job.ID)

	if rec := call(srv, http.MethodPost, "/api/v1/backup/destinations/"+dest.ID+"/password", "", readTok, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("read password %d", rec.Code)
	}
	rec = call(srv, http.MethodPost, "/api/v1/backup/destinations/"+dest.ID+"/password", "", "", cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "password") {
		t.Fatalf("password %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(srv, http.MethodPost, "/api/v1/backup/destinations/"+dest.ID+"/password", "", writeTok, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("write token password %d %s", rec.Code, rec.Body.String())
	}
	replace := `{"destinationId":"` + dest.ID + `","snapshotId":"abcdef0123456789","mode":"replace","confirm":"replace","paths":["` + filepath.Join(shares, "library", "note.txt") + `"]}`
	if rec := call(srv, http.MethodPost, "/api/v1/backup/restore", replace, writeTok, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("write token replace %d %s", rec.Code, rec.Body.String())
	}

	log := call(srv, http.MethodGet, "/api/v1/audit", "", "", cookie)
	text := log.Body.String()
	if !strings.Contains(text, "backup destination saved") || !strings.Contains(text, "backup repository password revealed") {
		t.Fatalf("audit log missing backup actions: %s", text)
	}
}

func waitAPIJob(t *testing.T, srv *Server, cookie *http.Cookie, id string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rec := call(srv, http.MethodGet, "/api/v1/backup/jobs/"+id, "", "", cookie)
		if rec.Code == http.StatusOK && (strings.Contains(rec.Body.String(), `"succeeded"`) || strings.Contains(rec.Body.String(), `"failed"`)) {
			if strings.Contains(rec.Body.String(), `"failed"`) {
				t.Fatalf("job failed: %s", rec.Body.String())
			}
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatal("api job did not finish")
}

type apiAbsentCmd struct{}

func (apiAbsentCmd) Look(string) (string, error) { return "", os.ErrNotExist }
func (apiAbsentCmd) Run(context.Context, string, ...string) (string, error) {
	return "", os.ErrNotExist
}

type apiMemEngine struct{}

func (apiMemEngine) Available() error                          { return nil }
func (apiMemEngine) Ensure(context.Context, backup.Repo) error { return nil }
func (apiMemEngine) Probe(context.Context, backup.Repo) error  { return nil }
func (apiMemEngine) Backup(context.Context, backup.Repo, backup.BackupReq, backup.ProgressFunc) (backup.Summary, error) {
	return backup.Summary{SnapshotID: "abcdef0123456789"}, nil
}
func (apiMemEngine) Forget(context.Context, backup.Repo, string, backup.Retention) error { return nil }
func (apiMemEngine) Check(context.Context, backup.Repo, bool, backup.ProgressFunc) error { return nil }
func (apiMemEngine) Snapshots(context.Context, backup.Repo, string) ([]backup.Snapshot, error) {
	return nil, nil
}
func (apiMemEngine) List(context.Context, backup.Repo, string) ([]backup.Node, error) {
	return nil, nil
}
func (apiMemEngine) Restore(context.Context, backup.Repo, string, []string, string, backup.ProgressFunc) error {
	return nil
}
