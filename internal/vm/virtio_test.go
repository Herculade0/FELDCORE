package vm

import (
	"bytes"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCloudInitISO(t *testing.T) {
	spec := Spec{
		Name: "box",
		CloudInit: CloudInit{
			Enabled: true, User: "core", Password: `p@ss"word`,
			SSHKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFakeKeyForTheRoundTripTest",
		},
	}
	img, err := cloudInitISO(spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(img)%2048 != 0 {
		t.Fatalf("iso length %d", len(img))
	}
	pvd := img[16*2048 : 17*2048]
	if pvd[0] != 1 || string(pvd[1:6]) != "CD001" || !bytes.Contains(pvd[40:72], []byte("cidata")) {
		t.Fatal("primary descriptor")
	}
	svd := img[17*2048 : 18*2048]
	if svd[0] != 2 || string(svd[88:91]) != "%/E" {
		t.Fatal("joliet descriptor")
	}
	if !bytes.Contains(img, []byte("#cloud-config")) || !bytes.Contains(img, []byte("core")) {
		t.Fatal("user-data missing")
	}
	if !bytes.Contains(img, []byte(`"p@ss\"word"`)) {
		t.Fatal("password was not quoted")
	}
	// Joliet stores the real file name as UTF-16BE.
	if !bytes.Contains(img, utf16be("user-data")) || !bytes.Contains(img, utf16be("meta-data")) {
		t.Fatal("joliet names missing")
	}
	if _, err := cloudInitISO(Spec{Name: "box", CloudInit: CloudInit{Enabled: true, User: "core"}}); err == nil {
		t.Fatal("cloud-init with no credential accepted")
	}
}

func TestVerifyISOFile(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.iso")
	if err := os.WriteFile(bad, bytes.Repeat([]byte("<html>"), 100), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyISOFile(bad, 0); err == nil {
		t.Fatal("html accepted")
	}
	good := filepath.Join(dir, "good.iso")
	buf := bytes.Repeat([]byte{0}, 32<<20)
	copy(buf[32768:], []byte{1, 'C', 'D', '0', '0', '1'})
	if err := os.WriteFile(good, buf, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyISOFile(good, int64(len(buf))); err != nil {
		t.Fatal(err)
	}
	if err := verifyISOFile(good, int64(len(buf))+1); err == nil {
		t.Fatal("truncated file accepted")
	}
}

func TestVirtioNamesAndAllowlist(t *testing.T) {
	ver, file := virtioNames("https://fedorapeople.org/groups/virt/virtio-win/direct-downloads/archive-virtio/virtio-win-0.1.302-1/virtio-win-0.1.302.iso")
	if ver != "0.1.302-1" || file != "virtio-win-0.1.302.iso" {
		t.Fatalf("versioned file: %s %s", ver, file)
	}
	ver, file = virtioNames("http://fedorapeople.org/groups/virt/virtio-win/direct-downloads/archive-virtio/virtio-win-0.1.302-1/virtio-win.iso")
	if ver != "0.1.302-1" || file != "virtio-win-0.1.302-1.iso" {
		t.Fatalf("symlink: %s %s", ver, file)
	}
	u, _ := url.Parse("https://fedorapeople.org/groups/virt/virtio-win/direct-downloads/stable-virtio/virtio-win.iso")
	if err := allowedVirtio(u); err != nil {
		t.Fatal(err)
	}
	u, _ = url.Parse("https://evil.example/virtio-win.iso")
	if err := allowedVirtio(u); err == nil {
		t.Fatal("other host accepted")
	}
	u, _ = url.Parse("https://fedorapeople.org/groups/virt/other/virtio-win.iso")
	if err := allowedVirtio(u); err == nil {
		t.Fatal("other folder accepted")
	}
	sum := checksumFor("66f65c16ab3e8dfe12c2855a0d1ac303  virtio-win.rpm\nabcabcabcabcabcabcabcabcabcabcab  virtio-win-0.1.302.iso\n", "virtio-win-0.1.302.iso")
	if sum != "abcabcabcabcabcabcabcabcabcabcab" {
		t.Fatalf("checksum %s", sum)
	}
	if checksumFor("66f65c16ab3e8dfe12c2855a0d1ac303  virtio-win-0.1.302-1.noarch.rpm\n", "virtio-win-0.1.302.iso") != "" {
		t.Fatal("rpm checksum applied to the iso")
	}
}

func TestQcow2Size(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "disk.qcow2")
	hdr := make([]byte, 64)
	copy(hdr, "QFI\xfb")
	// virtual size at offset 24, 20 GiB
	hdr[24], hdr[25], hdr[26], hdr[27] = 0, 0, 0, 5
	hdr[28], hdr[29], hdr[30], hdr[31] = 0, 0, 0, 0
	// 5<<32 = 20 GiB? 5 * 2^32 = 21474836480 = 20 GiB. Yes if we put 5 in the high uint32.
	// Big endian uint64: bytes 24-31 = 0x0000000500000000
	if err := os.WriteFile(p, hdr, 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := imageBytes(p)
	if err != nil {
		t.Fatal(err)
	}
	if n != 5<<32 || roundGB(n) != 20 {
		t.Fatalf("size %d gb %d", n, roundGB(n))
	}
	if imageFormat(p) != "qcow2" {
		t.Fatal("format")
	}
}

func TestImageBytesRaw(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "disk.raw")
	if err := os.WriteFile(p, bytes.Repeat([]byte{1}, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := imageBytes(p)
	if err != nil || n != 4096 {
		t.Fatalf("%d %v", n, err)
	}
	if strings.Contains(imageFormat(p), "qcow") {
		t.Fatal("raw detected as qcow")
	}
}
