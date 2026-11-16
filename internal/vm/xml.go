package vm

import (
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Firmware is host state the domain XML has to name: OVMF files, the
// virtiofsd binary, and paths that were resolved from the form's file names.
type Firmware struct {
	Code       string
	Vars       string
	VirtioFS   string
	Emulator   string
	InstallISO string
	VirtioISO  string
	CloudISO   string
	Shares     map[string]string // lower-case share name -> host path
}

// BuildDomainXML renders a libvirt domain. spec must already be normalized
// and its disk paths must be absolute paths inside the domains folder.
func BuildDomainXML(spec Spec, fw Firmware) (string, error) {
	if spec.BIOS == "ovmf" && (fw.Code == "" || fw.Vars == "") {
		return "", invalid("OVMF is not installed. Turn libvirt on in Settings (that installs the ovmf package), or choose SeaBIOS.")
	}
	if spec.TPM {
		// swtpm is started by libvirt; the package check happens in the manager.
	}
	if len(spec.Shares) > 0 && fw.VirtioFS == "" {
		return "", invalid("Sharing a host folder into the VM needs virtiofsd. Install the virtiofsd package, or turn the shares off.")
	}
	for _, name := range spec.Shares {
		if fw.Shares[strings.ToLower(name)] == "" {
			return "", invalid("The share %s is not on this server.", name)
		}
	}

	machine := "q35"
	if spec.Machine == "i440fx" {
		machine = "pc"
	}
	retro := spec.Machine == "i440fx" && !usesVirtio(spec)
	maxKiB := spec.MaxMemoryMB * 1024
	curKiB := spec.MemoryMB * 1024

	var b strings.Builder
	b.WriteString("<domain type='kvm'>\n")
	fmt.Fprintf(&b, "  <name>%s</name>\n", xa(spec.Name))
	if spec.Description != "" {
		fmt.Fprintf(&b, "  <description>%s</description>\n", xa(spec.Description))
	}
	fmt.Fprintf(&b, "  <memory unit='KiB'>%d</memory>\n", maxKiB)
	fmt.Fprintf(&b, "  <currentMemory unit='KiB'>%d</currentMemory>\n", curKiB)
	fmt.Fprintf(&b, "  <vcpu placement='static'>%d</vcpu>\n", len(spec.CPUs))
	b.WriteString("  <cputune>\n")
	for i, cpu := range spec.CPUs {
		fmt.Fprintf(&b, "    <vcpupin vcpu='%d' cpuset='%d'/>\n", i, cpu)
	}
	b.WriteString("  </cputune>\n")
	writeMeta(&b, spec)
	fmt.Fprintf(&b, "  <os>\n    <type arch='x86_64' machine='%s'>hvm</type>\n", machine)
	if spec.BIOS == "ovmf" {
		fmt.Fprintf(&b, "    <loader readonly='yes' type='pflash'>%s</loader>\n", xa(fw.Code))
		fmt.Fprintf(&b, "    <nvram template='%s'>/var/lib/libvirt/qemu/nvram/%s_VARS.fd</nvram>\n", xa(fw.Vars), xa(spec.Name))
	}
	if spec.BootISO && fw.InstallISO != "" {
		b.WriteString("    <boot dev='cdrom'/>\n    <boot dev='hd'/>\n")
	} else {
		b.WriteString("    <boot dev='hd'/>\n")
		if fw.InstallISO != "" {
			b.WriteString("    <boot dev='cdrom'/>\n")
		}
	}
	b.WriteString("  </os>\n")
	b.WriteString("  <features>\n    <acpi/>\n    <apic/>\n")
	if spec.HyperV {
		b.WriteString("    <hyperv mode='custom'>\n      <relaxed state='on'/>\n      <vapic state='on'/>\n      <spinlocks state='on' retries='8191'/>\n    </hyperv>\n")
	}
	b.WriteString("  </features>\n")
	fmt.Fprintf(&b, "  <cpu mode='host-passthrough' check='none' migratable='on'")
	if len(spec.Shares) > 0 {
		last := len(spec.CPUs) - 1
		cpus := "0"
		if last > 0 {
			cpus = fmt.Sprintf("0-%d", last)
		}
		fmt.Fprintf(&b, ">\n    <numa>\n      <cell id='0' cpus='%s' memory='%d' unit='KiB' memAccess='shared'/>\n    </numa>\n  </cpu>\n", cpus, maxKiB)
	} else {
		b.WriteString("/>\n")
	}
	fmt.Fprintf(&b, "  <clock offset='%s'>\n", spec.Clock)
	b.WriteString("    <timer name='rtc' tickpolicy='catchup'/>\n    <timer name='pit' tickpolicy='delay'/>\n    <timer name='hpet' present='no'/>\n")
	if spec.HyperV {
		b.WriteString("    <timer name='hypervclock' present='yes'/>\n")
	}
	b.WriteString("  </clock>\n")
	b.WriteString("  <on_poweroff>destroy</on_poweroff>\n  <on_reboot>restart</on_reboot>\n  <on_crash>destroy</on_crash>\n")
	if len(spec.Shares) > 0 {
		b.WriteString("  <memoryBacking>\n    <source type='memfd'/>\n    <access mode='shared'/>\n  </memoryBacking>\n")
	}
	b.WriteString("  <devices>\n")
	if fw.Emulator != "" {
		fmt.Fprintf(&b, "    <emulator>%s</emulator>\n", xa(fw.Emulator))
	}
	if err := writeDisks(&b, spec, fw); err != nil {
		return "", err
	}
	writeControllers(&b, spec)
	if spec.Network == "bridge" {
		fmt.Fprintf(&b, "    <interface type='bridge'>\n      <source bridge='%s'/>\n      <model type='%s'/>\n    </interface>\n", xa(spec.Bridge), xa(spec.NIC))
	} else {
		fmt.Fprintf(&b, "    <interface type='network'>\n      <source network='default'/>\n      <model type='%s'/>\n    </interface>\n", xa(spec.NIC))
	}
	b.WriteString("    <input type='keyboard' bus='ps2'/>\n    <input type='mouse' bus='ps2'/>\n")
	if !retro {
		b.WriteString("    <input type='tablet' bus='usb'/>\n")
	}
	if spec.Graphics == "vnc" {
		b.WriteString("    <graphics type='vnc' port='-1' autoport='yes' listen='127.0.0.1'>\n")
		b.WriteString("      <listen type='address' address='127.0.0.1'/>\n    </graphics>\n")
		if spec.Video != "none" && spec.Video != "" {
			fmt.Fprintf(&b, "    <video>\n      %s\n    </video>\n", videoModel(spec.Video))
		}
	}
	if spec.Graphics == "gpu" {
		writePCI(&b, spec.GPU)
	}
	for _, addr := range spec.PCIs {
		writePCI(&b, addr)
	}
	for _, u := range spec.USBs {
		fmt.Fprintf(&b, "    <hostdev mode='subsystem' type='usb' managed='yes'>\n      <source>\n        <vendor id='0x%s'/>\n        <product id='0x%s'/>\n      </source>\n    </hostdev>\n", xa(u.Vendor), xa(u.Product))
	}
	if spec.Sound != "none" && spec.Sound != "" {
		fmt.Fprintf(&b, "    <sound model='%s'/>\n", xa(spec.Sound))
	}
	if spec.TPM {
		model := "tpm-crb"
		if spec.Machine == "i440fx" {
			model = "tpm-tis"
		}
		fmt.Fprintf(&b, "    <tpm model='%s'>\n      <backend type='emulator' version='2.0'/>\n    </tpm>\n", model)
	}
	for _, name := range spec.Shares {
		dir := fw.Shares[strings.ToLower(name)]
		fmt.Fprintf(&b, "    <filesystem type='mount' accessmode='passthrough'>\n      <driver type='virtiofs'/>\n      <binary path='%s'/>\n      <source dir='%s'/>\n      <target dir='%s'/>\n    </filesystem>\n", xa(fw.VirtioFS), xa(dir), xa(name))
	}
	if !retro {
		b.WriteString("    <channel type='unix'>\n      <target type='virtio' name='org.qemu.guest_agent.0'/>\n    </channel>\n")
		b.WriteString("    <memballoon model='virtio'/>\n")
		b.WriteString("    <rng model='virtio'>\n      <backend model='random'>/dev/urandom</backend>\n    </rng>\n")
	}
	b.WriteString("  </devices>\n</domain>\n")
	return b.String(), nil
}

func writeMeta(b *strings.Builder, spec Spec) {
	b.WriteString("  <metadata>\n    <vm xmlns='urn:feldcore:vm'>\n")
	fmt.Fprintf(b, "      <os>%s</os>\n", xa(spec.OS))
	if spec.CloudInit.Enabled {
		fmt.Fprintf(b, "      <cloudUser>%s</cloudUser>\n", xa(spec.CloudInit.User))
		if spec.CloudInit.SSHKey != "" {
			fmt.Fprintf(b, "      <cloudKey>%s</cloudKey>\n", xa(spec.CloudInit.SSHKey))
		}
	}
	b.WriteString("    </vm>\n  </metadata>\n")
}

func writeDisks(b *strings.Builder, spec Spec, fw Firmware) error {
	count := map[string]int{}
	for _, d := range spec.Disks {
		dev := diskTarget(d.Bus, count[d.Bus])
		count[d.Bus]++
		fmt.Fprintf(b, "    <disk type='file' device='disk'>\n      <driver name='qemu' type='%s' cache='writeback' discard='unmap'/>\n      <source file='%s'/>\n      <target dev='%s' bus='%s'/>\n    </disk>\n", xa(d.Format), xa(d.Path), dev, xa(d.Bus))
	}
	cdBus := "sata"
	if spec.Machine == "i440fx" {
		cdBus = "ide"
	}
	if count["scsi"] > 0 && count["sata"] == 0 {
		cdBus = "scsi"
	}
	var cds []string
	if fw.InstallISO != "" {
		cds = append(cds, fw.InstallISO)
	}
	if fw.VirtioISO != "" {
		cds = append(cds, fw.VirtioISO)
	}
	if fw.CloudISO != "" {
		cds = append(cds, fw.CloudISO)
	}
	if cdBus == "ide" && count["ide"]+len(cds) > 4 {
		return invalid("IDE only has four devices. Remove a disk or a CD, or switch the disks to SATA or VirtIO.")
	}
	for _, path := range cds {
		dev := diskTarget(cdBus, count[cdBus])
		count[cdBus]++
		fmt.Fprintf(b, "    <disk type='file' device='cdrom'>\n      <driver name='qemu' type='raw'/>\n      <source file='%s'/>\n      <target dev='%s' bus='%s'/>\n      <readonly/>\n    </disk>\n", xa(path), dev, cdBus)
	}
	return nil
}

func writeControllers(b *strings.Builder, spec Spec) {
	if spec.Machine == "q35" {
		b.WriteString("    <controller type='usb' model='qemu-xhci' ports='15'/>\n")
		b.WriteString("    <controller type='pci' index='0' model='pcie-root'/>\n")
		b.WriteString("    <controller type='sata' index='0'/>\n")
	} else {
		b.WriteString("    <controller type='usb' model='piix3-uhci'/>\n")
		b.WriteString("    <controller type='pci' index='0' model='pci-root'/>\n")
		b.WriteString("    <controller type='ide' index='0'/>\n")
	}
	if spec.Machine == "q35" {
		for _, d := range spec.Disks {
			if d.Bus == "ide" {
				b.WriteString("    <controller type='ide' index='0'/>\n")
				break
			}
		}
	}
	for _, d := range spec.Disks {
		if d.Bus == "scsi" {
			b.WriteString("    <controller type='scsi' index='0' model='virtio-scsi'/>\n")
			break
		}
	}
	if !usesVirtio(spec) {
		return
	}
	b.WriteString("    <controller type='virtio-serial' index='0'/>\n")
}

func writePCI(b *strings.Builder, addr string) {
	parts := strings.Split(addr, ":")
	sf := strings.Split(parts[2], ".")
	fmt.Fprintf(b, "    <hostdev mode='subsystem' type='pci' managed='yes'>\n      <source>\n        <address domain='0x%s' bus='0x%s' slot='0x%s' function='0x%s'/>\n      </source>\n    </hostdev>\n", parts[0], parts[1], sf[0], sf[1])
}

func diskTarget(bus string, n int) string {
	letter := string(rune('a' + n))
	switch bus {
	case "virtio":
		return "vd" + letter
	case "ide":
		return "hd" + letter
	default:
		return "sd" + letter
	}
}

func videoModel(model string) string {
	switch model {
	case "cirrus":
		return "<model type='cirrus' vram='16384' heads='1' primary='yes'/>"
	case "vga", "bochs":
		return fmt.Sprintf("<model type='%s' vram='16384' heads='1' primary='yes'/>", model)
	case "virtio":
		return "<model type='virtio' heads='1' primary='yes'/>"
	default:
		return "<model type='qxl' ram='65536' vram='65536' vgamem='16384' heads='1' primary='yes'/>"
	}
}

func usesVirtio(spec Spec) bool {
	if spec.NIC == "virtio" || spec.Video == "virtio" || len(spec.Shares) > 0 {
		return true
	}
	for _, d := range spec.Disks {
		if d.Bus == "virtio" || d.Bus == "scsi" {
			return true
		}
	}
	return false
}

func xa(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// ParseDomain reads a domain XML document back into the form. Warnings name
// devices the form does not round-trip; saving the form would drop them.
func ParseDomain(doc string, shareNames []string) (Spec, []string) {
	var spec Spec
	var warnings []string
	dec := xml.NewDecoder(strings.NewReader(doc))
	var stack []string
	var text strings.Builder
	type graphics struct {
		vnc    bool
		listen string
	}
	var gfx graphics
	var inGraphics bool
	knownShares := map[string]string{}
	for _, n := range shareNames {
		knownShares[strings.ToLower(n)] = n
	}

	flush := func(local string) {
		val := strings.TrimSpace(text.String())
		text.Reset()
		switch local {
		case "name":
			if spec.Name == "" {
				spec.Name = val
			}
		case "description":
			spec.Description = val
		case "memory":
			// filled from attributes via the element path below
		}
	}

	var memUnit, curUnit string
	var memVal, curVal int64
	bootSeen := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return spec, append(warnings, "The domain XML could not be read completely.")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			stack = append(stack, t.Name.Local)
			text.Reset()
			switch t.Name.Local {
			case "memory":
				memUnit = attr(t, "unit")
			case "currentMemory":
				curUnit = attr(t, "unit")
			case "type":
				if parent(stack) == "os" {
					m := attr(t, "machine")
					if strings.Contains(strings.ToLower(m), "q35") {
						spec.Machine = "q35"
					} else if m != "" {
						spec.Machine = "i440fx"
					}
				}
			case "loader":
				spec.BIOS = "ovmf"
			case "boot":
				if parent(stack) == "os" && !bootSeen {
					bootSeen = true
					spec.BootISO = attr(t, "dev") == "cdrom"
				}
			case "clock":
				if off := attr(t, "offset"); off != "" {
					spec.Clock = off
				}
			case "hyperv":
				spec.HyperV = true
			case "disk":
				warnings = append(warnings, readDisk(dec, t, &spec)...)
				stack = stack[:len(stack)-1]
			case "interface":
				if spec.NIC != "" {
					warnings = append(warnings, "Only the first network interface is kept by the form.")
				} else {
					readInterface(dec, t, &spec)
				}
				stack = stack[:len(stack)-1]
			case "graphics":
				inGraphics = attr(t, "type") == "vnc"
				if inGraphics {
					gfx.vnc = true
					gfx.listen = attr(t, "listen")
					spec.Graphics = "vnc"
				}
			case "listen":
				if inGraphics {
					if a := attr(t, "address"); a != "" {
						gfx.listen = a
					}
				}
			case "model":
				if parent(stack) == "video" && spec.Video == "" {
					spec.Video = attr(t, "type")
				}
				if parent(stack) == "sound" && spec.Sound == "" {
					spec.Sound = attr(t, "type")
				}
			case "sound":
				if m := attr(t, "model"); m != "" {
					spec.Sound = m
				}
			case "tpm":
				spec.TPM = true
			case "hostdev":
				readHostdev(dec, t, &spec)
				stack = stack[:len(stack)-1]
			case "filesystem":
				readFS(dec, t, &spec, knownShares, &warnings)
				stack = stack[:len(stack)-1]
			case "os", "cloudUser", "cloudKey":
				// char data handled on end
			}
		case xml.CharData:
			text.Write(t)
		case xml.EndElement:
			local := t.Name.Local
			val := strings.TrimSpace(text.String())
			switch local {
			case "memory":
				memVal = atoi64(val)
			case "currentMemory":
				curVal = atoi64(val)
			case "vcpu":
				if len(spec.CPUs) == 0 {
					n := int(atoi64(val))
					if n > 0 && n <= 128 {
						spec.CPUs = make([]int, n)
						for i := range spec.CPUs {
							spec.CPUs[i] = i
						}
					}
				}
			case "vcpupin":
				// attributes were on the start element; handled below
			case "os":
				if len(stack) >= 2 && stack[len(stack)-2] == "vm" && val != "" {
					spec.OS = val
				}
			case "cloudUser":
				spec.CloudInit.Enabled = true
				spec.CloudInit.User = val
			case "cloudKey":
				spec.CloudInit.SSHKey = val
				spec.CloudInit.Enabled = true
			case "graphics":
				inGraphics = false
			}
			flush(local)
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	// vcpupin is attribute-only, so walk again for pins. A second pass keeps
	// the decoder above focused on elements that have text.
	spec.CPUs = readPins(doc, spec.CPUs)
	if memVal > 0 {
		spec.MaxMemoryMB = toMB(memVal, memUnit)
	}
	if curVal > 0 {
		spec.MemoryMB = toMB(curVal, curUnit)
	}
	if spec.MemoryMB == 0 {
		spec.MemoryMB = spec.MaxMemoryMB
	}
	if spec.BIOS == "" {
		spec.BIOS = "seabios"
	}
	if spec.Graphics == "" {
		if spec.GPU != "" {
			spec.Graphics = "gpu"
		} else {
			spec.Graphics = "vnc"
		}
	}
	if spec.Sound == "" {
		spec.Sound = "none"
	}
	if spec.Clock == "" {
		spec.Clock = "utc"
	}
	for i, d := range spec.Disks {
		if d.Bus == "" {
			spec.Disks[i].Bus = "virtio"
		}
		if d.Format == "" {
			spec.Disks[i].Format = "qcow2"
		}
	}
	if gfx.vnc && gfx.listen != "" && gfx.listen != "127.0.0.1" && gfx.listen != "localhost" && gfx.listen != "::1" {
		warnings = append(warnings, "VNC is not bound to localhost. Saving from the form binds it to 127.0.0.1.")
	}
	_ = memUnit
	_ = curUnit
	return spec, warnings
}

func readPins(doc string, fallback []int) []int {
	dec := xml.NewDecoder(strings.NewReader(doc))
	var pins []int
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		el, ok := tok.(xml.StartElement)
		if !ok || el.Name.Local != "vcpupin" {
			continue
		}
		set := attr(el, "cpuset")
		n, err := strconv.Atoi(set)
		if err != nil {
			continue
		}
		pins = append(pins, n)
	}
	if len(pins) == 0 {
		return fallback
	}
	return pins
}

func readDisk(dec *xml.Decoder, start xml.StartElement, spec *Spec) []string {
	var file, bus, format, device string
	device = attr(start, "device")
	var warnings []string
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "driver":
				format = attr(t, "type")
			case "source":
				if file == "" {
					file = attr(t, "file")
				}
			case "target":
				bus = attr(t, "bus")
			}
		case xml.EndElement:
			if t.Name.Local == "disk" {
				goto done
			}
		}
	}
done:
	if device == "cdrom" {
		base := file
		if i := strings.LastIndex(base, "/"); i >= 0 {
			base = base[i+1:]
		}
		low := strings.ToLower(base)
		switch {
		case strings.HasSuffix(low, "cidata.iso"):
			spec.CloudInit.Enabled = true
		case strings.HasPrefix(low, "virtio-win"):
			spec.VirtioISO = base
		case spec.InstallISO == "":
			spec.InstallISO = base
		default:
			warnings = append(warnings, "An extra CD ("+base+") is not shown in the form.")
		}
		return warnings
	}
	if device != "" && device != "disk" {
		warnings = append(warnings, "A "+device+" disk is not shown in the form.")
		return warnings
	}
	if format == "" {
		format = "qcow2"
	}
	spec.Disks = append(spec.Disks, Disk{Path: file, Format: format, Bus: bus})
	return warnings
}

func readInterface(dec *xml.Decoder, start xml.StartElement, spec *Spec) {
	kind := attr(start, "type")
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "source":
				if br := attr(t, "bridge"); br != "" {
					spec.Network = "bridge"
					spec.Bridge = br
				} else if attr(t, "network") != "" {
					spec.Network = "nat"
				}
			case "model":
				if m := attr(t, "type"); m != "" {
					spec.NIC = m
				}
			}
		case xml.EndElement:
			if t.Name.Local == "interface" {
				return
			}
		}
	}
	if kind == "bridge" && spec.Network == "" {
		spec.Network = "bridge"
	}
	if spec.Network == "" {
		spec.Network = "nat"
	}
}

func readHostdev(dec *xml.Decoder, start xml.StartElement, spec *Spec) {
	kind := attr(start, "type")
	var vendor, product, addr string
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "vendor":
				vendor = strings.TrimPrefix(strings.ToLower(attr(t, "id")), "0x")
			case "product":
				product = strings.TrimPrefix(strings.ToLower(attr(t, "id")), "0x")
			case "address":
				if attr(t, "bus") != "" && attr(t, "slot") != "" && kind == "pci" {
					dom := attr(t, "domain")
					if dom == "" {
						dom = "0"
					}
					addr = fmt.Sprintf("%s:%s:%s.%s",
						padHex(dom, 4), padHex(attr(t, "bus"), 2),
						padHex(attr(t, "slot"), 2), padHex(attr(t, "function"), 1))
				}
			}
		case xml.EndElement:
			if t.Name.Local == "hostdev" {
				if kind == "usb" && vendor != "" && product != "" {
					spec.USBs = append(spec.USBs, USBDevice{Vendor: vendor, Product: product})
				}
				if kind == "pci" && addr != "" {
					if spec.GPU == "" && spec.Graphics != "vnc" {
						spec.GPU = addr
						spec.Graphics = "gpu"
					} else {
						spec.PCIs = append(spec.PCIs, addr)
					}
				}
				return
			}
		}
	}
}

func readFS(dec *xml.Decoder, start xml.StartElement, spec *Spec, known map[string]string, warnings *[]string) {
	driver := ""
	tag := ""
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "driver":
				driver = attr(t, "type")
			case "target":
				tag = attr(t, "dir")
			}
		case xml.EndElement:
			if t.Name.Local == "filesystem" {
				if driver != "" && driver != "virtiofs" {
					*warnings = append(*warnings, "A "+driver+" share is not shown in the form.")
					return
				}
				if name, ok := known[strings.ToLower(tag)]; ok {
					spec.Shares = append(spec.Shares, name)
				} else if tag != "" {
					*warnings = append(*warnings, "The virtiofs tag "+tag+" is not one of this server's shares, so the form leaves it out.")
				}
				return
			}
		}
	}
}

func parent(stack []string) string {
	if len(stack) < 2 {
		return ""
	}
	return stack[len(stack)-2]
}

func attr(el xml.StartElement, name string) string {
	for _, a := range el.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

func atoi64(s string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n
}

func toMB(v int64, unit string) int {
	switch strings.ToLower(unit) {
	case "b", "bytes":
		return int(v / 1024 / 1024)
	case "kb", "kib":
		return int(v / 1024)
	case "mb", "mib", "":
		if unit == "" {
			return int(v / 1024) // libvirt's default unit is KiB
		}
		return int(v)
	case "gb", "gib":
		return int(v * 1024)
	default:
		return int(v / 1024)
	}
}

func padHex(s string, n int) string {
	s = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "0x")
	if s == "" {
		s = "0"
	}
	for len(s) < n {
		s = "0" + s
	}
	return s
}
