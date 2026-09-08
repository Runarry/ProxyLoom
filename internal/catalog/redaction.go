package catalog

import (
	"fmt"
	"log/slog"
)

// Explicit JSON is available for trusted storage, while ordinary formatting
// and structured logging redact entire aggregates, including untrusted names.
func redact(state fmt.State)                              { _, _ = fmt.Fprint(state, "[REDACTED]") }
func (CreateInput) Format(state fmt.State, _ rune)        { redact(state) }
func (UpdateInput) Format(state fmt.State, _ rune)        { redact(state) }
func (IdempotencyRequest) Format(state fmt.State, _ rune) { redact(state) }
func (CreateInput) LogValue() slog.Value                  { return slog.StringValue("[REDACTED]") }
func (UpdateInput) LogValue() slog.Value                  { return slog.StringValue("[REDACTED]") }
func (IdempotencyRequest) LogValue() slog.Value           { return slog.StringValue("[REDACTED]") }
