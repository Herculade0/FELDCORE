package health

import "testing"

func TestDropInactiveNFSHelpers(t *testing.T) {
	units := []Unit{
		{Name: "nfsdcld.service", Description: "NFSv4 Client Tracking Daemon"},
		{Name: "rpc-statd.service", Description: "NFS status monitor"},
		{Name: "fsidd.service", Description: "NFS FSID Daemon"},
		{Name: "nginx.service", Description: "web server"},
	}

	// NFS is not in use: the three helper failures are noise and must be
	// dropped, but an unrelated failure stays.
	got := DropInactiveNFSHelpers(units, false)
	if len(got) != 1 || got[0].Name != "nginx.service" {
		t.Fatalf("inactive NFS: got %+v, want only nginx.service", got)
	}

	// NFS is in use: keep every failure so a real NFS fault still surfaces.
	got = DropInactiveNFSHelpers(units, true)
	if len(got) != len(units) {
		t.Fatalf("active NFS: got %d units, want %d", len(got), len(units))
	}
}

func TestNFSHelperUnitNaming(t *testing.T) {
	for _, name := range []string{"rpc-statd", "rpc-statd.service", "fsidd.service", "nfsdcld"} {
		if !nfsHelperUnit(name) {
			t.Errorf("%q should be recognised as an NFS helper", name)
		}
	}
	for _, name := range []string{"nfs-server.service", "nginx.service", "docker.service"} {
		if nfsHelperUnit(name) {
			t.Errorf("%q must not be treated as an NFS helper", name)
		}
	}
}
