package isolation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
)

type Event struct {
	Seq        int
	Role       string
	Remote     string
	SNI        string
	Result     string
	RequestID  string
	TargetHost string
}

type Log struct {
	mu     sync.Mutex
	events []Event
}

func (l *Log) Record(event Event) Event {
	if l == nil {
		return event
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	event.Seq = len(l.events) + 1
	l.events = append(l.events, event)
	return event
}

func (l *Log) Events() []Event {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Event, len(l.events))
	copy(out, l.events)
	return out
}

func (l *Log) Count(result string) int {
	n := 0
	for _, event := range l.Events() {
		if event.Result == result {
			n++
		}
	}
	return n
}

func (l *Log) CountRole(role, result string) int {
	n := 0
	for _, event := range l.Events() {
		if event.Role == role && event.Result == result {
			n++
		}
	}
	return n
}

func (l *Log) Seq() int {
	events := l.Events()
	if len(events) == 0 {
		return 0
	}
	return events[len(events)-1].Seq
}

func (l *Log) After(seq int) []Event {
	var out []Event
	for _, event := range l.Events() {
		if event.Seq > seq {
			out = append(out, event)
		}
	}
	return out
}

func (l *Log) Reset() {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.events = nil
	l.mu.Unlock()
}

func (l *Log) Last(role, result string) (Event, bool) {
	events := l.Events()
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Role == role && events[i].Result == result {
			return events[i], true
		}
	}
	return Event{}, false
}

func (Event) Format(state fmt.State, _ rune) { _, _ = fmt.Fprint(state, "isolation.Event{[REDACTED]}") }
func (Event) LogValue() slog.Value           { return slog.StringValue("isolation.Event{[REDACTED]}") }

func RedactLog(line []byte, secrets []string) []byte {
	out := bytes.Clone(line)
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		sum := sha256.Sum224([]byte(secret))
		token := []byte(hex.EncodeToString(sum[:]))
		out = bytes.ReplaceAll(out, []byte(secret), []byte("[REDACTED]"))
		out = bytes.ReplaceAll(out, token, []byte("[REDACTED]"))
	}
	return out
}
