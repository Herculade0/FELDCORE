package appliance

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "feldcore-os-release")
	if err := os.WriteFile(path, []byte("ID=feldcore\nVERSION_ID=0.1.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	restore := SetReleasePath(path)
	defer restore()
	if !Is() {
		t.Fatal("ID=feldcore was not recognised")
	}
	if RealApt() {
		t.Fatal("the image must not count as having apt")
	}
	if err := os.WriteFile(path, []byte("ID=debian\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if Is() {
		t.Fatal("debian was treated as the image")
	}
}

func TestMissingRelease(t *testing.T) {
	restore := SetReleasePath(filepath.Join(t.TempDir(), "missing"))
	defer restore()
	if Is() {
		t.Fatal("a missing release file looked like the image")
	}
}
