package storage

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// readOnlyFolderMsg is the plan and confirm failure when a folder cannot be
// created because its parent is on the read-only operating-system image.
const readOnlyFolderMsg = "That folder is on the read-only operating-system image, so FELDCORE can't create it. Pick a folder under /mnt/feldcore."

// mkdirAll creates a mount directory. Tests replace it to simulate a
// read-only parent after the plan check has passed.
var mkdirAll = os.MkdirAll

// createProbe creates and removes a temporary file in dir. Tests replace it.
// A nil error means the directory accepted the file.
var createProbe = probeCreate

func probeCreate(dir string) error {
	f, err := os.CreateTemp(dir, ".feldcore-write-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_ = f.Close()
	return os.Remove(name)
}

// blocksCreate reports whether err means the directory cannot be created
// on a read-only mount. Permission errors for a non-root process are not
// that case: unit tests name absolute folders such as /mnt/feldcore without
// owning /mnt. Root is not stopped by mode bits, so EACCES or EPERM for
// root is treated as not writable.
func blocksCreate(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.EROFS) {
		return true
	}
	if os.Geteuid() == 0 && (errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM)) {
		return true
	}
	return false
}

// CheckMountCreatable fails when path is not an existing usable directory
// and the nearest existing parent cannot take a new folder. It does not
// format, mount, or leave a file behind.
func CheckMountCreatable(path string) error {
	path = filepath.Clean(path)
	info, err := os.Stat(path)
	if err == nil && info.IsDir() && createProbe(path) == nil {
		return nil
	}
	parent := existingDir(filepath.Dir(path))
	if parent == "" || blocksCreate(createProbe(parent)) {
		return input(readOnlyFolderMsg)
	}
	return nil
}

func existingDir(path string) string {
	path = filepath.Clean(path)
	for {
		info, err := os.Stat(path)
		if err == nil && info.IsDir() {
			return path
		}
		parent := filepath.Dir(path)
		if parent == path {
			return ""
		}
		path = parent
	}
}
