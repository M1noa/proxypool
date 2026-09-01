package check

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
)

// socks4Server accepts one connection, reads the 9-byte request, hands it to
// the caller for inspection, and writes back the given reply code.
func socks4Server(t *testing.T, replyCode byte) (addr string, gotReq chan []byte) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	gotReq = make(chan []byte, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 9)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return
		}
		gotReq <- buf
		conn.Write([]byte{0x00, replyCode, 0, 0, 0, 0, 0, 0})
	}()
	t.Cleanup(func() { ln.Close() })
	return ln.Addr().String(), gotReq
}

func TestDialSOCKS4Success(t *testing.T) {
	addr, gotReq := socks4Server(t, 0x5A)
	destIP := net.ParseIP("5.6.7.8").To4()

	conn, err := dialSOCKS4(context.Background(), addr, destIP, 8080)
	if err != nil {
		t.Fatalf("dialSOCKS4: %v", err)
	}
	defer conn.Close()

	req := <-gotReq
	if req[0] != 0x04 || req[1] != 0x01 {
		t.Errorf("request header = %#v, want [0x04, 0x01, ...]", req[:2])
	}
	if port := binary.BigEndian.Uint16(req[2:4]); port != 8080 {
		t.Errorf("request port = %d, want 8080", port)
	}
	if !net.IP(req[4:8]).Equal(destIP) {
		t.Errorf("request dest ip = %v, want %v", net.IP(req[4:8]), destIP)
	}
	if req[8] != 0x00 {
		t.Errorf("request userid terminator = %#x, want 0x00", req[8])
	}
}

func TestDialSOCKS4Rejected(t *testing.T) {
	addr, _ := socks4Server(t, 0x5B) // request rejected or failed
	destIP := net.ParseIP("5.6.7.8").To4()

	if _, err := dialSOCKS4(context.Background(), addr, destIP, 80); err == nil {
		t.Error("dialSOCKS4 with a rejection reply = nil error, want an error")
	}
}

func TestDialSOCKS4NonIPv4Destination(t *testing.T) {
	destIP := net.ParseIP("2001:db8::1") // ipv6, no To4()
	if _, err := dialSOCKS4(context.Background(), "127.0.0.1:1", destIP, 80); err == nil {
		t.Error("dialSOCKS4 with an ipv6 destination = nil error, want an error")
	}
}

func TestDialSOCKS4DialFailure(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close() // nothing is listening now

	destIP := net.ParseIP("5.6.7.8").To4()
	if _, err := dialSOCKS4(context.Background(), addr, destIP, 80); err == nil {
		t.Error("dialSOCKS4 against a closed listener = nil error, want an error")
	}
}
