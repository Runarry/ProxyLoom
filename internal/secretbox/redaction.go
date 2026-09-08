package secretbox

import (
	"fmt"
	"log/slog"
)

func redactFormat(state fmt.State) { _, _ = fmt.Fprint(state, "[REDACTED]") }
func redactedValue() slog.Value    { return slog.StringValue("[REDACTED]") }

func (Box) Format(state fmt.State, _ rune)      { redactFormat(state) }
func (Box) LogValue() slog.Value                { return redactedValue() }
func (Context) Format(state fmt.State, _ rune)  { redactFormat(state) }
func (Context) LogValue() slog.Value            { return redactedValue() }
func (Payload) Format(state fmt.State, _ rune)  { redactFormat(state) }
func (Payload) LogValue() slog.Value            { return redactedValue() }
func (Wrapping) Format(state fmt.State, _ rune) { redactFormat(state) }
func (Wrapping) LogValue() slog.Value           { return redactedValue() }
