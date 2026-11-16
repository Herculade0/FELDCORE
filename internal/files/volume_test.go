package files

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupShare(t *testing.T) (root, outside string, vol *Volume) {
	t.Helper()
	parent := t.TempDir()
	root = filepath.Join(parent, "share")
	outside = filepath.Join(parent, "outside")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ok.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "abs")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../outside/secret.txt", filepath.Join(root, "rel")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("ok.txt", filepath.Join(root, "inside")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "outdir")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../outside/secret.txt", filepath.Join(root, "sub", "leak")); err != nil {
		t.Fatal(err)
	}
	var err error
	vol, err = OpenVolume(root)
	if err != nil {
		t.Fatal(err)
	}
	return root, outside, vol
}

func TestSymlinkInsideCanBeRead(t *testing.T) {
	_, _, vol := setupShare(t)
	f, info, err := vol.Open("inside")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b := make([]byte, 8)
	n, err := f.Read(b)
	if err != nil || string(b[:n]) != "hello" {
		t.Fatalf("got %q err %v size %d", b[:n], err, info.Size())
	}
}

func TestTraversalAndSymlinksStayInside(t *testing.T) {
	root, outside, vol := setupShare(t)
	outsideSecret := filepath.Join(outside, "secret.txt")

	refused := []string{
		"../outside/secret.txt",
		"..",
		"../..",
		"/etc/passwd",
		"foo/../../outside/secret.txt",
		"sub/../../outside/secret.txt",
		"abs",
		"rel",
		"sub/leak",
		"outdir/secret.txt",
		"ok.txt\x00",
		`..\..\outside\secret.txt`,
	}
	for _, p := range refused {
		if _, _, err := vol.Open(p); err == nil {
			t.Errorf("open %q succeeded", p)
		} else if _, ok := err.(*InputError); !ok && !errors.Is(err, ErrNotFound) {
			// A missing in-share path is fine. An escaping one must not be a raw OS error
			// that still managed to read the file. Not-found is acceptable only when the
			// cleaned path does not exist inside the share.
			if !errors.Is(err, ErrNotFound) {
				t.Errorf("open %q: %v", p, err)
			}
		}
		if _, err := os.ReadFile(outsideSecret); err != nil || string(must(os.ReadFile(outsideSecret))) != "secret" {
			t.Fatalf("outside file changed while opening %q", p)
		}
	}

	if err := vol.Mkdir("../outside/pwned"); err == nil {
		t.Fatal("mkdir escaped")
	}
	if _, err := os.Stat(filepath.Join(outside, "pwned")); !os.IsNotExist(err) {
		t.Fatal("mkdir created a folder outside the share")
	}
	ups := NewUploads()
	if _, err := ups.Create(root, "outdir/pwned.txt", 4, "overwrite"); err == nil {
		t.Fatal("upload through an escaping directory link was accepted")
	}
	if _, err := os.Stat(filepath.Join(outside, "pwned.txt")); !os.IsNotExist(err) {
		t.Fatal("upload wrote a file outside the share")
	}
	if _, err := ups.Create(root, "../outside/pwned.txt", 4, "fail"); err == nil {
		t.Fatal("upload with .. was accepted")
	}

	// Deleting a link must remove the link, not the file it points at.
	if _, err := vol.Delete([]string{"rel"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(outsideSecret); err != nil {
		t.Fatal("deleting a symlink removed the outside file", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "rel")); !os.IsNotExist(err) {
		t.Fatal("symlink was not removed from the share root")
	}

	// A folder that contains an escaping link can be removed without following it.
	if err := os.Mkdir(filepath.Join(root, "box"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideSecret, filepath.Join(root, "box", "leak")); err != nil {
		t.Fatal(err)
	}
	if _, err := vol.Delete([]string{"box"}); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(outsideSecret); err != nil || string(b) != "secret" {
		t.Fatal("removing a folder followed a symlink outside", err)
	}
}

func TestListHidesUploadsAndDoesNotEscape(t *testing.T) {
	root, _, vol := setupShare(t)
	list, err := vol.List(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range list.Entries {
		if e.Name == "secret.txt" || e.Name == UploadsDir || e.Name == TrashDir {
			t.Fatalf("listing exposed %s", e.Name)
		}
	}
	if _, err := vol.List("outdir"); err == nil {
		t.Fatal("listing an escaping directory link succeeded")
	}
	ups := NewUploads()
	created, err := ups.Create(root, "note.txt", 5, "fail")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ups.Write(root, created.ID, 0, strings.NewReader("hello"), 5); err != nil {
		t.Fatal(err)
	}
	list, err = vol.List(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range list.Entries {
		if e.Name == UploadsDir || strings.Contains(e.Name, ".part") {
			t.Fatalf("partial upload is visible: %+v", e)
		}
	}
}

func TestTrashThenPermanentDelete(t *testing.T) {
	root, outside, vol := setupShare(t)
	gone, err := vol.Delete([]string{"ok.txt"})
	if err != nil || len(gone) != 1 {
		t.Fatal(err, gone)
	}
	if _, err := os.Stat(filepath.Join(root, "ok.txt")); !os.IsNotExist(err) {
		t.Fatal("file still at the old path")
	}
	list, err := vol.List(TrashDir)
	if err != nil || len(list.Entries) != 1 {
		t.Fatalf("trash: %+v %v", list, err)
	}
	if _, err := os.Stat(filepath.Join(outside, "secret.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := vol.Delete([]string{list.Entries[0].Path}); err != nil {
		t.Fatal(err)
	}
	left, err := vol.List(TrashDir)
	if err != nil || len(left.Entries) != 0 {
		t.Fatalf("trash not empty: %+v %v", left, err)
	}
}

func TestUploadChunksResumeAndConflict(t *testing.T) {
	root, outside, _ := setupShare(t)
	ups := NewUploads()
	created, err := ups.Create(root, "big.bin", 6, "fail")
	if err != nil || created.Complete {
		t.Fatal(err, created)
	}
	mid, err := ups.Write(root, created.ID, 0, strings.NewReader("abc"), 3)
	if err != nil || mid.Offset != 3 || mid.Complete {
		t.Fatal(err, mid)
	}
	// A new table, as if the process restarted, continues from the stored offset.
	again := NewUploads()
	st, err := again.Status(root, created.ID)
	if err != nil || st.Offset != 3 {
		t.Fatal(err, st)
	}
	done, err := again.Write(root, created.ID, 3, strings.NewReader("def"), 3)
	if err != nil || !done.Complete {
		t.Fatal(err, done)
	}
	b, err := os.ReadFile(filepath.Join(root, "big.bin"))
	if err != nil || string(b) != "abcdef" {
		t.Fatalf("%q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(outside, "big.bin")); !os.IsNotExist(err) {
		t.Fatal("upload landed outside the share")
	}

	if _, err := ups.Create(root, "big.bin", 1, "fail"); !errors.As(err, new(*ConflictError)) {
		t.Fatalf("want conflict, got %v", err)
	}
	skipped, err := ups.Create(root, "big.bin", 1, "skip")
	if err != nil || !skipped.Skipped {
		t.Fatal(err, skipped)
	}
	renamed, err := ups.Create(root, "big.bin", 1, "rename")
	if err != nil || renamed.Path != "big (2).bin" {
		t.Fatal(err, renamed)
	}
	if _, err := ups.Write(root, renamed.ID, 0, strings.NewReader("Z"), 1); err != nil {
		t.Fatal(err)
	}
	over, err := ups.Create(root, "big.bin", 3, "overwrite")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ups.Write(root, over.ID, 0, strings.NewReader("xyz"), 3); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(filepath.Join(root, "big.bin"))
	if err != nil || string(b) != "xyz" {
		t.Fatalf("overwrite: %q %v", b, err)
	}

	empty, err := ups.Create(root, "nested/empty.txt", 0, "fail")
	if err != nil || !empty.Complete {
		t.Fatal(err, empty)
	}
	if st, err := os.Stat(filepath.Join(root, "nested", "empty.txt")); err != nil || st.Size() != 0 {
		t.Fatal(err, st)
	}
}

func TestMkdirCopyMoveZip(t *testing.T) {
	root, outside, vol := setupShare(t)
	if err := vol.Mkdir("album/day"); err != nil {
		t.Fatal(err)
	}
	if err := vol.Mkdir("album/day"); err != nil {
		t.Fatal("mkdir of an existing folder should succeed", err)
	}
	if st, err := os.Stat(filepath.Join(root, "album", "day")); err != nil || !st.IsDir() {
		t.Fatal(err)
	}
	copied, err := vol.Transfer([]string{"ok.txt"}, "album/day", "fail", true)
	if err != nil || len(copied) != 1 {
		t.Fatal(err, copied)
	}
	b, err := os.ReadFile(filepath.Join(root, "album", "day", "ok.txt"))
	if err != nil || string(b) != "hello" {
		t.Fatal(err, string(b))
	}
	if _, err := vol.Transfer([]string{"ok.txt"}, "album", "fail", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "ok.txt")); !os.IsNotExist(err) {
		t.Fatal("move left the original")
	}
	var buf bytes.Buffer
	if err := vol.WriteZip(&buf, []string{"album"}); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range zr.File {
		if strings.Contains(f.Name, "secret") || strings.Contains(f.Name, "..") {
			t.Fatalf("zip escaped: %s", f.Name)
		}
		if f.Name == "album/day/ok.txt" || f.Name == "album/ok.txt" {
			found = true
		}
	}
	if !found {
		var names []string
		for _, f := range zr.File {
			names = append(names, f.Name)
		}
		t.Fatalf("zip missing the file: %v", names)
	}
	if _, err := os.Stat(filepath.Join(outside, "secret.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestEmptyFolderAndRename(t *testing.T) {
	root, _, vol := setupShare(t)
	if err := vol.Mkdir("empty"); err != nil {
		t.Fatal(err)
	}
	list, err := vol.List("empty")
	if err != nil || len(list.Entries) != 0 {
		t.Fatal(err, list)
	}
	got, err := vol.Rename("empty", "vacant")
	if err != nil || got != "vacant" {
		t.Fatal(err, got)
	}
	if _, err := os.Stat(filepath.Join(root, "vacant")); err != nil {
		t.Fatal(err)
	}
}

func must(b []byte, err error) []byte {
	if err != nil {
		panic(err)
	}
	return b
}
