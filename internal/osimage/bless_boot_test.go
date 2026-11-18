package osimage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// FELDCORE blesses a good boot with feldcore-bless.service, which renames the
// counted loader entry to the stable name. The stock systemd-bless-boot.service
// would race it and fail on every first boot, so the image build must mask it.
// Boot counting stays firmware-side in systemd-boot, so A/B fallback is kept.
func TestImageMasksStockBlessBoot(t *testing.T) {
	root := repoRoot(t)
	build, err := os.ReadFile(filepath.Join(root, "os", "build.sh"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(build)
	if !strings.Contains(text, `sudo ln -sfn /dev/null "${rootfs}/etc/systemd/system/systemd-bless-boot.service"`) {
		t.Fatal("build.sh does not mask systemd-bless-boot.service; it will fail on first boot alongside feldcore-bless.service")
	}
	// feldcore's own bless unit must stay enabled.
	if !strings.Contains(text, "feldcore-bless.service") {
		t.Fatal("build.sh no longer enables feldcore-bless.service")
	}
}
