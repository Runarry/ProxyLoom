package isolation

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	trojanConnect = 0x01
	atypIPv4      = 0x01
	atypDomain    = 0x03
	atypIPv6      = 0x04
)

type Proxy struct {
	Addr     string
	Host     string
	Log      *Log
	listener net.Listener
	cancel   context.CancelFunc
}

type ProxyConfig struct {
	Bind        string
	Host        string
	Password    string
	Certificate tls.Certificate
	AllowFrom   []net.IP
	DialLocal   net.IP
	Role        string
	Log         *Log
}

type ClientConfig struct {
	Address    string
	ServerName string
	Password   string
	RootCAs    *x509.CertPool
	DialLocal  net.IP
}

func StartTrojan(cfg ProxyConfig) (*Proxy, error) {
	if cfg.Password == "" || cfg.Host == "" {
		return nil, errors.New("trojan fixture requires host and password")
	}
	bind := cfg.Bind
	if bind == "" {
		bind = "127.0.0.1:0"
	}
	tlsConfig := &tls.Config{Certificates: []tls.Certificate{cfg.Certificate}, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}}
	inner, err := net.Listen("tcp", bind)
	if err != nil {
		return nil, err
	}
	listener := tls.NewListener(inner, tlsConfig)
	ctx, cancel := context.WithCancel(context.Background())
	proxy := &Proxy{Addr: listener.Addr().String(), Host: cfg.Host, Log: cfg.Log, listener: listener, cancel: cancel}
	go proxy.serve(ctx, cfg)
	return proxy, nil
}

func (p *Proxy) Close() error {
	if p == nil {
		return nil
	}
	p.cancel()
	return p.listener.Close()
}

func (p *Proxy) serve(ctx context.Context, cfg ProxyConfig) {
	for {
		conn, err := p.listener.Accept()
		if err != nil {
			return
		}
		go p.handle(ctx, cfg, conn)
	}
}

func (p *Proxy) handle(ctx context.Context, cfg ProxyConfig, conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	remote := conn.RemoteAddr().String()
	if !allowedSource(conn.RemoteAddr(), cfg.AllowFrom) {
		p.record(cfg.Role, remote, "", "forbidden_source", "")
		return
	}
	sni := ""
	if tlsConn, ok := conn.(*tls.Conn); ok {
		if err := tlsConn.Handshake(); err != nil {
			p.record(cfg.Role, remote, "", "handshake_failed", "")
			return
		}
		sni = tlsConn.ConnectionState().ServerName
	}
	// Keep the same reader for the handshake and relay: it may already hold
	// application bytes sent in the same TLS record as the request header.
	reader := bufio.NewReader(conn)
	host, port, err := readTrojanRequest(reader, cfg.Password)
	if err != nil {
		p.record(cfg.Role, remote, sni, "auth_failed", "")
		return
	}
	dialer := net.Dialer{Timeout: 5 * time.Second}
	if cfg.DialLocal != nil {
		dialer.LocalAddr = &net.TCPAddr{IP: cfg.DialLocal}
	}
	upstream, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		p.record(cfg.Role, remote, sni, "dial_failed", host)
		return
	}
	defer upstream.Close()
	_ = conn.SetDeadline(time.Time{})
	p.record(cfg.Role, remote, sni, "ok", host)
	relay(conn, upstream, reader)
}

func (p *Proxy) record(role, remote, sni, result, host string) {
	if p.Log == nil {
		return
	}
	p.Log.Record(Event{Role: role, Remote: remote, SNI: sni, Result: result, TargetHost: host})
}

func allowedSource(addr net.Addr, allow []net.IP) bool {
	if len(allow) == 0 {
		return true
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, allowed := range allow {
		if allowed != nil && ip.Equal(allowed) {
			return true
		}
	}
	return false
}

func readTrojanRequest(reader *bufio.Reader, password string) (string, int, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", 0, err
	}
	line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	sum := sha256.Sum224([]byte(password))
	if !strings.EqualFold(line, hex.EncodeToString(sum[:])) {
		return "", 0, errors.New("trojan authentication failed")
	}
	cmd, err := reader.ReadByte()
	if err != nil {
		return "", 0, err
	}
	if cmd != trojanConnect {
		return "", 0, errors.New("only CONNECT is supported")
	}
	atyp, err := reader.ReadByte()
	if err != nil {
		return "", 0, err
	}
	var host string
	switch atyp {
	case atypIPv4:
		var ip [4]byte
		if _, err := io.ReadFull(reader, ip[:]); err != nil {
			return "", 0, err
		}
		host = net.IP(ip[:]).String()
	case atypIPv6:
		var ip [16]byte
		if _, err := io.ReadFull(reader, ip[:]); err != nil {
			return "", 0, err
		}
		host = net.IP(ip[:]).String()
	case atypDomain:
		n, err := reader.ReadByte()
		if err != nil {
			return "", 0, err
		}
		name := make([]byte, n)
		if _, err := io.ReadFull(reader, name); err != nil {
			return "", 0, err
		}
		host = string(name)
	default:
		return "", 0, errors.New("unsupported address type")
	}
	var portBuf [2]byte
	if _, err := io.ReadFull(reader, portBuf[:]); err != nil {
		return "", 0, err
	}
	crlf := make([]byte, 2)
	if _, err := io.ReadFull(reader, crlf); err != nil || crlf[0] != '\r' || crlf[1] != '\n' {
		return "", 0, errors.New("truncated trojan request")
	}
	return host, int(binary.BigEndian.Uint16(portBuf[:])), nil
}

func writeTrojanRequest(conn net.Conn, password, host string, port int) error {
	sum := sha256.Sum224([]byte(password))
	if _, err := io.WriteString(conn, hex.EncodeToString(sum[:])+"\r\n"); err != nil {
		return err
	}
	payload := []byte{trojanConnect, atypDomain, byte(len(host))}
	payload = append(payload, host...)
	var portBuf [2]byte
	binary.BigEndian.PutUint16(portBuf[:], uint16(port))
	payload = append(payload, portBuf[:]...)
	payload = append(payload, '\r', '\n')
	_, err := conn.Write(payload)
	return err
}

func DialTrojan(ctx context.Context, cfg ClientConfig, destHost string, destPort int) (net.Conn, error) {
	dialer := net.Dialer{Timeout: 5 * time.Second}
	if cfg.DialLocal != nil {
		dialer.LocalAddr = &net.TCPAddr{IP: cfg.DialLocal}
	}
	raw, err := dialer.DialContext(ctx, "tcp", cfg.Address)
	if err != nil {
		return nil, err
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: cfg.ServerName, RootCAs: cfg.RootCAs}
	conn := tls.Client(raw, tlsConfig)
	if err := conn.HandshakeContext(ctx); err != nil {
		raw.Close()
		return nil, err
	}
	if err := writeTrojanRequest(conn, cfg.Password, destHost, destPort); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func DialChain(ctx context.Context, first, second ClientConfig, destHost string, destPort int) (net.Conn, error) {
	secondHost, secondPort, err := splitAddr(second.Address)
	if err != nil {
		return nil, err
	}
	hop, err := DialTrojan(ctx, first, secondHost, secondPort)
	if err != nil {
		return nil, err
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: second.ServerName, RootCAs: second.RootCAs}
	inner := tls.Client(hop, tlsConfig)
	if err := inner.HandshakeContext(ctx); err != nil {
		hop.Close()
		return nil, err
	}
	if err := writeTrojanRequest(inner, second.Password, destHost, destPort); err != nil {
		inner.Close()
		return nil, err
	}
	return inner, nil
}

func splitAddr(address string) (string, int, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", 0, err
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return "", 0, err
	}
	return host, n, nil
}

func relay(a, b net.Conn, aReader io.Reader) {
	var done sync.WaitGroup
	done.Add(2)
	copy := func(dst net.Conn, src io.Reader) {
		defer done.Done()
		_, _ = io.Copy(dst, src)
		_ = dst.SetDeadline(time.Now())
	}
	go copy(a, b)
	go copy(b, aReader)
	done.Wait()
}

func ProbeHTTP(conn net.Conn, host string) ([]byte, error) {
	if host == "" {
		host = "target.proxyloom.test"
	}
	request := fmt.Sprintf("GET /probe HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", host)
	if _, err := io.WriteString(conn, request); err != nil {
		return nil, err
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	return io.ReadAll(conn)
}
