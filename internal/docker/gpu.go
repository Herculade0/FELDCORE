package docker

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// driDir is where the kernel exposes GPU device nodes. Passing it into a
// container is how AMD and Intel GPUs are shared; any number of containers
// can use the same GPU this way.
const driDir = "/dev/dri"

// PCI vendor ids as reported in sysfs.
var gpuVendors = map[string]struct{ key, name string }{
	"0x1002": {"amd", "AMD Radeon graphics"},
	"0x8086": {"intel", "Intel graphics"},
	"0x10de": {"nvidia", "NVIDIA graphics"},
}

// sysDRM is a variable so tests can point it at a fake tree.
var sysDRM = "/sys/class/drm"

// DetectGPUs lists the graphics devices on this machine and whether FELDCORE
// can share each with containers.
func DetectGPUs() []GPU {
	hasRender := false
	if nodes, _ := filepath.Glob(filepath.Join(driDir, "renderD*")); len(nodes) > 0 {
		hasRender = true
	}

	gpus := []GPU{}
	cards, _ := filepath.Glob(filepath.Join(sysDRM, "card[0-9]*"))
	sort.Strings(cards)
	for _, card := range cards {
		// Skip connector entries such as card0-HDMI-A-1.
		if strings.Contains(filepath.Base(card), "-") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(card, "device", "vendor"))
		if err != nil {
			continue
		}
		v, ok := gpuVendors[strings.TrimSpace(string(raw))]
		if !ok {
			continue
		}
		g := GPU{Vendor: v.key, Name: v.name}
		switch {
		case v.key == "nvidia":
			// NVIDIA needs its own container toolkit rather than /dev/dri.
			g.Note = "NVIDIA sharing isn't supported yet."
		case !hasRender:
			g.Note = "No render device was found in /dev/dri. The graphics driver may not be loaded."
		default:
			g.Supported = true
		}
		gpus = append(gpus, g)
	}

	// Some environments (WSL, certain VMs) expose a render device without the
	// usual sysfs description. It can still be shared.
	if len(gpus) == 0 && hasRender {
		gpus = append(gpus, GPU{Vendor: "generic", Name: "Graphics device (/dev/dri)", Supported: true})
	}
	return gpus
}

func gpuAvailable(gpus []GPU) bool {
	for _, g := range gpus {
		if g.Supported {
			return true
		}
	}
	return false
}

// grantFor works out what a spec asking for GPU access needs. It returns nil
// when the spec does not ask for it.
func (m *Manager) grantFor(s Spec) (*gpuGrant, error) {
	if !s.GPU {
		return nil, nil
	}
	if !gpuAvailable(DetectGPUs()) {
		return nil, invalid("This server has no GPU that FELDCORE can share with containers.")
	}
	return &gpuGrant{Device: driDir, Groups: deviceGroups(driDir)}, nil
}

// deviceGroups returns the numeric ids of the groups owning the device nodes
// in dir (typically "video" and "render"). A container process must be in
// them to open the GPU; ids are used because the names may not exist inside
// the container.
func deviceGroups(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || info.Mode()&os.ModeCharDevice == 0 {
			continue
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || st.Gid == 0 {
			continue
		}
		gid := strconv.FormatUint(uint64(st.Gid), 10)
		if !seen[gid] {
			seen[gid] = true
			out = append(out, gid)
		}
	}
	sort.Strings(out)
	return out
}
