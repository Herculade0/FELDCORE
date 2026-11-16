package vpn

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

// NAT-PMP (RFC 6886). Proton's WireGuard servers answer this on the tunnel
// gateway when the config was generated with port forwarding enabled.
const (
	natPort      = 5351
	OpUDP   byte = 1
	OpTCP   byte = 2
)

// GatewayIP is the address Proton's NAT-PMP server uses. A /32 client
// address such as 10.2.0.2/32 has no usable network address, and Proton's
// gateway is the .1 in that /24.
func GatewayIP(address string) (string, error) {
	first := strings.TrimSpace(address)
	if i := strings.Index(first, ","); i >= 0 {
		first = strings.TrimSpace(first[:i])
	}
	ip, ipnet, err := net.ParseCIDR(first)
	if err != nil {
		return "", input("The tunnel address is not valid, so the NAT-PMP gateway cannot be worked out.")
	}
	ip = ip.To4()
	if ip == nil {
		return "", input("NAT-PMP for Proton uses an IPv4 tunnel address.")
	}
	ones, _ := ipnet.Mask.Size()
	if ones >= 31 {
		gw := make(net.IP, len(ip))
		copy(gw, ip)
		gw[3] = 1
		return gw.String(), nil
	}
	base := ip.Mask(ipnet.Mask).To4()
	if base == nil {
		return "", input("The tunnel address is not a usable IPv4 network.")
	}
	base[3] = 1
	return base.String(), nil
}

// MarshalMapRequest builds a NAT-PMP map request. external 0 asks the gateway
// to choose the public port. lifetime is in seconds.
func MarshalMapRequest(op byte, internal, external uint16, lifetime uint32) []byte {
	b := make([]byte, 12)
	b[0] = 0
	b[1] = op
	binary.BigEndian.PutUint16(b[4:6], internal)
	binary.BigEndian.PutUint16(b[6:8], external)
	binary.BigEndian.PutUint32(b[8:12], lifetime)
	return b
}

// MapResult is a successful NAT-PMP map response.
type MapResult struct {
	Op       byte
	External uint16
	Lifetime uint32
}

// ParseMapResponse reads a NAT-PMP map response.
func ParseMapResponse(p []byte) (MapResult, error) {
	if len(p) < 16 {
		return MapResult{}, fmt.Errorf("NAT-PMP response is too short")
	}
	if p[0] != 0 {
		return MapResult{}, fmt.Errorf("NAT-PMP version %d is not supported", p[0])
	}
	op := p[1]
	if op != OpUDP+128 && op != OpTCP+128 {
		return MapResult{}, fmt.Errorf("NAT-PMP opcode %d is not a map response", op)
	}
	code := binary.BigEndian.Uint16(p[2:4])
	if code != 0 {
		return MapResult{}, fmt.Errorf("NAT-PMP refused the request (code %d)", code)
	}
	return MapResult{
		Op:       op - 128,
		External: binary.BigEndian.Uint16(p[10:12]),
		Lifetime: binary.BigEndian.Uint32(p[12:16]),
	}, nil
}

// QueryMap sends one map request on conn and reads the response.
// conn's deadline should already be set.
func QueryMap(conn io.ReadWriter, op byte, internal, external uint16, lifetime uint32) (MapResult, error) {
	if _, err := conn.Write(MarshalMapRequest(op, internal, external, lifetime)); err != nil {
		return MapResult{}, err
	}
	buf := make([]byte, 16)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return MapResult{}, err
	}
	res, err := ParseMapResponse(buf)
	if err != nil {
		return MapResult{}, err
	}
	if res.Op != op {
		return MapResult{}, fmt.Errorf("NAT-PMP answered a different request")
	}
	return res, nil
}

// DialMap asks the gateway for a public port. It is called from inside the
// tunnel's network namespace.
func DialMap(gateway string, op byte, internal uint16, lifetime uint32) (MapResult, error) {
	conn, err := net.DialTimeout("udp", net.JoinHostPort(gateway, fmt.Sprintf("%d", natPort)), 3*time.Second)
	if err != nil {
		return MapResult{}, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	return QueryMap(conn, op, internal, 0, lifetime)
}
