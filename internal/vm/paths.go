package vm

import (
	"path/filepath"
	"strings"
)

func within(parent, child string) bool {
	parent = filepath.Clean(parent)
	child = filepath.Clean(child)
	if parent == "" || child == "" {
		return false
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(osSep))
}

const osSep = string(filepath.Separator)

var blockedStorage = []string{
	"/proc", "/sys", "/dev", "/etc", "/boot", "/bin", "/sbin",
	"/usr", "/lib", "/lib64", "/root", "/run",
}

// validateStoragePath accepts an absolute folder an administrator chose for
// disks or ISOs. It refuses the operating-system directories.
func validateStoragePath(p string) error {
	if !filepath.IsAbs(p) {
		return invalid("Use an absolute path.")
	}
	c := filepath.Clean(p)
	if c != p {
		return invalid("That path is not valid.")
	}
	if strings.Count(c, "/") < 2 {
		return invalid("Choose a folder, not the root of a disk.")
	}
	for _, b := range blockedStorage {
		if c == b || strings.HasPrefix(c, b+"/") {
			return invalid("That folder is part of the operating system and cannot hold virtual disks.")
		}
	}
	return nil
}
