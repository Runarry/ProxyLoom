package isolation

import (
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

type Target struct {
	Addr     string
	URL      string
	Log      *Log
	listener net.Listener
	server   *http.Server
	seq      atomic.Int64
}

func StartHTTPTarget(bind string, log *Log) (*Target, error) {
	return startTarget(bind, nil, log)
}

func StartHTTPSTarget(bind string, leaf Leaf, log *Log) (*Target, error) {
	return startTarget(bind, &tls.Config{Certificates: []tls.Certificate{leaf.Certificate}, MinVersion: tls.VersionTLS12}, log)
}

func startTarget(bind string, tlsConfig *tls.Config, log *Log) (*Target, error) {
	if bind == "" {
		bind = "127.0.0.1:0"
	}
	listener, err := net.Listen("tcp", bind)
	if err != nil {
		return nil, err
	}
	if tlsConfig != nil {
		listener = tls.NewListener(listener, tlsConfig)
	}
	target := &Target{Addr: listener.Addr().String(), listener: listener, Log: log}
	scheme := "http"
	if tlsConfig != nil {
		scheme = "https"
	}
	target.URL = scheme + "://" + target.Addr + "/probe"
	mux := http.NewServeMux()
	mux.HandleFunc("/probe", target.handleProbe)
	target.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       3 * time.Second,
		WriteTimeout:      3 * time.Second,
	}
	go target.server.Serve(listener)
	return target, nil
}

func (t *Target) handleProbe(w http.ResponseWriter, r *http.Request) {
	id := t.seq.Add(1)
	if t.Log != nil {
		t.Log.Record(Event{Role: "target", Remote: r.RemoteAddr, Result: "ok", RequestID: formatSeq(id)})
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "request_id": id, "remote_addr": r.RemoteAddr})
}

func (t *Target) Close() error {
	if t == nil || t.server == nil {
		return nil
	}
	return t.server.Close()
}

func formatSeq(n int64) string {
	if n <= 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
