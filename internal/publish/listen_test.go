package publish

import (
	"strings"
	"testing"
)

func TestListenAddrs(t *testing.T) {
	got, err := ListenAddrs(8791, "", []string{"172.17.0.1", "127.0.0.1", "0.0.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "127.0.0.1:8791" || got[1] != "172.17.0.1:8791" {
		t.Fatalf("default %v", got)
	}
	if _, err := ListenAddrs(8791, "0.0.0.0", nil); err == nil {
		t.Fatal("0.0.0.0 was accepted")
	}
	if _, err := ListenAddrs(8791, "::", nil); err == nil {
		t.Fatal(":: was accepted")
	}
	got, err = ListenAddrs(8791, "192.0.2.10, 127.0.0.1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "192.0.2.10:8791,127.0.0.1:8791" {
		t.Fatalf("explicit %v", got)
	}
	for _, addr := range got {
		if strings.HasPrefix(addr, "0.0.0.0:") || strings.HasPrefix(addr, "[::]:") {
			t.Fatalf("bound every interface: %s", addr)
		}
	}
}
