package identity

import (
	"fmt"
	"log/slog"
)

func (Options) Format(state fmt.State, _ rune) { _, _ = fmt.Fprint(state, "[REDACTED]") }
func (Options) LogValue() slog.Value           { return slog.StringValue("[REDACTED]") }
func (Session) Format(state fmt.State, _ rune) { _, _ = fmt.Fprint(state, "[REDACTED]") }
func (Session) LogValue() slog.Value           { return slog.StringValue("[REDACTED]") }
