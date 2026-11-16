package shares

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Herculade0/feldcore/internal/store"
)

var testID = identity{User: "nobody", Group: "nogroup", UID: 65534, GID: 65534}

func share(name string, smb store.SMB, nfs store.NFS) store.Share {
	return store.Share{Name: name, SMB: smb, NFS: nfs}
}

func TestRenderSMB(t *testing.T) {
	shares := []store.Share{
		share("media", store.SMB{Enabled: true, Access: store.SMBPublic}, store.NFS{}),
		share("docs", store.SMB{Enabled: true, Access: store.SMBPrivate, Users: []string{"alice", "bob"}}, store.NFS{}),
		share("iso", store.SMB{Enabled: true, Access: store.SMBReadOnly}, store.NFS{}),
		share("hidden", store.SMB{Enabled: false, Access: store.SMBPublic}, store.NFS{}),
	}
	shares[0].Comment = "Films and music"
	got := RenderSMB(shares, "/srv/feldcore", testID)

	for _, want := range []string{
		"[global]\n   map to guest = Bad User",
		"[media]\n   comment = Films and music\n   path = /srv/feldcore/media\n",
		"[docs]\n   path = /srv/feldcore/docs\n   browseable = yes\n   read only = no\n   guest ok = no\n   valid users = alice bob\n",
		"[iso]\n   path = /srv/feldcore/iso\n   browseable = yes\n   read only = yes\n   guest ok = yes\n",
		"force user = nobody\n   force group = nogroup\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered config is missing:\n%s\n--- got ---\n%s", want, got)
		}
	}
	if strings.Contains(got, "[hidden]") {
		t.Error("a share with SMB disabled must not be rendered")
	}
	// Sections are sorted, so output is stable between runs.
	if !(strings.Index(got, "[docs]") < strings.Index(got, "[iso]") &&
		strings.Index(got, "[iso]") < strings.Index(got, "[media]")) {
		t.Error("shares are not rendered in name order")
	}
}

func TestRenderSMBNoGuestMeansNoGlobalOverride(t *testing.T) {
	got := RenderSMB([]store.Share{
		share("docs", store.SMB{Enabled: true, Access: store.SMBPrivate, Users: []string{"alice"}}, store.NFS{}),
	}, "/srv/feldcore", testID)
	if strings.Contains(got, "[global]") {
		t.Errorf("private-only config should not touch [global]:\n%s", got)
	}
}

func TestRenderNFS(t *testing.T) {
	got := RenderNFS([]store.Share{
		share("media", store.SMB{}, store.NFS{Enabled: true, Clients: "192.168.0.0/24 10.0.0.5"}),
		share("backup", store.SMB{}, store.NFS{Enabled: true, Clients: "*", ReadOnly: true}),
		share("off", store.SMB{}, store.NFS{Enabled: false, Clients: "*"}),
	}, "/srv/feldcore", testID)

	opts := "sync,no_subtree_check,all_squash,anonuid=65534,anongid=65534"
	for _, want := range []string{
		"/srv/feldcore/media 192.168.0.0/24(rw," + opts + ") 10.0.0.5(rw," + opts + ")\n",
		"/srv/feldcore/backup *(ro," + opts + ")\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("exports are missing:\n%s\n--- got ---\n%s", want, got)
		}
	}
	if strings.Contains(got, "/srv/feldcore/off") {
		t.Error("a share with NFS disabled must not be exported")
	}
}

func TestNormaliseRejectsBadInput(t *testing.T) {
	users := []string{"alice"}
	cases := map[string]Input{
		"unknown access":          {SMB: store.SMB{Enabled: true, Access: "everyone"}},
		"private without users":   {SMB: store.SMB{Enabled: true, Access: store.SMBPrivate}},
		"unknown share user":      {SMB: store.SMB{Enabled: true, Access: store.SMBPrivate, Users: []string{"mallory"}}},
		"nfs without clients":     {NFS: store.NFS{Enabled: true}},
		"nfs option injection":    {NFS: store.NFS{Enabled: true, Clients: "*(rw,no_root_squash)"}},
		"nfs newline injection":   {NFS: store.NFS{Enabled: true, Clients: "10.0.0.1\n/etc *(rw)"}},
		"nfs shell metacharacter": {NFS: store.NFS{Enabled: true, Clients: "host;reboot"}},
	}
	for name, in := range cases {
		_, err := normalise(in, users)
		var ie *InputError
		if name == "nfs newline injection" {
			// Whitespace splits into separate tokens; the injected path
			// token must still be refused.
			if !errors.As(err, &ie) {
				t.Errorf("%s: expected a validation error, got %v", name, err)
			}
			continue
		}
		if !errors.As(err, &ie) {
			t.Errorf("%s: expected a validation error, got %v", name, err)
		}
	}
}

func TestNormaliseAcceptsAndCleans(t *testing.T) {
	sh, err := normalise(Input{
		Comment: "  Family\nphotos \\ ",
		SMB:     store.SMB{Enabled: true, Access: store.SMBPrivate, Users: []string{"bob", "alice", "bob"}},
		NFS:     store.NFS{Enabled: true, Clients: " 192.168.0.0/24, nas.lan  "},
	}, []string{"alice", "bob"})
	if err != nil {
		t.Fatal(err)
	}
	if sh.Comment != "Family photos" {
		t.Errorf("comment not cleaned: %q", sh.Comment)
	}
	if strings.Join(sh.SMB.Users, ",") != "alice,bob" {
		t.Errorf("users not deduplicated and sorted: %v", sh.SMB.Users)
	}
	if sh.NFS.Clients != "192.168.0.0/24 nas.lan" {
		t.Errorf("clients not normalised: %q", sh.NFS.Clients)
	}
}

func TestCleanCommentCannotBreakOutOfItsLine(t *testing.T) {
	got := cleanComment("nice\n[evil]\npath = /etc\\")
	if strings.ContainsAny(got, "\n\r\\") {
		t.Errorf("comment still contains line breaks or backslashes: %q", got)
	}
}

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	// Point every system path into the temp dir so tests never touch /etc.
	return New(st, Paths{
		SharesRoot:   filepath.Join(dir, "shares"),
		SambaConf:    filepath.Join(dir, "smb.conf"),
		SambaInclude: filepath.Join(dir, "feldcore-shares.conf"),
		ExportsFile:  filepath.Join(dir, "exports.d", "feldcore.exports"),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestCreateUpdateDeleteLifecycle(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	v, err := m.Create(ctx, Input{Name: "media", Comment: "Films"})
	if err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(v.Path); err != nil || !st.IsDir() {
		t.Fatalf("share folder was not created at %s", v.Path)
	}

	if _, err := m.Create(ctx, Input{Name: "MEDIA"}); err == nil {
		t.Error("a duplicate name differing only in case should be refused")
	}
	for _, bad := range []string{"", "has space", "../etc", "global", "a/b", "-lead", strings.Repeat("x", 33)} {
		if _, err := m.Create(ctx, Input{Name: bad}); err == nil {
			t.Errorf("name %q should be refused", bad)
		}
	}

	if _, err := m.Update(ctx, "media", Input{Comment: "Movies"}); err != nil {
		t.Fatal(err)
	}
	if got := m.Overview(ctx).Shares[0].Comment; got != "Movies" {
		t.Errorf("update not stored: %q", got)
	}
	if _, err := m.Update(ctx, "nope", Input{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("updating a missing share: got %v, want ErrNotFound", err)
	}

	// A folder holding data must survive the share being deleted.
	if err := os.WriteFile(filepath.Join(v.Path, "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	kept, err := m.Delete(ctx, "media")
	if err != nil {
		t.Fatal(err)
	}
	if !kept || !fileExists(filepath.Join(v.Path, "keep.txt")) {
		t.Error("deleting a share must keep a non-empty folder and its files")
	}

	// An empty folder is tidied away.
	v2, _ := m.Create(ctx, Input{Name: "empty"})
	if kept, _ := m.Delete(ctx, "empty"); kept || fileExists(v2.Path) {
		t.Error("deleting a share with an empty folder should remove the folder")
	}
}

func TestEnsureDefaults(t *testing.T) {
	ctx := context.Background()
	m := newTestManager(t)
	m.asRoot = func() bool { return true }

	// An existing appdata folder, with a file in it, must be adopted.
	appdata := m.dir("appdata")
	if err := os.MkdirAll(appdata, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(appdata, "keep-me")
	if err := os.WriteFile(marker, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(appdata)
	if err != nil {
		t.Fatal(err)
	}
	before := info.Mode()

	// An existing share is not overwritten.
	if _, err := m.Create(ctx, Input{
		Name:    "system",
		Comment: "leave this",
		SMB:     store.SMB{Enabled: true, Access: store.SMBReadOnly},
	}); err != nil {
		t.Fatal(err)
	}

	m.EnsureDefaults(ctx)
	m.EnsureDefaults(ctx) // second run changes nothing

	got := map[string]store.Share{}
	for _, sh := range m.st.Snapshot().Shares {
		if _, ok := got[sh.Name]; ok {
			t.Fatalf("share %s stored twice", sh.Name)
		}
		got[sh.Name] = sh
	}
	for _, name := range []string{"appdata", "system", "domains", "isos"} {
		sh, ok := got[name]
		if !ok {
			t.Errorf("missing default share %s", name)
			continue
		}
		if st, err := os.Stat(m.dir(name)); err != nil || !st.IsDir() {
			t.Errorf("folder for %s was not created", name)
		}
		if name != "system" && (sh.SMB.Enabled || sh.NFS.Enabled) {
			t.Errorf("%s should not be exported, smb=%v nfs=%v", name, sh.SMB.Enabled, sh.NFS.Enabled)
		}
	}
	for _, extra := range []string{"media", "data"} {
		if _, ok := got[extra]; ok {
			t.Errorf("Unraid does not ship a default %s share", extra)
		}
	}

	if data, err := os.ReadFile(marker); err != nil || string(data) != "mine" {
		t.Fatalf("adopted appdata folder was modified: %v %q", err, data)
	}
	if st, err := os.Stat(appdata); err != nil || st.Mode() != before {
		t.Errorf("adopted folder mode changed: was %v now %v", before, st.Mode())
	}
	sys := got["system"]
	if sys.Comment != "leave this" || !sys.SMB.Enabled || sys.SMB.Access != store.SMBReadOnly {
		t.Errorf("existing system share was overwritten: %+v", sys)
	}

	// Without root, nothing new is created.
	quiet := newTestManager(t)
	quiet.asRoot = func() bool { return false }
	quiet.EnsureDefaults(ctx)
	if len(quiet.st.Snapshot().Shares) != 0 {
		t.Fatalf("defaults created without root: %+v", quiet.st.Snapshot().Shares)
	}
	if _, err := os.Stat(quiet.dir("domains")); !os.IsNotExist(err) {
		t.Fatalf("folder created without root: %v", err)
	}
}
