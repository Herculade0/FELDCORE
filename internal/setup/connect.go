package setup

import (
	"context"
	"net"
	"strings"
	"time"
)

// ConnStatus is the result of a DNS and HTTPS check.
type ConnStatus struct {
	OK        bool   `json:"ok"`
	DNS       bool   `json:"dns"`
	Reachable bool   `json:"reachable"`
	Detail    string `json:"detail"`
}

func (s *Service) connectivity(ctx context.Context, dns string) ConnStatus {
	servers := splitDNS(dns)
	if s.check != nil {
		return s.check(ctx, servers)
	}
	return checkConnectivity(ctx, servers)
}

func splitDNS(dns string) []string {
	var out []string
	for _, d := range strings.Fields(strings.ReplaceAll(dns, ",", " ")) {
		if ip := net.ParseIP(d); ip != nil && ip.To4() != nil {
			out = append(out, d)
		}
	}
	return out
}

func checkConnectivity(ctx context.Context, servers []string) ConnStatus {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	r := &net.Resolver{PreferGo: true}
	if len(servers) > 0 {
		server := servers[0]
		r.Dial = func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{Timeout: 2 * time.Second}
			return d.DialContext(ctx, "udp", net.JoinHostPort(server, "53"))
		}
	}
	ips, err := r.LookupHost(ctx, "one.one.one.one")
	if err != nil || len(ips) == 0 {
		detail := "DNS lookup failed."
		if err != nil {
			detail = "DNS lookup failed: " + err.Error()
		}
		return ConnStatus{Detail: detail}
	}
	d := net.Dialer{Timeout: 2 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ips[0], "443"))
	if err != nil {
		return ConnStatus{DNS: true, Detail: "DNS works, but " + ips[0] + ":443 did not connect."}
	}
	_ = conn.Close()
	return ConnStatus{OK: true, DNS: true, Reachable: true, Detail: "DNS and HTTPS to " + ips[0] + " both worked."}
}
