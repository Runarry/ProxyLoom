package isolation_test

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Runarry/ProxyLoom/internal/isolation"
)

func TestTrojanPreservesPayload(t *testing.T) {
	ca, err := isolation.NewTestCA()
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := ca.Issue("a.proxyloom.test")
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := isolation.StartTrojan(isolation.ProxyConfig{
		Host: "a.proxyloom.test", Password: "EXAMPLE_ONLY_A", Certificate: leaf.Certificate,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()

	for _, test := range []struct {
		name  string
		size  int
		split bool
	}{
		{name: "header and payload in one TLS write", size: 128},
		{name: "fragmented header and payload", size: 128, split: true},
		{name: "payload larger than buffer", size: 64 * 1024},
	} {
		t.Run(test.name, func(t *testing.T) {
			payload := bytes.Repeat([]byte("0123456789abcdef"), test.size/16)
			target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil || !bytes.Equal(body, payload) {
					http.Error(w, "incomplete payload", http.StatusBadRequest)
					return
				}
				_, _ = w.Write(body)
			}))
			target.Config.ReadTimeout = 3 * time.Second
			target.Start()
			defer target.Close()
			host, portText, err := net.SplitHostPort(target.Listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			// Build one wire request independently of DialTrojan, which writes
			// the handshake separately from application data.
			sum := sha256.Sum224([]byte("EXAMPLE_ONLY_A"))
			header := append([]byte(hex.EncodeToString(sum[:])+"\r\n"), 1, 3, byte(len(host)))
			header = append(header, host...)
			header = binary.BigEndian.AppendUint16(header, uint16(atoi(t, portText)))
			header = append(header, '\r', '\n')
			request := []byte(fmt.Sprintf("POST /probe HTTP/1.1\r\nHost: target.proxyloom.test\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", len(payload)))
			request = append(request, payload...)
			wire := append(append([]byte(nil), header...), request...)
			conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", proxy.Addr, &tls.Config{
				MinVersion: tls.VersionTLS12, ServerName: "a.proxyloom.test", RootCAs: ca.Pool,
			})
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
			chunks := [][]byte{wire}
			if test.split {
				chunks = [][]byte{wire[:17], wire[17 : len(header)-1], wire[len(header)-1 : len(header)+11], wire[len(header)+11:]}
			}
			for _, chunk := range chunks {
				if _, err := conn.Write(chunk); err != nil {
					t.Fatal(err)
				}
			}
			response, err := http.ReadResponse(bufio.NewReader(conn), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil || response.StatusCode != http.StatusOK || !bytes.Equal(body, payload) {
				t.Fatalf("payload round trip failed: status=%d bytes=%d error=%v", response.StatusCode, len(body), err)
			}
		})
	}
}
