// Package vm manages KVM guests through libvirt.
//
// FELDCORE generates a libvirt domain XML document and applies it with virsh.
// That keeps the daemon free of cgo (the libvirt C bindings need libvirt-dev
// and tie the binary to one library version) and free of libvirt's RPC
// schema. virsh is the same tool an operator uses to inspect a guest, which
// matches how FELDCORE drives the rest of the host. The XML is the source of
// truth, so "edit XML" and the form are one pipeline, and the tests run
// without libvirt installed.
package vm

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// InputError is a validation failure whose message is safe to show.
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

// OpError is an operational failure whose message is safe to show.
type OpError struct{ Msg string }

func (e *OpError) Error() string { return e.Msg }

func invalid(msg string, args ...any) error {
	if len(args) > 0 {
		msg = fmt.Sprintf(msg, args...)
	}
	return &InputError{Msg: msg}
}

var (
	nameRe   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]{0,63}$`)
	fileRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._ +()\-]{0,180}$`)
	bridgeRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._:-]{0,14}$`)
	pciRe    = regexp.MustCompile(`(?i)^[0-9a-f]{4}:[0-9a-f]{2}:[0-9a-f]{2}\.[0-7]$`)
	hex4Re   = regexp.MustCompile(`(?i)^[0-9a-f]{4}$`)
	userRe   = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	snapRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,40}$`)
	shareRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,31}$`)
	sshKeyRe = regexp.MustCompile(`^(ssh-ed25519|ssh-rsa|ssh-dss|ecdsa-sha2-nistp256|ecdsa-sha2-nistp384|ecdsa-sha2-nistp521|sk-ssh-ed25519@openssh\.com|sk-ecdsa-sha2-nistp256@openssh\.com) [A-Za-z0-9+/=]+( .*)?$`)
)

// Disk is one virtual disk on the domains share.
type Disk struct {
	Path   string `json:"path,omitempty"`
	SizeGB int    `json:"sizeGB"`
	Format string `json:"format"`
	Bus    string `json:"bus"`
}

// USBDevice is a host USB device to pass through, identified by its ids.
type USBDevice struct {
	Vendor  string `json:"vendor"`
	Product string `json:"product"`
	Name    string `json:"name,omitempty"`
}

// CloudInit is an optional NoCloud seed attached to the VM.
// It is for a cloud image the operator already has. FELDCORE does not
// download operating-system images.
type CloudInit struct {
	Enabled   bool   `json:"enabled"`
	User      string `json:"user,omitempty"`
	Password  string `json:"password,omitempty"`
	SSHKey    string `json:"sshKey,omitempty"`
	BaseImage string `json:"baseImage,omitempty"`
}

// Spec is the VM form. CPU entries are both the vCPU count and the pin set:
// selecting core 2 gives the guest a vCPU pinned to host core 2.
type Spec struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	OS          string      `json:"os,omitempty"`
	CPUs        []int       `json:"cpus"`
	MemoryMB    int         `json:"memoryMB"`
	MaxMemoryMB int         `json:"maxMemoryMB"`
	Machine     string      `json:"machine"`
	BIOS        string      `json:"bios"`
	TPM         bool        `json:"tpm"`
	Disks       []Disk      `json:"disks"`
	InstallISO  string      `json:"installISO,omitempty"`
	VirtioISO   string      `json:"virtioISO,omitempty"`
	BootISO     bool        `json:"bootISO"`
	Graphics    string      `json:"graphics"`
	Video       string      `json:"video,omitempty"`
	GPU         string      `json:"gpu,omitempty"`
	Sound       string      `json:"sound,omitempty"`
	NIC         string      `json:"nic,omitempty"`
	Network     string      `json:"network"`
	Bridge      string      `json:"bridge,omitempty"`
	USBs        []USBDevice `json:"usbs,omitempty"`
	PCIs        []string    `json:"pcis,omitempty"`
	Shares      []string    `json:"shares,omitempty"`
	Clock       string      `json:"clock,omitempty"`
	HyperV      bool        `json:"hyperv"`
	Autostart   bool        `json:"autostart"`
	CloudInit   CloudInit   `json:"cloudInit"`
}

// normalized returns a copy with defaults filled in, or a validation error.
func (s Spec) normalized() (Spec, error) {
	s.Name = strings.TrimSpace(s.Name)
	if !nameRe.MatchString(s.Name) {
		return s, invalid("Name the VM with letters, numbers, dots, underscores or hyphens, starting with a letter (up to 64 characters).")
	}
	s.Description = strings.TrimSpace(s.Description)
	if len(s.Description) > 2000 || strings.ContainsRune(s.Description, 0) {
		return s, invalid("The description is too long.")
	}
	switch s.OS {
	case "", "linux", "windows", "windows-legacy", "other":
		if s.OS == "" {
			s.OS = "other"
		}
	default:
		return s, invalid("Operating system must be Linux, Windows, legacy Windows, or other.")
	}
	if len(s.CPUs) == 0 {
		return s, invalid("Choose at least one CPU. Each selected core is a vCPU and is pinned to that core.")
	}
	if len(s.CPUs) > 128 {
		return s, invalid("A VM can use at most 128 CPU cores.")
	}
	seenCPU := map[int]bool{}
	for _, c := range s.CPUs {
		if c < 0 || c > 1023 || seenCPU[c] {
			return s, invalid("CPU core %d is not valid.", c)
		}
		seenCPU[c] = true
	}
	sort.Ints(s.CPUs)

	if s.MemoryMB == 0 {
		s.MemoryMB = 2048
	}
	if s.MemoryMB < 128 || s.MemoryMB > 1024*1024 {
		return s, invalid("Memory must be between 128 MB and 1 TB.")
	}
	if s.MaxMemoryMB == 0 || s.MaxMemoryMB < s.MemoryMB {
		s.MaxMemoryMB = s.MemoryMB
	}
	if s.MaxMemoryMB > 1024*1024 {
		return s, invalid("Maximum memory must be 1 TB or less.")
	}

	switch s.Machine {
	case "", "q35":
		s.Machine = "q35"
	case "i440fx":
	default:
		return s, invalid("Machine type must be Q35 or i440fx.")
	}
	switch s.BIOS {
	case "", "ovmf":
		s.BIOS = "ovmf"
	case "seabios":
	default:
		return s, invalid("Firmware must be OVMF (UEFI) or SeaBIOS.")
	}
	if s.TPM && s.BIOS != "ovmf" {
		return s, invalid("A TPM requires OVMF (UEFI). SeaBIOS cannot use the software TPM.")
	}

	switch s.Graphics {
	case "", "vnc":
		s.Graphics = "vnc"
	case "gpu":
		if !pciRe.MatchString(s.GPU) {
			return s, invalid("Choose the GPU to pass through, or use VNC.")
		}
		s.GPU = strings.ToLower(s.GPU)
	default:
		return s, invalid("Graphics must be VNC or a passed-through GPU.")
	}
	switch s.Video {
	case "", "qxl":
		s.Video = "qxl"
	case "virtio", "cirrus", "vga", "bochs", "none":
	default:
		return s, invalid("Video must be qxl, virtio, cirrus, vga, bochs or none.")
	}
	switch s.Sound {
	case "", "ich9":
		s.Sound = "ich9"
	case "none", "ich6", "ac97", "sb16":
	default:
		return s, invalid("Sound must be none, ich9, ich6, ac97 or sb16.")
	}
	switch s.NIC {
	case "", "virtio":
		s.NIC = "virtio"
	case "e1000", "e1000e", "rtl8139":
	default:
		return s, invalid("The network card must be virtio, e1000 or rtl8139.")
	}
	switch s.Network {
	case "", "nat":
		s.Network = "nat"
		s.Bridge = ""
	case "bridge":
		if !bridgeRe.MatchString(s.Bridge) {
			return s, invalid("Choose a bridge such as br0, or use NAT.")
		}
	default:
		return s, invalid("Networking must be NAT or a bridge.")
	}
	switch s.Clock {
	case "", "utc":
		s.Clock = "utc"
	case "localtime":
	default:
		return s, invalid("The clock must be UTC or local time.")
	}

	if s.InstallISO != "" {
		if err := checkFileName(s.InstallISO); err != nil {
			return s, err
		}
	}
	if s.VirtioISO != "" {
		if err := checkFileName(s.VirtioISO); err != nil {
			return s, err
		}
	}
	if s.CloudInit.BaseImage != "" {
		if err := checkFileName(s.CloudInit.BaseImage); err != nil {
			return s, invalid("The cloud image file name is not valid.")
		}
	}
	if len(s.Disks) == 0 && s.InstallISO == "" && s.CloudInit.BaseImage == "" {
		return s, invalid("Add a virtual disk or choose an ISO to boot.")
	}
	if len(s.Disks) > 8 {
		return s, invalid("A VM can have at most 8 virtual disks.")
	}
	buses := map[string]int{}
	for i, d := range s.Disks {
		switch d.Format {
		case "", "qcow2":
			d.Format = "qcow2"
		case "raw":
		default:
			return s, invalid("Disk %d must be qcow2 or raw.", i+1)
		}
		switch d.Bus {
		case "", "virtio":
			d.Bus = "virtio"
		case "sata", "scsi", "ide":
		default:
			return s, invalid("Disk %d bus must be virtio, sata, scsi or ide.", i+1)
		}
		if d.SizeGB < 1 || d.SizeGB > 65536 {
			return s, invalid("Disk %d must be between 1 GB and 64 TB.", i+1)
		}
		if strings.ContainsRune(d.Path, 0) {
			return s, invalid("Disk %d has a bad path.", i+1)
		}
		buses[d.Bus]++
		s.Disks[i] = d
	}
	if buses["sata"] > 0 && buses["scsi"] > 0 {
		return s, invalid("Use SATA or SCSI, not both. They share the same device names.")
	}
	if buses["ide"] > 4 {
		return s, invalid("IDE only has four devices, including CD-ROMs.")
	}

	if len(s.USBs) > 16 || len(s.PCIs) > 16 {
		return s, invalid("Too many devices to pass through.")
	}
	for i, u := range s.USBs {
		if !hex4Re.MatchString(u.Vendor) || !hex4Re.MatchString(u.Product) {
			return s, invalid("A USB device needs a four-digit vendor and product id.")
		}
		s.USBs[i].Vendor = strings.ToLower(u.Vendor)
		s.USBs[i].Product = strings.ToLower(u.Product)
	}
	seenPCI := map[string]bool{}
	for i, p := range s.PCIs {
		if !pciRe.MatchString(p) {
			return s, invalid("PCI address %q is not valid.", p)
		}
		p = strings.ToLower(p)
		if seenPCI[p] {
			return s, invalid("PCI device %s is listed twice.", p)
		}
		seenPCI[p] = true
		s.PCIs[i] = p
	}
	if s.Graphics == "gpu" && seenPCI[s.GPU] {
		return s, invalid("The display GPU is already in the PCI list. Remove the duplicate.")
	}
	if len(s.Shares) > 8 {
		return s, invalid("At most 8 host folders can be shared into a VM.")
	}
	seenShare := map[string]bool{}
	for _, sh := range s.Shares {
		if !shareRe.MatchString(sh) || seenShare[strings.ToLower(sh)] {
			return s, invalid("Share %q cannot be mounted into the VM.", sh)
		}
		seenShare[strings.ToLower(sh)] = true
	}

	if s.CloudInit.Enabled {
		s.CloudInit.User = strings.TrimSpace(s.CloudInit.User)
		if !userRe.MatchString(s.CloudInit.User) {
			return s, invalid("The cloud-init user name must be a short lowercase Linux name.")
		}
		s.CloudInit.SSHKey = strings.TrimSpace(s.CloudInit.SSHKey)
		if s.CloudInit.SSHKey != "" && !sshKeyRe.MatchString(s.CloudInit.SSHKey) {
			return s, invalid("The SSH public key does not look like an OpenSSH key.")
		}
		if len(s.CloudInit.Password) > 200 || strings.ContainsAny(s.CloudInit.Password, "\r\n") {
			return s, invalid("The cloud-init password cannot contain a line break.")
		}
	} else {
		s.CloudInit.User = ""
		s.CloudInit.Password = ""
		s.CloudInit.SSHKey = ""
	}
	return s, nil
}

func checkFileName(name string) error {
	if name != filepath.Base(name) || strings.Contains(name, "..") || !fileRe.MatchString(name) {
		return invalid("Choose a file in the ISO folder by its name, not a path.")
	}
	return nil
}

func checkName(name string) error {
	if !nameRe.MatchString(name) {
		return invalid("That VM name is not valid.")
	}
	return nil
}
