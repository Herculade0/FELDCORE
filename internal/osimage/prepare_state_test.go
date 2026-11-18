package osimage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// prepareState returns the first-boot reconciliation script.
func prepareState(t *testing.T) string {
	t.Helper()
	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "os", "image", "usr", "lib", "feldcore", "prepare-state"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The /etc overlay is mounted by prepare-state, in userspace, after PID1 set
// the running hostname from the image's lower /etc/hostname. Setting only the
// static hostname there leaves the running name (what gopsutil and
// /api/v1/system/info report) on the image default. prepare-state must set the
// running hostname too.
func TestPrepareStateSetsRunningHostname(t *testing.T) {
	prep := prepareState(t)
	if !strings.Contains(prep, "hostname \"${name}\"") {
		t.Fatal("prepare-state does not set the running hostname; a reboot would revert /api/v1/system/info to the image default")
	}
	if !strings.Contains(prep, "hostnamectl set-hostname --static \"${name}\"") {
		t.Fatal("prepare-state no longer pins the static hostname")
	}
}

// The wizard's single-disk path writes a data-disk line into the overlay
// /etc/fstab. systemd-fstab-generator read the image's lower /etc/fstab at
// early boot, before the overlay existed, so prepare-state must mount anything
// the overlay added once the overlay is in place.
func TestPrepareStateMountsOverlayFstab(t *testing.T) {
	prep := prepareState(t)
	if !strings.Contains(prep, "mount -a") {
		t.Fatal("prepare-state does not mount overlay fstab entries; a wizard-selected data disk would never mount after reboot")
	}
}
