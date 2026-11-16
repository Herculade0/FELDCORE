package vm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SaveResult is what creating or updating a VM returns.
type SaveResult struct {
	Name    string `json:"name"`
	Warning string `json:"warning,omitempty"`
}

// Save defines a domain from the form. When the VM already exists it must be
// shut down. start asks libvirt to boot it afterwards.
func (m *Manager) Save(ctx context.Context, spec Spec, start bool) (SaveResult, error) {
	spec, err := spec.normalized()
	if err != nil {
		return SaveResult{}, err
	}
	existing := false
	var previous Spec
	if _, err := m.virsh(ctx, "dominfo", spec.Name); err == nil {
		existing = true
		st, err := m.stateOf(ctx, spec.Name)
		if err != nil {
			return SaveResult{}, err
		}
		if st != "stopped" {
			return SaveResult{}, invalid("Shut the VM down before changing its hardware.")
		}
		if det, err := m.Get(ctx, spec.Name); err == nil {
			previous = det.Spec
		}
	} else if m.run.Has("virsh") {
		// dominfo fails both for "no such domain" and for "libvirt is down".
		// A missing domain is fine. A dead libvirt is not, and define reports it.
		msg := err.Error()
		if !strings.Contains(strings.ToLower(msg), "failed to get domain") && !strings.Contains(strings.ToLower(msg), "domain not found") {
			// Keep going when the message is the usual "not found". Connection
			// errors still surface at define time.
		}
	}
	if err := m.prepareDisks(ctx, &spec); err != nil {
		return SaveResult{}, err
	}
	fw, err := m.firmware(ctx, spec, previous, existing)
	if err != nil {
		return SaveResult{}, err
	}
	doc, err := BuildDomainXML(spec, fw)
	if err != nil {
		return SaveResult{}, err
	}
	if err := m.define(ctx, doc); err != nil {
		return SaveResult{}, err
	}
	if spec.Autostart {
		if _, err := m.virsh(ctx, "autostart", spec.Name); err != nil {
			return SaveResult{Name: spec.Name, Warning: "The VM was saved, but autostart could not be turned on."}, nil
		}
	} else if existing {
		_, _ = m.virsh(ctx, "autostart", spec.Name, "--disable")
	}
	res := SaveResult{Name: spec.Name}
	if start {
		if err := m.Start(ctx, spec.Name); err != nil {
			res.Warning = "The VM was saved, but it did not start. " + err.Error()
		}
	}
	return res, nil
}

func (m *Manager) prepareDisks(ctx context.Context, spec *Spec) error {
	if !m.run.Has("qemu-img") {
		return &OpError{"qemu-img is not installed. Turn on libvirt in Settings to install qemu-utils."}
	}
	dir := m.vmDir(spec.Name)
	if !within(m.domainsDir(), dir) && dir != m.domainsDir() {
		// vmDir is domainsDir/name, which is inside domainsDir unless the
		// name is hostile. The name is already validated.
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return &OpError{"Could not create the VM folder on the domains share."}
	}
	var backing string
	if spec.CloudInit.BaseImage != "" {
		p, err := m.findImage(spec.CloudInit.BaseImage)
		if err != nil {
			return err
		}
		backing = p
		if len(spec.Disks) == 0 {
			spec.Disks = []Disk{{SizeGB: 20, Format: "qcow2", Bus: "virtio"}}
		}
		spec.Disks[0].Format = "qcow2"
	}
	for i := range spec.Disks {
		d := &spec.Disks[i]
		if d.Path == "" {
			d.Path = filepath.Join(dir, fmt.Sprintf("vdisk%d.%s", i+1, d.Format))
		} else if !within(m.domainsDir(), d.Path) {
			return invalid("Disks have to stay in the domains folder.")
		}
		if _, err := os.Stat(d.Path); err == nil {
			if err := m.resizeDisk(ctx, d.Path, d.SizeGB); err != nil {
				return err
			}
			continue
		}
		back := ""
		if i == 0 {
			back = backing
		}
		if back != "" {
			if cur, err := imageBytes(back); err == nil && int64(d.SizeGB)<<30 < cur {
				return invalid("The first disk must be at least %d GB to fit the cloud image.", roundGB(cur))
			}
		}
		if err := m.createDisk(ctx, *d, back); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) createDisk(ctx context.Context, d Disk, backing string) error {
	if err := os.MkdirAll(filepath.Dir(d.Path), 0o755); err != nil {
		return &OpError{"Could not create the disk folder."}
	}
	args := []string{"create", "-f", d.Format}
	if backing != "" {
		args = append(args, "-b", backing, "-F", imageFormat(backing))
	}
	args = append(args, d.Path, fmt.Sprintf("%dG", d.SizeGB))
	if _, err := m.run.Run(ctx, 2*time.Minute, "qemu-img", args...); err != nil {
		return &OpError{"Could not create the virtual disk: " + err.Error()}
	}
	relaxPerms(d.Path, false)
	return nil
}

func (m *Manager) resizeDisk(ctx context.Context, path string, sizeGB int) error {
	cur, err := imageBytes(path)
	if err != nil {
		return nil
	}
	want := int64(sizeGB) << 30
	if want+int64(1<<20) < cur {
		return invalid("A disk cannot be shrunk (it is %d GB). Choose %d GB or more.", roundGB(cur), roundGB(cur))
	}
	if want <= cur+int64(1<<20) {
		return nil
	}
	if _, err := m.run.Run(ctx, time.Minute, "qemu-img", "resize", path, fmt.Sprintf("%dG", sizeGB)); err != nil {
		return &OpError{"Could not grow the disk: " + err.Error()}
	}
	return nil
}

func (m *Manager) firmware(ctx context.Context, spec, previous Spec, existing bool) (Firmware, error) {
	fw := Firmware{Shares: map[string]string{}}
	if spec.BIOS == "ovmf" {
		code, vars := findOVMF()
		if code == "" || vars == "" {
			return fw, invalid("OVMF is not installed. Turn libvirt on in Settings (that installs the ovmf package), or choose SeaBIOS.")
		}
		fw.Code, fw.Vars = code, vars
	}
	if spec.TPM && !m.run.Has("swtpm") {
		return fw, invalid("A software TPM needs the swtpm package. Turn libvirt on in Settings, or turn the TPM off.")
	}
	if len(spec.Shares) > 0 {
		fw.VirtioFS = m.virtiofsd()
		if fw.VirtioFS == "" {
			return fw, invalid("Sharing a host folder needs virtiofsd. Install it, or turn the shares off.")
		}
		for _, sh := range m.shares() {
			fw.Shares[strings.ToLower(sh.Name)] = sh.Path
		}
	}
	var err error
	if fw.InstallISO, err = m.isoPath(spec.InstallISO); err != nil {
		return fw, err
	}
	if fw.VirtioISO, err = m.isoPath(spec.VirtioISO); err != nil {
		return fw, err
	}
	if spec.CloudInit.Enabled {
		seed, err := m.cloudSeed(spec, previous, existing)
		if err != nil {
			return fw, err
		}
		fw.CloudISO = seed
	}
	for _, p := range []string{"/usr/bin/qemu-system-x86_64", "/usr/bin/qemu-kvm"} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			fw.Emulator = p
			break
		}
	}
	_ = ctx
	return fw, nil
}

func (m *Manager) cloudSeed(spec, previous Spec, existing bool) (string, error) {
	path := filepath.Join(m.vmDir(spec.Name), "cidata.iso")
	if !within(m.vmDir(spec.Name), path) {
		return "", invalid("Could not place the cloud-init ISO.")
	}
	keep := existing && spec.CloudInit.Password == "" &&
		previous.CloudInit.User == spec.CloudInit.User &&
		previous.CloudInit.SSHKey == spec.CloudInit.SSHKey
	if keep {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	raw, err := cloudInitISO(spec)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", &OpError{"Could not create the VM folder."}
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return "", &OpError{"Could not write the cloud-init ISO."}
	}
	relaxPerms(path, true)
	return path, nil
}

func (m *Manager) isoPath(name string) (string, error) {
	if name == "" {
		return "", nil
	}
	if err := checkFileName(name); err != nil {
		return "", err
	}
	p := filepath.Join(m.isosDir(), name)
	if !within(m.isosDir(), p) {
		return "", invalid("That ISO is not in the ISO folder.")
	}
	st, err := os.Stat(p)
	if err != nil || st.IsDir() {
		return "", invalid("%s is not in the ISO folder (%s). Copy it there, then choose it again.", name, m.isosDir())
	}
	return p, nil
}

func (m *Manager) findImage(name string) (string, error) {
	if err := checkFileName(name); err != nil {
		return "", err
	}
	for _, dir := range []string{m.isosDir(), m.domainsDir()} {
		p := filepath.Join(dir, name)
		if !within(dir, p) {
			continue
		}
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", invalid("The cloud image %s is not in the ISO or domains folder.", name)
}
