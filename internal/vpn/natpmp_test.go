package vpn

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestGatewayIP(t *testing.T) {
	got, err := GatewayIP("10.2.0.2/32")
	if err != nil || got != "10.2.0.1" {
		t.Fatalf("proton /32 gateway = %s %v", got, err)
	}
	got, err = GatewayIP("10.7.0.5/24, fd00::2/128")
	if err != nil || got != "10.7.0.1" {
		t.Fatalf("subnet gateway = %s %v", got, err)
	}
}

func TestNATMapRoundTrip(t *testing.T) {
	req := MarshalMapRequest(OpTCP, 443, 0, 60)
	if req[1] != OpTCP || binary.BigEndian.Uint16(req[4:6]) != 443 || binary.BigEndian.Uint32(req[8:12]) != 60 {
		t.Fatalf("request %x", req)
	}
	res := make([]byte, 16)
	res[1] = OpTCP + 128
	binary.BigEndian.PutUint16(res[10:12], 4242)
	binary.BigEndian.PutUint32(res[12:16], 60)
	conn := &fakeConn{res: res}
	got, err := QueryMap(conn, OpTCP, 443, 0, 60)
	if err != nil || got.External != 4242 || got.Lifetime != 60 {
		t.Fatalf("response %+v %v", got, err)
	}
	if !bytes.Equal(conn.got, req) {
		t.Fatalf("sent %x want %x", conn.got, req)
	}
	res[2] = 0
	res[3] = 2
	if _, err := ParseMapResponse(res); err == nil {
		t.Fatal("a refused mapping should error")
	}
}

type fakeConn struct {
	got []byte
	res []byte
}

func (f *fakeConn) Write(p []byte) (int, error) {
	f.got = append(f.got, p...)
	return len(p), nil
}

func (f *fakeConn) Read(p []byte) (int, error) {
	n := copy(p, f.res)
	f.res = f.res[n:]
	return n, nil
}
