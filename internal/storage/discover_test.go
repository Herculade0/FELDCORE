package storage

import (
	"context"
	"errors"
	"testing"
)

func TestParseLsblk(t *testing.T) {
	raw := []byte(`{
	  "blockdevices": [
	    {
	      "name": "sda", "path": "/dev/sda", "type": "disk", "size": 100000000000,
	      "model": "HGST HUS", "serial": "ABC123", "rota": true, "tran": "sata",
	      "fstype": null, "uuid": null, "mountpoints": [null],
	      "children": [
	        {"name": "sda1", "path": "/dev/sda1", "type": "part", "size": 500,
	         "fstype": "vfat", "mountpoints": ["/boot/efi"]}
	      ]
	    },
	    {
	      "name": "nvme0n1", "path": "/dev/nvme0n1", "type": "disk", "size": 512000000000,
	      "model": "Samsung", "serial": "NV1", "rota": false, "tran": "nvme",
	      "fstype": null, "mountpoints": [null]
	    },
	    {
	      "name": "vdc", "path": "/dev/vdc", "type": "disk", "size": 10,
	      "model": null, "serial": null, "rota": true, "tran": "virtio",
	      "fstype": "ext4", "uuid": "11111111-1111-1111-1111-111111111111",
	      "mountpoints": ["/"]
	    },
	    {"name": "sr0", "path": "/dev/sr0", "type": "rom", "size": 1, "rota": false}
	  ]
	}`)
	disks, err := parseLsblk(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(disks) != 3 {
		t.Fatalf("disks: %+v", disks)
	}
	sda := disks[0]
	if sda.Name != "sda" || sda.Serial != "ABC123" || sda.Type != "hdd" || !sda.OS || sda.Partitions != 1 {
		t.Fatalf("sda: %+v", sda)
	}
	if len(sda.ChildMounts) != 1 || sda.ChildMounts[0] != "/boot/efi" {
		t.Fatalf("mounts: %+v", sda.ChildMounts)
	}
	if disks[1].Type != "nvme" || disks[1].Rotational {
		t.Fatalf("nvme: %+v", disks[1])
	}
	if disks[2].Type != "virtual" || !disks[2].OS || disks[2].Mountpoint != "/" {
		t.Fatalf("vdc: %+v", disks[2])
	}
}

func TestParseSMART(t *testing.T) {
	raw := []byte(`{
	  "serial_number": "ABC123",
	  "smart_status": {"passed": true},
	  "temperature": {"current": 34},
	  "ata_smart_attributes": {"table": [
	    {"id": 5, "raw": {"value": 2}},
	    {"id": 9, "raw": {"value": 1000}},
	    {"id": 197, "raw": {"value": 1}}
	  ]}
	}`)
	sm, serial, ok := parseSMART(raw)
	if !ok || !sm.Available || sm.Healthy != "passed" || serial != "ABC123" {
		t.Fatalf("%+v %s %v", sm, serial, ok)
	}
	if sm.TempC == nil || *sm.TempC != 34 || sm.Reallocated == nil || *sm.Reallocated != 2 || sm.Pending == nil || *sm.Pending != 1 {
		t.Fatalf("%+v", sm)
	}
	bad := []byte(`{"smart_status": {"passed": false}, "smartctl": {"messages": [{"string": "nope"}]}}`)
	sm, _, ok = parseSMART(bad)
	if !ok || sm.Healthy != "failed" {
		t.Fatalf("%+v", sm)
	}
	if _, _, ok = parseSMART([]byte("not json")); ok {
		t.Fatal("parsed junk")
	}
}

func TestFillBlkid(t *testing.T) {
	run := &fakeRun{look: map[string]bool{"blkid": true}, fn: func(name string, args []string) (string, error) {
		if name == "blkid" && len(args) > 0 && args[len(args)-1] == "/dev/vda" {
			return "DEVNAME=/dev/vda\nUUID=aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee\nTYPE=ext4\n", nil
		}
		return "", errors.New("no medium")
	}}
	disks := []Disk{{Name: "vda", Path: "/dev/vda"}, {Name: "loop0", Path: "/dev/loop0", FSType: "xfs"}}
	fillBlkid(context.Background(), run, disks)
	if disks[0].FSType != "ext4" || disks[0].UUID != "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
		t.Fatalf("vda: %+v", disks[0])
	}
	if disks[1].FSType != "xfs" {
		t.Fatalf("existing type was replaced: %+v", disks[1])
	}
}

func TestIdentity(t *testing.T) {
	d := Disk{Path: "/dev/sda", Serial: "ABC", ByID: "/dev/disk/by-id/ata-HGST_ABC"}
	applyIdentity(&d)
	if d.Confirm != "ABC" || d.ConfirmKind != "serial" || d.ID != "serial:ABC" {
		t.Fatalf("%+v", d)
	}
	d = Disk{Path: "/dev/vda", ByID: "/dev/disk/by-id/virtio-99"}
	applyIdentity(&d)
	if d.Confirm != "virtio-99" || d.ConfirmKind != "by-id" {
		t.Fatalf("%+v", d)
	}
	d = Disk{Path: "/dev/loop0"}
	applyIdentity(&d)
	if d.Confirm != "" {
		t.Fatalf("loop without identity: %+v", d)
	}
}
