package osimage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImageKeepsSlotLayoutAndLocksSSH(t *testing.T) {
	root := repoRoot(t)
	build, err := os.ReadFile(filepath.Join(root, "os", "build.sh"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(build)
	for _, needle := range []string{
		`esp_mib=256`,
		`state_mib=${FELDCORE_STATE_MIB:-8192}`,
		`slot_slack=128`,
		`-n "1:1M:+${esp_mib}M" -t 1:EF00 -c 1:FC-ESP`,
		`-n "2:0:+${slot_mib}M" -t 2:8300 -c 2:feldcore-a`,
		`-n "3:0:+${slot_mib}M" -t 3:8300 -c 3:feldcore-b`,
		`-n "4:0:+${state_mib}M" -t 4:8300 -c 4:feldcore-state`,
		`sudo truncate -s 0 "${rootfs}/etc/machine-id"`,
		`sudo ln -sfn /etc/machine-id "${rootfs}/var/lib/dbus/machine-id"`,
		`multi-user.target.wants/ssh.service`,
	} {
		if !strings.Contains(text, needle) {
			t.Errorf("os/build.sh is missing %s", needle)
		}
	}
	appliance := assembleBody(t, text, "assemble_appliance")
	if strings.Contains(appliance, "feldcore-data") || strings.Contains(appliance, `-n "5:`) {
		t.Fatal("assemble_appliance grew a data partition")
	}

	id, err := os.ReadFile(filepath.Join(root, "os", "image", "etc", "machine-id"))
	if err != nil {
		t.Fatal(err)
	}
	if len(id) != 0 {
		t.Fatalf("/etc/machine-id in the image tree is %q, want empty", id)
	}

	drop, err := os.ReadFile(filepath.Join(root, "os", "image", "etc", "ssh", "sshd_config.d", "00-feldcore.conf"))
	if err != nil {
		t.Fatal(err)
	}
	conf := string(drop)
	for _, needle := range []string{
		"PermitRootLogin prohibit-password",
		"PasswordAuthentication no",
		"KbdInteractiveAuthentication no",
		"PubkeyAuthentication yes",
	} {
		if !strings.Contains(conf, needle) {
			t.Errorf("sshd drop-in missing %s", needle)
		}
	}
	if strings.Contains(conf, "PermitRootLogin yes") || strings.Contains(conf, "PasswordAuthentication yes") {
		t.Fatalf("sshd drop-in allows password login:\n%s", conf)
	}

	preset, err := os.ReadFile(filepath.Join(root, "os", "image", "etc", "systemd", "system-preset", "00-feldcore.preset"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(preset), "disable ssh.service") || !strings.Contains(string(preset), "disable feldcore-wifi.service") {
		t.Fatalf("preset does not keep ssh and wifi off:\n%s", preset)
	}
	wifi, err := os.ReadFile(filepath.Join(root, "os", "image", "usr", "lib", "systemd", "system", "feldcore-wifi.service"))
	if err != nil {
		t.Fatal(err)
	}
	loopAt := strings.Index(text, "for unit in")
	afterAt := strings.Index(text, "sudo ln -sfn /usr/lib/systemd/system/systemd-networkd.service")
	if loopAt < 0 || afterAt < loopAt {
		t.Fatal("could not find the unit enable loop")
	}
	enableLoop := text[loopAt:afterAt]
	if strings.Contains(enableLoop, "ssh.service") || strings.Contains(enableLoop, "feldcore-wifi.service") {
		t.Fatalf("enable loop starts ssh or wifi:\n%s", enableLoop)
	}
	if !strings.Contains(string(wifi), "ConditionPathExists=/etc/wpa_supplicant/feldcore.conf") {
		t.Fatal("wifi unit does not wait for a config")
	}
	info, err := os.Stat(filepath.Join(root, "os", "image", "usr", "lib", "feldcore", "wifi-up"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatal("wifi-up is not executable")
	}
}

func assembleBody(t *testing.T, script, name string) string {
	t.Helper()
	start := strings.Index(script, name+"() {")
	if start < 0 {
		t.Fatalf("missing function %s", name)
	}
	rest := script[start:]
	end := strings.Index(rest[len(name)+4:], "\n}\n")
	if end < 0 {
		t.Fatalf("function %s does not close", name)
	}
	return rest[:len(name)+4+end]
}
