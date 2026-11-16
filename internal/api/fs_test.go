package api

import (
	"os"
	"path/filepath"
	"testing"
)

func names(list []pathSuggestion) []string {
	out := []string{}
	for _, s := range list {
		out = append(out, s.Name)
	}
	return out
}

func TestListDirs(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"media", "Movies", "music", "backups", ".hidden"} {
		if err := os.Mkdir(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A symlink to a folder counts as a folder.
	if err := os.Symlink(filepath.Join(root, "media"), filepath.Join(root, "mlink")); err != nil {
		t.Fatal(err)
	}

	eq := func(label string, got, want []string) {
		t.Helper()
		if len(got) != len(want) {
			t.Errorf("%s: got %v, want %v", label, got, want)
			return
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("%s: got %v, want %v", label, got, want)
				return
			}
		}
	}

	// A trailing slash lists everything inside: folders only, no files, no
	// hidden folders.
	eq("list children", names(listDirs(root+"/")),
		[]string{"Movies", "backups", "media", "mlink", "music"})

	// A partial name filters, ignoring case.
	eq("prefix m", names(listDirs(root+"/m")), []string{"Movies", "media", "mlink", "music"})
	eq("prefix MU", names(listDirs(root+"/MU")), []string{"music"})

	// Hidden folders appear once a dot is typed.
	eq("hidden", names(listDirs(root+"/.")), []string{".hidden"})

	// A folder that doesn't exist yields nothing rather than an error.
	eq("missing", names(listDirs(root+"/nope/")), []string{})

	// Results carry the full path, ready to drop into the field.
	if got := listDirs(root + "/back"); len(got) != 1 || got[0].Path != filepath.Join(root, "backups") {
		t.Errorf("path = %+v", got)
	}
}
