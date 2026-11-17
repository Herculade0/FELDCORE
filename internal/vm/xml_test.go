package vm

import (
	"strings"
	"testing"
)

func fw() Firmware {
	return Firmware{
		Code:     "/usr/share/OVMF/OVMF_CODE_4M.fd",
		Vars:     "/usr/share/OVMF/OVMF_VARS_4M.fd",
		VirtioFS: "/usr/libexec/virtiofsd",
		Shares:   map[string]string{"appdata": "/srv/feldcore/appdata"},
	}
}

func mustSpec(t *testing.T, s Spec) Spec {
	t.Helper()
	out, err := s.normalized()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestDomainXMLProfiles(t *testing.T) {
	base := fw()
	q35 := mustSpec(t, Spec{
		Name: "win11", Description: "a<b>&\"c", OS: "windows",
		CPUs: []int{2, 0}, MemoryMB: 4096, MaxMemoryMB: 8192,
		Machine: "q35", BIOS: "ovmf", TPM: true, HyperV: true, Clock: "localtime",
		Disks:      []Disk{{Path: "/srv/feldcore/domains/win11/vdisk1.qcow2", SizeGB: 64, Format: "qcow2", Bus: "virtio"}},
		InstallISO: "Win11.iso", VirtioISO: "virtio-win-0.1.302.iso", BootISO: true,
		Graphics: "vnc", Video: "qxl", Sound: "ich9", NIC: "virtio", Network: "nat",
		USBs: []USBDevice{{Vendor: "046d", Product: "c52b"}},
	})
	base.InstallISO = "/srv/feldcore/isos/Win11.iso"
	base.VirtioISO = "/srv/feldcore/isos/virtio-win-0.1.302.iso"
	doc, err := BuildDomainXML(q35, base)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`machine='q35'`, `type='pflash'`, `model='tpm-crb'`, `bus='virtio'`,
		`listen='127.0.0.1'`, `cpuset='0'`, `cpuset='2'`, `network='default'`,
		`model='ich9'`, `vendor id='0x046d'`, `offset='localtime'`, `hypervclock`,
		`a&lt;b&gt;&amp;&#34;c`, `Win11.iso`, `virtio-win-0.1.302.iso`,
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("q35 xml missing %s\n%s", want, doc)
		}
	}
	if strings.Contains(doc, "<!DOCTYPE") || strings.Contains(doc, "a<b>") {
		t.Fatal("description was not escaped")
	}
	// Pins are sorted, so vcpu 0 is host cpu 0 and vcpu 1 is host cpu 2.
	if !strings.Contains(doc, `<vcpupin vcpu='0' cpuset='0'/>`) || !strings.Contains(doc, `<vcpupin vcpu='1' cpuset='2'/>`) {
		t.Fatalf("pins:\n%s", doc)
	}

	old := mustSpec(t, Spec{
		Name: "win98", OS: "windows-legacy", CPUs: []int{1}, MemoryMB: 256,
		Machine: "i440fx", BIOS: "seabios", Clock: "localtime",
		Disks:    []Disk{{Path: "/srv/feldcore/domains/win98/vdisk1.qcow2", SizeGB: 2, Format: "qcow2", Bus: "ide"}},
		Graphics: "vnc", Video: "cirrus", Sound: "sb16", NIC: "rtl8139", Network: "nat",
	})
	doc, err = BuildDomainXML(old, fw())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`machine='pc'`, `model='sb16'`, `bus='ide'`, `type='cirrus'`, `type='rtl8139'`, `model='piix3-uhci'`} {
		if !strings.Contains(doc, want) {
			t.Fatalf("legacy xml missing %s\n%s", want, doc)
		}
	}
	for _, absent := range []string{`pflash`, `tpm-`, `memballoon`, `virtio-serial`} {
		if strings.Contains(doc, absent) {
			t.Fatalf("legacy xml has %s\n%s", absent, doc)
		}
	}

	share := mustSpec(t, Spec{
		Name: "files", OS: "linux", CPUs: []int{0}, MemoryMB: 2048,
		Machine: "q35", BIOS: "seabios",
		Disks:    []Disk{{Path: "/srv/feldcore/domains/files/vdisk1.qcow2", SizeGB: 10, Format: "raw", Bus: "virtio"}},
		Graphics: "vnc", Network: "bridge", Bridge: "br0", NIC: "virtio",
		Shares: []string{"appdata"},
	})
	doc, err = BuildDomainXML(share, fw())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`bridge='br0'`, `virtiofs`, `/srv/feldcore/appdata`, `type='memfd'`, `memAccess='shared'`, `type='raw'`} {
		if !strings.Contains(doc, want) {
			t.Fatalf("share xml missing %s\n%s", want, doc)
		}
	}

	gpu := mustSpec(t, Spec{
		Name: "gpu", CPUs: []int{0, 1}, MemoryMB: 8192,
		Machine: "q35", BIOS: "ovmf",
		Disks:    []Disk{{Path: "/srv/feldcore/domains/gpu/vdisk1.qcow2", SizeGB: 40, Format: "qcow2", Bus: "virtio"}},
		Graphics: "gpu", GPU: "0000:01:00.0",
		PCIs:    []string{"0000:01:00.1"},
		Network: "nat",
	})
	doc, err = BuildDomainXML(gpu, fw())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(doc, `type='vnc'`) {
		t.Fatal("gpu vm still has vnc")
	}
	if !strings.Contains(doc, `bus='0x01'`) || !strings.Contains(doc, `function='0x0'`) || !strings.Contains(doc, `function='0x1'`) {
		t.Fatalf("passthrough:\n%s", doc)
	}
}

func TestDomainRoundTrip(t *testing.T) {
	spec := mustSpec(t, Spec{
		Name: "box", Description: "hello", OS: "linux", CPUs: []int{3, 1},
		MemoryMB: 2048, MaxMemoryMB: 4096, Machine: "q35", BIOS: "ovmf", TPM: true,
		Disks:      []Disk{{Path: "/srv/feldcore/domains/box/vdisk1.qcow2", SizeGB: 20, Format: "qcow2", Bus: "virtio"}},
		InstallISO: "debian.iso", BootISO: true, Graphics: "vnc", Video: "virtio",
		Sound: "ich9", NIC: "virtio", Network: "bridge", Bridge: "br0",
		Shares: []string{"appdata"}, Clock: "utc",
		CloudInit: CloudInit{Enabled: true, User: "core", SSHKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFakeKeyForTheRoundTripTest"},
	})
	f := fw()
	f.InstallISO = "/isos/debian.iso"
	f.CloudISO = "/srv/feldcore/domains/box/cidata.iso"
	doc, err := BuildDomainXML(spec, f)
	if err != nil {
		t.Fatal(err)
	}
	back, warnings := ParseDomain(doc, []string{"appdata"})
	if len(warnings) > 0 {
		t.Fatal(warnings)
	}
	if back.Name != "box" || back.Description != "hello" || back.OS != "linux" {
		t.Fatalf("identity: %+v", back)
	}
	if len(back.CPUs) != 2 || back.CPUs[0] != 1 || back.CPUs[1] != 3 {
		t.Fatalf("cpus: %v", back.CPUs)
	}
	if back.MemoryMB != 2048 || back.MaxMemoryMB != 4096 {
		t.Fatalf("memory %d/%d", back.MemoryMB, back.MaxMemoryMB)
	}
	if back.Machine != "q35" || back.BIOS != "ovmf" || !back.TPM || !back.BootISO {
		t.Fatalf("firmware %+v", back)
	}
	if back.Network != "bridge" || back.Bridge != "br0" || back.NIC != "virtio" {
		t.Fatalf("net %+v %s", back.Network, back.Bridge)
	}
	if back.InstallISO != "debian.iso" || len(back.Disks) != 1 || back.Disks[0].Bus != "virtio" {
		t.Fatalf("disks %+v iso %s", back.Disks, back.InstallISO)
	}
	if len(back.Shares) != 1 || back.Shares[0] != "appdata" {
		t.Fatalf("shares %v", back.Shares)
	}
	if !back.CloudInit.Enabled || back.CloudInit.User != "core" || !strings.Contains(back.CloudInit.SSHKey, "ssh-ed25519") {
		t.Fatalf("cloud %+v", back.CloudInit)
	}
	if back.Video != "virtio" || back.Sound != "ich9" || back.Graphics != "vnc" {
		t.Fatalf("display %s %s %s", back.Graphics, back.Video, back.Sound)
	}
}

func TestRawXMLGuard(t *testing.T) {
	good := mustSpec(t, Spec{
		Name: "box", CPUs: []int{0}, MemoryMB: 512, Machine: "i440fx", BIOS: "seabios",
		Disks: []Disk{{Path: "/srv/feldcore/domains/box/vdisk1.raw", SizeGB: 4, Format: "raw", Bus: "virtio"}},
	})
	doc, err := BuildDomainXML(good, Firmware{})
	if err != nil {
		t.Fatal(err)
	}
	const diskDir = "/srv/feldcore/domains/box"
	if err := vetXML("box", doc, diskDir, true); err != nil {
		t.Fatal(err)
	}
	if err := vetXML("other", doc, diskDir, true); err == nil {
		t.Fatal("renamed domain accepted")
	}
	pub := strings.Replace(doc, "127.0.0.1", "0.0.0.0", -1)
	if err := vetXML("box", pub, diskDir, true); err == nil {
		t.Fatal("public vnc accepted")
	}
	if err := vetXML("box", `<!DOCTYPE foo [<!ENTITY x "y">]>`+doc, diskDir, true); err == nil {
		t.Fatal("entity accepted")
	}
	outside := strings.Replace(doc, "/srv/feldcore/domains/box/vdisk1.raw", "/var/lib/feldcore/secrets/master.key", 1)
	if err := vetXML("box", outside, diskDir, true); err == nil {
		t.Fatal("disk outside the VM folder accepted")
	}
	fs := strings.Replace(doc, "</devices>", "<filesystem type='mount'><source dir='/'/></filesystem></devices>", 1)
	if err := vetXML("box", fs, diskDir, true); err == nil {
		t.Fatal("filesystem root accepted")
	}
	cmd := strings.Replace(doc, "</domain>", "<qemu:commandline><qemu:arg value='-sandbox'/></qemu:commandline></domain>", 1)
	if err := vetXML("box", cmd, diskDir, true); err == nil {
		t.Fatal("qemu command line accepted")
	}
	hostdev := strings.Replace(doc, "</devices>", "<hostdev mode='subsystem' type='pci'><source><address domain='0x0000' bus='0x01' slot='0x00' function='0x0'/></source></hostdev></devices>", 1)
	if err := vetXML("box", hostdev, diskDir, false); err == nil {
		t.Fatal("hostdev accepted for a non-admin")
	}
	if err := vetXML("box", hostdev, diskDir, true); err != nil {
		t.Fatal(err)
	}
}

func TestSpecValidation(t *testing.T) {
	if _, err := (Spec{Name: "ok", InstallISO: "../etc/passwd"}).normalized(); err == nil {
		t.Fatal("iso traversal accepted")
	}
	if _, err := (Spec{Name: "Bad name", CPUs: []int{0}, Disks: []Disk{{SizeGB: 10}}}).normalized(); err == nil {
		t.Fatal("space in name accepted")
	}
	if _, err := (Spec{Name: "ok", CPUs: []int{0}, TPM: true, BIOS: "seabios", Disks: []Disk{{SizeGB: 10}}}).normalized(); err == nil {
		t.Fatal("tpm without uefi accepted")
	}
	s, err := (Spec{Name: "ok", CPUs: []int{2, 2}}).normalized()
	if err == nil {
		t.Fatalf("duplicate cpu accepted: %+v", s)
	}
}
