package backup

import (
	"bytes"
	"compress/bzip2"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/Herculade0/feldcore/internal/appliance"
	"github.com/Herculade0/feldcore/internal/install"
)

// resticVersion is the official release used when apt has no package.
const resticVersion = "0.19.1"

// resticSHA256 is the official SHA256 of restic_<version>_linux_<arch>.bz2.
var resticSHA256 = map[string]string{
	"amd64": "f415415624dcc452f2a02b8c33641791a8c6d6d3b65bbb3543fcf9a25151585c",
	"arm64": "a5f64aaab53d51e311fa3829124c5b703f2d14cf187d8640b6be3b2b49376465",
}

// InstallRestic puts restic on the machine. apt is tried first when this
// process can use it. Otherwise the pinned official binary is downloaded,
// checked, and stored under dataDir/bin.
func InstallRestic(ctx context.Context, dataDir string) error {
	if _, err := exec.LookPath("restic"); err == nil {
		install.Line(ctx, "restic is already installed.")
		return nil
	}
	if dataDir != "" {
		if st, err := os.Stat(filepath.Join(dataDir, "bin", "restic")); err == nil && !st.IsDir() {
			install.Line(ctx, "restic is already installed.")
			return nil
		}
	}
	if os.Geteuid() == 0 && appliance.RealApt() {
		install.Line(ctx, "Installing restic with apt.")
		if _, err := install.Run(ctx, 8*time.Minute, "apt-get", "update"); err != nil {
			install.Line(ctx, "apt-get update failed; trying the install anyway.")
		}
		if _, err := install.Run(ctx, 15*time.Minute, "apt-get", "install", "-y", "restic"); err == nil {
			return nil
		}
		install.Line(ctx, "apt did not install restic. Downloading the official binary.")
	}
	return downloadRestic(ctx, dataDir)
}

func downloadRestic(ctx context.Context, dataDir string) error {
	sum, ok := resticSHA256[runtime.GOARCH]
	if !ok || dataDir == "" {
		return fmt.Errorf("there is no pinned restic binary for this machine")
	}
	name := "restic_" + resticVersion + "_linux_" + runtime.GOARCH + ".bz2"
	url := "https://github.com/restic/restic/releases/download/v" + resticVersion + "/" + name
	install.Line(ctx, "Downloading "+name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 4 * time.Minute}
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("restic could not be downloaded: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("restic download returned %s", res.Status)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 80<<20))
	if err != nil {
		return err
	}
	got := sha256.Sum256(body)
	if hex.EncodeToString(got[:]) != sum {
		return fmt.Errorf("the restic download did not match the pinned checksum")
	}
	zr := bzip2.NewReader(bytes.NewReader(body))
	bin, err := io.ReadAll(io.LimitReader(zr, 80<<20))
	if err != nil {
		return fmt.Errorf("the restic archive could not be unpacked")
	}
	dir := filepath.Join(dataDir, "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "restic")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, bin, 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	install.Line(ctx, "restic "+resticVersion+" is installed.")
	return nil
}
