package health

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseSMART(t *testing.T) {
	ata := []byte(`{
	  "device": {"name": "/dev/sda"},
	  "model_name": "Example HDD",
	  "smart_status": {"passed": true},
	  "ata_smart_attributes": {"table": [
	    {"id": 5, "name": "Reallocated_Sector_Ct", "value": 100, "worst": 100, "thresh": 10, "raw": {"value": 3, "string": "3"}},
	    {"id": 197, "name": "Current_Pending_Sector", "value": 100, "worst": 100, "thresh": 0, "raw": {"value": 1, "string": "1"}},
	    {"id": 194, "name": "Temperature_Celsius", "value": 100, "worst": 100, "thresh": 0, "raw": {"value": 36, "string": "36"}}
	  ]}
	}`)
	d, err := ParseSMART(ata)
	if err != nil {
		t.Fatal(err)
	}
	if d.Device != "/dev/sda" || d.Reallocated != 3 || d.Pending != 1 || d.TempC == nil || *d.TempC != 36 {
		t.Fatalf("%+v", d)
	}
	if d.Passed == nil || !*d.Passed {
		t.Fatal("passed")
	}

	standby := []byte(`{"smartctl":{"messages":[{"string":"Device is in STANDBY mode, exiting"}]},"device":{"name":"/dev/sdb"}}`)
	s, err := ParseSMART(standby)
	if err != nil || !s.Asleep {
		t.Fatalf("%+v %v", s, err)
	}

	nvme := []byte(`{
	  "device": {"name": "/dev/nvme0n1"},
	  "model_name": "Example SSD",
	  "smart_status": {"passed": true},
	  "temperature": {"current": 41},
	  "nvme_smart_health_information_log": {
	    "temperature": 41, "available_spare": 100, "available_spare_threshold": 10,
	    "percentage_used": 2, "media_errors": 0
	  }
	}`)
	n, err := ParseSMART(nvme)
	if err != nil || !n.HasWear || n.Wear != 2 || !n.HasSpare || n.Spare != 100 {
		t.Fatalf("%+v %v", n, err)
	}
}

func TestParseZPoolAndUnits(t *testing.T) {
	pools := ParseZPool("tank\t1000\t800\t200\t-\t-\t0\t80\t1.00\tDEGRADED\t-\nshort line\n")
	if len(pools) != 1 || pools[0].Name != "tank" || pools[0].UsedPercent != 80 || pools[0].Health != "DEGRADED" {
		t.Fatalf("%+v", pools)
	}
	units := ParseUnits("nginx.service loaded failed failed A web server\n\nnot a unit\n")
	if len(units) != 1 || units[0].Name != "nginx.service" || !strings.Contains(units[0].Description, "web") {
		t.Fatalf("%+v", units)
	}
	if len(ParseUnits("")) != 0 {
		t.Fatal("empty")
	}
}

func TestListBlock(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"sda", "sda1", "nvme0n1", "nvme0n1p1", "loop0", "mmcblk0", "mmcblk0p1", "vda"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got := strings.Join(listBlock(root), ",")
	for _, want := range []string{"/dev/sda", "/dev/nvme0n1", "/dev/mmcblk0", "/dev/vda"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in %s", want, got)
		}
	}
	for _, skip := range []string{"loop0", "sda1", "nvme0n1p1", "mmcblk0p1"} {
		if strings.Contains(got, skip) {
			t.Fatalf("included %s in %s", skip, got)
		}
	}
}

func TestReadCerts(t *testing.T) {
	dir := t.TempDir()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "home.example"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(48 * time.Hour),
		DNSNames:     []string{"home.example"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	raw := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(filepath.Join(dir, "site.crt"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	// A private key in the same folder must not be treated as a certificate.
	if err := os.WriteFile(filepath.Join(dir, "site.key"), []byte("not a cert"), 0o600); err != nil {
		t.Fatal(err)
	}
	certs, err := ReadCerts(t.Context(), []string{dir, filepath.Join(dir, "missing")})
	if err != nil {
		t.Fatal(err)
	}
	if len(certs) != 1 || certs[0].Name != "home.example" {
		t.Fatalf("%+v", certs)
	}
}

func TestSensorKindAndMilli(t *testing.T) {
	if sensorKind("coretemp") != "cpu" || sensorKind("amdgpu") != "gpu" || sensorKind("nvme") != "disk" || sensorKind("battery") != "" {
		t.Fatal(sensorKind("coretemp"), sensorKind("amdgpu"), sensorKind("nvme"), sensorKind("battery"))
	}
	c, ok := milliC("42000")
	if !ok || c < 41 || c > 43 {
		t.Fatalf("%v %v", c, ok)
	}
	if _, ok := milliC("0"); ok {
		t.Fatal("zero is not a reading")
	}
}

func TestSeriesRing(t *testing.T) {
	old := seriesCap
	seriesCap = 20
	t.Cleanup(func() { seriesCap = old })
	dir := t.TempDir()
	path := filepath.Join(dir, "health-series.json")
	s := OpenSeries(path)
	for i := 0; i < seriesCap+5; i++ {
		s.Add(Point{T: int64(i), CPU: float64(i)})
	}
	if len(s.points) != seriesCap {
		t.Fatalf("len %d", len(s.points))
	}
	if s.points[0].T != 5 {
		t.Fatalf("oldest %d", s.points[0].T)
	}
	loaded := OpenSeries(path)
	if len(loaded.points) != seriesCap || loaded.points[len(loaded.points)-1].CPU != float64(seriesCap+4) {
		t.Fatalf("reload %+v", loaded.points[len(loaded.points)-1])
	}
	got := loaded.Since(time.Unix(int64(seriesCap), 0), 10)
	if len(got) == 0 || len(got) > 10 {
		t.Fatalf("window %d", len(got))
	}
}
