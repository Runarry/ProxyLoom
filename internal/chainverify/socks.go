package chainverify

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/Runarry/ProxyLoom/internal/isolation"
)

func socks5HTTPProbe(ctx context.Context, socksAddr, destHost string, destPort int, requestHost, requestID string) ([]byte, error) {
	dialer := net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", socksAddr)
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			conn.Close()
		}
	}()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if err := socks5Greeting(conn); err != nil {
		return nil, err
	}
	req, err := socksConnectRequest(destHost, destPort)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Write(req); err != nil {
		return nil, err
	}
	if err := readSocksConnectReply(conn); err != nil {
		return nil, err
	}
	body, err := isolation.ProbeHTTPWithID(conn, requestHost, requestID)
	conn.Close()
	success = true
	return body, err
}

func socks5Greeting(conn net.Conn) error {
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		return err
	}
	hello := make([]byte, 2)
	if _, err := io.ReadFull(conn, hello); err != nil {
		return err
	}
	if hello[0] != 0x05 || hello[1] != 0x00 {
		return fmt.Errorf("socks5 auth rejected: %x", hello)
	}
	return nil
}

func socksConnectRequest(host string, port int) ([]byte, error) {
	req := []byte{0x05, 0x01, 0x00}
	ip := net.ParseIP(host)
	switch {
	case ip != nil && ip.To4() != nil:
		req = append(req, 0x01)
		req = append(req, ip.To4()...)
	case ip != nil && ip.To16() != nil:
		req = append(req, 0x04)
		req = append(req, ip.To16()...)
	default:
		if len(host) == 0 || len(host) > 255 {
			return nil, fmt.Errorf("invalid socks host")
		}
		req = append(req, 0x03, byte(len(host)))
		req = append(req, host...)
	}
	var portBuf [2]byte
	binary.BigEndian.PutUint16(portBuf[:], uint16(port))
	return append(req, portBuf[:]...), nil
}

func readSocksConnectReply(conn net.Conn) error {
	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil {
		return err
	}
	if head[0] != 0x05 {
		return fmt.Errorf("socks5 reply version %d", head[0])
	}
	if head[1] != 0x00 {
		return fmt.Errorf("socks5 connect failed: status %d", head[1])
	}
	switch head[3] {
	case 0x01:
		_, err := io.ReadFull(conn, make([]byte, 4+2))
		return err
	case 0x04:
		_, err := io.ReadFull(conn, make([]byte, 16+2))
		return err
	case 0x03:
		size := make([]byte, 1)
		if _, err := io.ReadFull(conn, size); err != nil {
			return err
		}
		_, err := io.ReadFull(conn, make([]byte, int(size[0])+2))
		return err
	default:
		return fmt.Errorf("socks5 reply atyp %d", head[3])
	}
}
