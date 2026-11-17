package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Herculade0/feldcore/internal/appliance"
)

// HasNVIDIA reports whether a PCI device with NVIDIA's vendor id is present.
func HasNVIDIA() bool {
	entries, err := os.ReadDir("/sys/bus/pci/devices")
	if err != nil {
		return false
	}
	for _, e := range entries {
		raw, err := os.ReadFile("/sys/bus/pci/devices/" + e.Name() + "/vendor")
		if err != nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(string(raw)), "0x10de") {
			return true
		}
	}
	return false
}

// MergeNVIDIARuntime adds the nvidia container runtime to daemon.json.
// It does not change the default runtime. A missing file is created.
func MergeNVIDIARuntime(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	cfg := map[string]any{}
	if len(strings.TrimSpace(string(raw))) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return fmt.Errorf("could not read Docker's configuration")
		}
	}
	runtimes, _ := cfg["runtimes"].(map[string]any)
	if runtimes == nil {
		runtimes = map[string]any{}
	}
	runtimes["nvidia"] = map[string]any{
		"path": "nvidia-container-runtime",
	}
	cfg["runtimes"] = runtimes
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	if err := os.MkdirAll(dirOf(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

func dirOf(path string) string {
	i := strings.LastIndex(path, "/")
	if i <= 0 {
		return "."
	}
	return path[:i]
}

// ConfigureNVIDIARuntime registers the toolkit runtime and restarts Docker.
// On FELDCORE OS the daemon.json lives on the state partition (or is reached
// through the /etc overlay) and is edited here. apt is not used.
func ConfigureNVIDIARuntime(ctx context.Context) error {
	const path = "/etc/docker/daemon.json"
	if err := MergeNVIDIARuntime(path); err != nil {
		return &OpError{"Could not register the NVIDIA runtime: " + err.Error()}
	}
	if !appliance.Is() {
		if _, err := exec.LookPath("nvidia-ctk"); err == nil {
			cmd := exec.CommandContext(ctx, "nvidia-ctk", "runtime", "configure", "--runtime=docker")
			cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
			if out, err := cmd.CombinedOutput(); err != nil {
				return &OpError{"nvidia-ctk could not configure Docker: " + strings.TrimSpace(string(out))}
			}
		}
	}
	if _, err := exec.LookPath("nvidia-ctk"); err == nil {
		_ = os.MkdirAll("/etc/cdi", 0o755)
		cmd := exec.CommandContext(ctx, "nvidia-ctk", "cdi", "generate", "--output=/etc/cdi/nvidia.yaml")
		_, _ = cmd.CombinedOutput()
	}
	return restartDocker(ctx)
}

func restartDocker(ctx context.Context) error {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "systemctl", "restart", "docker")
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return &OpError{"Docker did not restart after the NVIDIA runtime was added: " + msg}
	}
	return nil
}

// InstallNVIDIAToolkitDebian installs the toolkit with apt and registers it.
// FELDCORE OS refuses this and installs the sysext instead.
func InstallNVIDIAToolkitDebian(ctx context.Context) error {
	if appliance.Is() {
		return &OpError{"On FELDCORE OS the NVIDIA Container Toolkit is an extension, not an apt package."}
	}
	if os.Geteuid() != 0 {
		return &OpError{"FELDCORE must run as root to install the NVIDIA Container Toolkit."}
	}
	if _, ok := lookBin("apt-get"); !ok {
		return &OpError{"Automatic install needs apt. Install nvidia-container-toolkit, then reload this page."}
	}
	if _, err := run(ctx, 5*time.Minute, "apt-get", "update"); err != nil {
		// A third-party repository may be down. The install is the part that matters.
	}
	if _, err := run(ctx, 15*time.Minute, "apt-get", "install", "-y", "nvidia-container-toolkit"); err != nil {
		return &OpError{"Could not install the NVIDIA Container Toolkit: " + err.Error() + ". Debian needs NVIDIA's apt repository before this package can be installed."}
	}
	return ConfigureNVIDIARuntime(ctx)
}
