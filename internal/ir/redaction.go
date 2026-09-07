package ir

import (
	"fmt"
	"log/slog"
)

// Structured loggers often JSON-encode nested values without consulting the
// nested Secret's LogValuer. Redact each public credential-bearing aggregate at
// its logging boundary. Explicit JSON serialization remains the compile wire form.
func redactFormat(state fmt.State) { _, _ = fmt.Fprint(state, "[REDACTED]") }
func redactedValue() slog.Value    { return slog.StringValue("[REDACTED]") }

func (Node) Format(state fmt.State, _ rune)                 { redactFormat(state) }
func (Node) LogValue() slog.Value                           { return redactedValue() }
func (Resource) Format(state fmt.State, _ rune)             { redactFormat(state) }
func (Resource) LogValue() slog.Value                       { return redactedValue() }
func (FrozenInputSpec) Format(state fmt.State, _ rune)      { redactFormat(state) }
func (FrozenInputSpec) LogValue() slog.Value                { return redactedValue() }
func (Endpoint) Format(state fmt.State, _ rune)             { redactFormat(state) }
func (Endpoint) LogValue() slog.Value                       { return redactedValue() }
func (MethodPasswordAuth) Format(state fmt.State, _ rune)   { redactFormat(state) }
func (MethodPasswordAuth) LogValue() slog.Value             { return redactedValue() }
func (VMessAuth) Format(state fmt.State, _ rune)            { redactFormat(state) }
func (VMessAuth) LogValue() slog.Value                      { return redactedValue() }
func (UUIDAuth) Format(state fmt.State, _ rune)             { redactFormat(state) }
func (UUIDAuth) LogValue() slog.Value                       { return redactedValue() }
func (PasswordAuth) Format(state fmt.State, _ rune)         { redactFormat(state) }
func (PasswordAuth) LogValue() slog.Value                   { return redactedValue() }
func (UsernamePasswordAuth) Format(state fmt.State, _ rune) { redactFormat(state) }
func (UsernamePasswordAuth) LogValue() slog.Value           { return redactedValue() }
func (RealitySecurity) Format(state fmt.State, _ rune)      { redactFormat(state) }
func (RealitySecurity) LogValue() slog.Value                { return redactedValue() }
