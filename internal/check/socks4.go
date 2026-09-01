package check

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
)

// dialSOCKS4 hand-rolls a SOCKS4 (not 4a) handshake. golang.org/x/net/proxy
// only speaks SOCKS5, and aiohttp_socks defaults socks4 to rdns=False, so
// destIP must already be a resolved ipv4 address — calibrate resolves the
// check host once for exactly this reason.
func dialSOCKS4(ctx context.Context, proxyAddr string, destIP net.IP, destPort int) (net.Conn, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, err
	}
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	ip4 := destIP.To4()
	if ip4 == nil {
		conn.Close()
		return nil, fmt.Errorf("socks4: destination is not ipv4: %s", destIP)
	}

	req := make([]byte, 0, 9)
	req = append(req, 0x04, 0x01)
	req = binary.BigEndian.AppendUint16(req, uint16(destPort))
	req = append(req, ip4...)
	req = append(req, 0x00) // empty userid, null-terminated
	if _, err := conn.Write(req); err != nil {
		conn.Close()
		return nil, err
	}

	reply := make([]byte, 8)
	if _, err := io.ReadFull(conn, reply); err != nil {
		conn.Close()
		return nil, err
	}
	if reply[1] != 0x5A {
		conn.Close()
		return nil, fmt.Errorf("socks4: request rejected or failed, code %d", reply[1])
	}
	return conn, nil
}
