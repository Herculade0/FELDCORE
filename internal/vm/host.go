package vm

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Core is one logical CPU the form can pin a vCPU to.
type Core struct {
	ID      int `json:"id"`
	Core    int `json:"core"`
	Thread  int `json:"thread"`
	Sibling int `json:"sibling"`
}

// PCIDevice is a host PCI function, grouped for passthrough.
type PCIDevice struct {
	Address string `json:"address"`
	Vendor  string `json:"vendor"`
	Device  string `json:"device"`
	Class   string `json:"class"`
	ClassID string `json:"classID"`
	Name    string `json:"name"`
	Group   int    `json:"group"`
}

// IOMMUGroup is one isolation group. Devices in a group have to move together
// when one of them is passed through.
type IOMMUGroup struct {
	ID      int         `json:"id"`
	Devices []PCIDevice `json:"devices"`
}

// USBInfo is a host USB device that can be passed through.
type USBInfo struct {
	Vendor  string `json:"vendor"`
	Product string `json:"product"`
	Name    string `json:"name"`
	Bus     string `json:"bus,omitempty"`
	Device  string `json:"device,omitempty"`
}

// Media is a file in the ISO or domains folder.
type Media struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Virtio bool   `json:"virtio,omitempty"`
}

// ShareInfo is a host share the VM can mount with virtio-fs.
type ShareInfo struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// Host is everything the VM form needs from this machine.
type Host struct {
	KVM          bool         `json:"kvm"`
	KVMMessage   string       `json:"kvmMessage,omitempty"`
	Virsh        bool         `json:"virsh"`
	Libvirt      bool         `json:"libvirt"`
	QemuImg      bool         `json:"qemuImg"`
	OVMF         string       `json:"ovmf,omitempty"`
	OVMFVars     string       `json:"ovmfVars,omitempty"`
	SWTPM        bool         `json:"swtpm"`
	VirtioFS     bool         `json:"virtiofsd"`
	Root         bool         `json:"root"`
	Cores        []Core       `json:"cores"`
	MemoryMB     int          `json:"memoryMB"`
	Bridges      []string     `json:"bridges"`
	IOMMU        []IOMMUGroup `json:"iommu"`
	IOMMUMessage string       `json:"iommuMessage,omitempty"`
	USB          []USBInfo    `json:"usb"`
	ISOs         []Media      `json:"isos"`
	Images       []Media      `json:"images"`
	Shares       []ShareInfo  `json:"shares"`
	DomainsPath  string       `json:"domainsPath"`
	ISOsPath     string       `json:"isosPath"`
	Message      string       `json:"message,omitempty"`
}

func (m *Manager) Host(ctx context.Context) Host {
	ok, msg := m.kvm()
	h := Host{
		KVM: ok, KVMMessage: msg,
		Virsh: m.run.Has("virsh"), QemuImg: m.run.Has("qemu-img"),
		SWTPM: m.run.Has("swtpm"), VirtioFS: m.virtiofsd() != "",
		Root:  os.Geteuid() == 0,
		Cores: m.cores(), MemoryMB: m.memoryMB(),
		Bridges: m.bridges(), USB: m.usbDevices(),
		DomainsPath: m.domainsDir(), ISOsPath: m.isosDir(),
		Shares: m.shareList(),
	}
	h.OVMF, h.OVMFVars = findOVMF()
	h.IOMMU, h.IOMMUMessage = m.iommu()
	h.ISOs = listMedia(h.ISOsPath, true)
	h.Images = listMedia(h.ISOsPath, false)
	for _, extra := range listMedia(h.DomainsPath, false) {
		h.Images = append(h.Images, extra)
	}
	if !h.Virsh {
		h.Message = "virsh is not installed. Turn on libvirt in Settings to install KVM, OVMF and the software TPM."
	} else if !m.libvirtUp(ctx) {
		h.Libvirt = false
		h.Message = "libvirt is not running. Turn it on in Settings. It stays off when this CPU cannot virtualise."
	} else {
		h.Libvirt = true
	}
	if h.Cores == nil {
		h.Cores = []Core{}
	}
	if h.Bridges == nil {
		h.Bridges = []string{}
	}
	if h.IOMMU == nil {
		h.IOMMU = []IOMMUGroup{}
	}
	if h.USB == nil {
		h.USB = []USBInfo{}
	}
	if h.ISOs == nil {
		h.ISOs = []Media{}
	}
	if h.Images == nil {
		h.Images = []Media{}
	}
	if h.Shares == nil {
		h.Shares = []ShareInfo{}
	}
	return h
}

func (m *Manager) sys(p string) string {
	if m.root == "" {
		return p
	}
	return filepath.Join(m.root, strings.TrimPrefix(p, "/"))
}

func (m *Manager) cores() []Core {
	dir := m.sys("/sys/devices/system/cpu")
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		n := runtime.NumCPU()
		out := make([]Core, n)
		for i := range out {
			out[i] = Core{ID: i, Core: i, Sibling: -1}
		}
		return out
	}
	var ids []int
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "cpu") {
			continue
		}
		n, err := strconv.Atoi(strings.TrimPrefix(e.Name(), "cpu"))
		if err != nil {
			continue
		}
		ids = append(ids, n)
	}
	sort.Ints(ids)
	if len(ids) == 0 {
		return []Core{{ID: 0, Core: 0, Sibling: -1}}
	}
	out := make([]Core, len(ids))
	for i, id := range ids {
		base := filepath.Join(dir, "cpu"+strconv.Itoa(id), "topology")
		core := readInt(filepath.Join(base, "core_id"), id)
		sib := -1
		thread := 0
		if b, err := os.ReadFile(filepath.Join(base, "thread_siblings_list")); err == nil {
			parts := strings.Split(strings.TrimSpace(string(b)), ",")
			if len(parts) == 2 {
				a, _ := strconv.Atoi(strings.TrimSpace(parts[0]))
				c, _ := strconv.Atoi(strings.TrimSpace(parts[1]))
				if a == id {
					sib = c
				} else {
					sib = a
					thread = 1
				}
			}
		}
		out[i] = Core{ID: id, Core: core, Thread: thread, Sibling: sib}
	}
	return out
}

func readInt(path string, def int) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return def
	}
	return n
}

func (m *Manager) memoryMB() int {
	b, err := os.ReadFile(m.sys("/proc/meminfo"))
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				kb, _ := strconv.Atoi(fields[1])
				return kb / 1024
			}
		}
	}
	return 0
}

func (m *Manager) bridges() []string {
	dir := m.sys("/sys/class/net")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []string{}
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if skipBridge(name) {
			continue
		}
		if st, err := os.Stat(filepath.Join(dir, name, "bridge")); err == nil && st.IsDir() {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func skipBridge(name string) bool {
	for _, p := range []string{"virbr", "docker", "br-", "veth", "vnet", "tap", "cni", "kube", "flannel", "weave", "vir"} {
		if p == "vir" {
			continue
		}
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

func (m *Manager) iommu() ([]IOMMUGroup, string) {
	root := m.sys("/sys/kernel/iommu_groups")
	entries, err := os.ReadDir(root)
	groups := map[int][]PCIDevice{}
	if err == nil {
		for _, e := range entries {
			id, err := strconv.Atoi(e.Name())
			if err != nil {
				continue
			}
			devs, _ := os.ReadDir(filepath.Join(root, e.Name(), "devices"))
			for _, d := range devs {
				if dev, ok := m.readPCI(d.Name(), id); ok {
					groups[id] = append(groups[id], dev)
				}
			}
		}
	}
	if len(groups) == 0 {
		pciRoot := m.sys("/sys/bus/pci/devices")
		devs, err := os.ReadDir(pciRoot)
		if err != nil || len(devs) == 0 {
			return []IOMMUGroup{}, "IOMMU groups are not available, so PCI passthrough will not work. Enable VT-d or AMD-Vi in the firmware and start the kernel with intel_iommu=on or amd_iommu=on."
		}
		var flat []PCIDevice
		for _, d := range devs {
			if dev, ok := m.readPCI(d.Name(), -1); ok {
				flat = append(flat, dev)
			}
		}
		sort.Slice(flat, func(i, j int) bool { return flat[i].Address < flat[j].Address })
		if len(flat) == 0 {
			return []IOMMUGroup{}, "No PCI devices were found."
		}
		return []IOMMUGroup{{ID: -1, Devices: flat}}, "IOMMU groups are not available, so a device cannot be isolated from the host. Enable VT-d or AMD-Vi in the firmware and start the kernel with intel_iommu=on or amd_iommu=on. The devices are listed so you can see them, but passing one through will fail until groups exist."
	}
	var ids []int
	for id := range groups {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	out := make([]IOMMUGroup, 0, len(ids))
	for _, id := range ids {
		devs := groups[id]
		sort.Slice(devs, func(i, j int) bool { return devs[i].Address < devs[j].Address })
		out = append(out, IOMMUGroup{ID: id, Devices: devs})
	}
	return out, ""
}

func (m *Manager) readPCI(addr string, group int) (PCIDevice, bool) {
	base := m.sys("/sys/bus/pci/devices/" + addr)
	vendor := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(readSmall(filepath.Join(base, "vendor")))), "0x")
	device := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(readSmall(filepath.Join(base, "device")))), "0x")
	if vendor == "" || device == "" {
		return PCIDevice{}, false
	}
	classRaw := strings.TrimSpace(readSmall(filepath.Join(base, "class")))
	classID := strings.TrimPrefix(strings.ToLower(classRaw), "0x")
	name := lookupPCI(vendor, device)
	if name == "" {
		name = classLabel(classID) + " " + vendor + ":" + device
	}
	return PCIDevice{
		Address: strings.ToLower(addr), Vendor: vendor, Device: device,
		Class: classLabel(classID), ClassID: classID, Name: name, Group: group,
	}, true
}

func classLabel(id string) string {
	if len(id) < 2 {
		return "Device"
	}
	switch id[:2] {
	case "01":
		return "Storage"
	case "02":
		return "Network"
	case "03":
		return "Display"
	case "04":
		return "Audio"
	case "06":
		return "Bridge"
	case "0c":
		return "USB controller"
	case "0d":
		return "Wireless"
	default:
		return "Device"
	}
}

func (m *Manager) usbDevices() []USBInfo {
	dir := m.sys("/sys/bus/usb/devices")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []USBInfo{}
	}
	var out []USBInfo
	for _, e := range entries {
		name := e.Name()
		if strings.Contains(name, ":") || strings.HasPrefix(name, "usb") {
			continue
		}
		base := filepath.Join(dir, name)
		vendor := strings.ToLower(strings.TrimSpace(readSmall(filepath.Join(base, "idVendor"))))
		product := strings.ToLower(strings.TrimSpace(readSmall(filepath.Join(base, "idProduct"))))
		if len(vendor) != 4 || len(product) != 4 {
			continue
		}
		prod := strings.TrimSpace(readSmall(filepath.Join(base, "product")))
		man := strings.TrimSpace(readSmall(filepath.Join(base, "manufacturer")))
		label := strings.TrimSpace(man + " " + prod)
		if label == "" {
			label = vendor + ":" + product
		}
		if strings.Contains(strings.ToLower(label), "root hub") {
			continue
		}
		out = append(out, USBInfo{
			Vendor: vendor, Product: product, Name: label,
			Bus:    strings.TrimSpace(readSmall(filepath.Join(base, "busnum"))),
			Device: strings.TrimSpace(readSmall(filepath.Join(base, "devnum"))),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func readSmall(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if len(b) > 512 {
		b = b[:512]
	}
	return string(b)
}

func listMedia(dir string, isoOnly bool) []Media {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []Media{}
	}
	var out []Media
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		low := strings.ToLower(name)
		if strings.HasSuffix(low, ".partial") || strings.HasSuffix(low, ".sha256") || low == "cidata.iso" {
			continue
		}
		ext := filepath.Ext(low)
		switch ext {
		case ".iso":
			if !isoOnly {
				continue
			}
		case ".img", ".qcow2", ".raw":
			if isoOnly {
				continue
			}
		default:
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Media{Name: name, Size: info.Size(), Virtio: strings.HasPrefix(low, "virtio-win") && ext == ".iso"})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

var (
	pciOnce  sync.Once
	pciNames map[string]string
)

func lookupPCI(vendor, device string) string {
	pciOnce.Do(loadPCI)
	if pciNames == nil {
		return ""
	}
	return pciNames[strings.ToLower(vendor)+":"+strings.ToLower(device)]
}

func loadPCI() {
	var b []byte
	var err error
	for _, p := range []string{"/usr/share/misc/pci.ids", "/usr/share/hwdata/pci.ids"} {
		b, err = os.ReadFile(p)
		if err == nil {
			break
		}
	}
	if err != nil {
		return
	}
	pciNames = map[string]string{}
	vendor, vendorName := "", ""
	for _, line := range strings.Split(string(b), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "C ") {
			break
		}
		if strings.HasPrefix(line, "\t\t") {
			continue
		}
		if strings.HasPrefix(line, "\t") {
			f := strings.Fields(line)
			if vendor != "" && len(f) >= 2 {
				name := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), f[0]))
				pciNames[vendor+":"+strings.ToLower(f[0])] = strings.TrimSpace(vendorName + " " + name)
			}
			continue
		}
		f := strings.Fields(line)
		if len(f) >= 2 && len(f[0]) == 4 {
			vendor = strings.ToLower(f[0])
			vendorName = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), f[0]))
		}
	}
}

func findOVMF() (code, vars string) {
	candidates := []string{
		"/usr/share/OVMF/OVMF_CODE_4M.fd",
		"/usr/share/OVMF/OVMF_CODE.fd",
		"/usr/share/edk2/ovmf/OVMF_CODE.fd",
		"/usr/share/OVMF/OVMF_CODE_4M.secboot.fd",
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			code = c
			break
		}
	}
	if code == "" {
		return "", ""
	}
	dir := filepath.Dir(code)
	try := []string{
		strings.Replace(filepath.Base(code), "CODE", "VARS", 1),
		"OVMF_VARS_4M.fd",
		"OVMF_VARS.fd",
	}
	for _, name := range try {
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return code, p
		}
	}
	return code, ""
}

func (m *Manager) virtiofsd() string {
	for _, p := range []string{"/usr/libexec/virtiofsd", "/usr/lib/qemu/virtiofsd"} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	if p, ok := lookBin("virtiofsd"); ok {
		return p
	}
	return ""
}

func (m *Manager) libvirtUp(ctx context.Context) bool {
	if !m.run.Has("virsh") {
		return false
	}
	_, err := m.run.Run(ctx, 5*time.Second, "virsh", "-c", "qemu:///system", "--quiet", "list", "--name")
	return err == nil
}
