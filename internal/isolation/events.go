package isolation

import (
	"encoding/json"
	"net"
	"net/http"
	"time"
)

// EventView is the test-only JSON shape for fixture observation. It contains
// connection metadata, not credentials.
type EventView struct {
	Seq        int    `json:"seq"`
	Role       string `json:"role"`
	Remote     string `json:"remote"`
	SNI        string `json:"sni,omitempty"`
	Result     string `json:"result"`
	RequestID  string `json:"request_id,omitempty"`
	TargetHost string `json:"target_host,omitempty"`
}

type EventServer struct {
	Addr     string
	listener net.Listener
	server   *http.Server
}

func ListenEvents(bind string, log *Log) (*EventServer, error) {
	if bind == "" {
		bind = "127.0.0.1:0"
	}
	listener, err := net.Listen("tcp", bind)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	mux.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		events := log.Events()
		views := make([]EventView, 0, len(events))
		for _, event := range events {
			views = append(views, EventView{
				Seq: event.Seq, Role: event.Role, Remote: event.Remote, SNI: event.SNI,
				Result: event.Result, RequestID: event.RequestID, TargetHost: event.TargetHost,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "events": views})
	})
	mux.HandleFunc("/reset", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		log.Reset()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       3 * time.Second,
		WriteTimeout:      3 * time.Second,
	}
	go server.Serve(listener)
	return &EventServer{Addr: listener.Addr().String(), listener: listener, server: server}, nil
}

func (s *EventServer) Close() error {
	if s == nil || s.server == nil {
		return nil
	}
	return s.server.Close()
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func RemoteIP(remote string) string {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return remote
	}
	return host
}
