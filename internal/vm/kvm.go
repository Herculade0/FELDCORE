package vm

import (
	"os"
	"strings"
)

// KVMStatus reports whether /dev/kvm can be used, and a message when it cannot.
// A missing device is explained rather than left for a later start to fail
// with a raw QEMU error. WSL without nested virtualisation is the common case:
// the CPU flags may still be present while /dev/kvm is not.
func KVMStatus() (bool, string) {
	return kvmStatus("/dev/kvm", "/proc/cpuinfo", "/proc/version")
}

func kvmStatus(dev, cpuinfo, version string) (bool, string) {
	if _, err := os.Stat(dev); err == nil {
		return true, ""
	}
	cpu, _ := os.ReadFile(cpuinfo)
	ver, _ := os.ReadFile(version)
	return false, kvmMessage(string(cpu), string(ver))
}

func kvmMessage(cpuinfo, version string) string {
	low := strings.ToLower(version)
	wsl := strings.Contains(low, "microsoft") || strings.Contains(low, "wsl")
	if wsl {
		return "KVM is not available: /dev/kvm does not exist. This looks like WSL without nested virtualisation. On Windows, add nestedVirtualization=true under [wsl2] in %UserProfile%\\.wslconfig, run wsl --shutdown, and open the distro again. Intel VT-x or AMD-V also has to be enabled in the PC firmware. Virtual machines can be defined here, but they will not start until /dev/kvm exists."
	}
	if !cpuHasFlag(cpuinfo, "vmx") && !cpuHasFlag(cpuinfo, "svm") {
		return "KVM is not available: /dev/kvm does not exist, and this CPU does not advertise virtualisation (vmx or svm). Enable Intel VT-x or AMD-V in the firmware. If this machine is itself a virtual machine, enable nested virtualisation on its host. VMs can be defined, but they will not start."
	}
	return "KVM is not available: /dev/kvm does not exist. The CPU supports virtualisation, but the kvm module is not loaded or this process cannot open the device. On Debian, install qemu-kvm and load kvm_intel or kvm_amd. Virtual machines can be defined, but they will not start until /dev/kvm exists."
}

func cpuHasFlag(cpuinfo, flag string) bool {
	for _, line := range strings.Split(cpuinfo, "\n") {
		if !strings.HasPrefix(line, "flags") && !strings.HasPrefix(line, "Features") {
			continue
		}
		for _, f := range strings.Fields(line) {
			if f == flag {
				return true
			}
		}
	}
	return false
}
