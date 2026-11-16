package files

import (
	"net"
	"testing"
)

func TestCheckIP(t *testing.T) {
	always := []string{
		"127.0.0.1",
		"127.0.0.2",
		"::1",
		"0.0.0.0",
		"::",
		"169.254.1.1",
		"169.254.169.254",
		"169.254.170.2",
		"fe80::1",
		"255.255.255.255",
		"224.0.0.1",
		"fd00:ec2::254",
		"100.100.100.200",
		"::ffff:127.0.0.1",
		"::ffff:169.254.169.254",
	}
	for _, raw := range always {
		ip := net.ParseIP(raw)
		if err := CheckIP(ip, false); err == nil {
			t.Errorf("%s allowed with LAN off", raw)
		}
		if err := CheckIP(ip, true); err == nil {
			t.Errorf("%s allowed with LAN on", raw)
		}
	}

	lan := []string{"10.1.2.3", "192.168.1.9", "172.16.0.4", "100.64.1.1", "fd12:3456::1", "::ffff:10.0.0.8"}
	for _, raw := range lan {
		ip := net.ParseIP(raw)
		if err := CheckIP(ip, false); err == nil {
			t.Errorf("%s allowed with LAN off", raw)
		}
		if err := CheckIP(ip, true); err != nil {
			t.Errorf("%s blocked with LAN on: %v", raw, err)
		}
	}

	public := []string{"1.1.1.1", "8.8.8.8", "203.0.113.10", "2606:4700:4700::1111"}
	for _, raw := range public {
		ip := net.ParseIP(raw)
		if err := CheckIP(ip, false); err != nil {
			t.Errorf("%s blocked: %v", raw, err)
		}
	}
	if err := CheckIP(nil, true); err == nil {
		t.Fatal("nil address allowed")
	}
}

func TestValidateURL(t *testing.T) {
	bad := []string{
		"",
		"file:///etc/passwd",
		"gopher://example.com",
		"http://",
		"http://user:pass@example.com/a",
		"http://localhost/x",
		"http://localhost.localdomain/x",
		"http://metadata.google.internal/computeMetadata/v1/",
		"http://METADATA.GOOGLE.INTERNAL./",
		"http://169.254.169.254/latest/meta-data/",
		"http://[::1]/",
		"http://127.0.0.1:9/secret",
		"http://127.1/secret",
		"http://0x7f000001/",
		"http://[::ffff:127.0.0.1]/",
		"javascript:alert(1)",
		"http://foo.localhost/x",
	}
	for _, raw := range bad {
		if _, err := ValidateURL(raw); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	// A private literal is not rejected until the LAN policy is applied.
	u, err := ValidateURL("http://10.1.2.3/file")
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckIP(net.ParseIP(u.Hostname()), false); err == nil {
		t.Fatal("private literal skipped the LAN check")
	}
	if _, err := ValidateURL("https://example.com/a/b.bin?token=1"); err != nil {
		t.Fatal(err)
	}
	parsed, err := ValidateURL("https://user.example/a/b")
	if err != nil {
		t.Fatal(err)
	}
	if got := SafeURL(parsed); got != "https://user.example/a/b" {
		t.Fatal(got)
	}
}
