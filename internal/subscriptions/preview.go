package subscriptions

import (
	"github.com/Runarry/ProxyLoom/internal/ir"
	"unicode/utf8"
)

// Native display snippets are bounded independently of the complete immutable
// artifact. The confirmation hash and change flag still cover all bytes.
func Preview(value string) (string, bool) {
	const limit = 32 << 10
	if len(value) <= limit {
		return value, false
	}
	end := limit
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	return value[:end], true
}
func Diagnostics(value []ir.Diagnostic, limit int) []ir.Diagnostic {
	if len(value) <= limit {
		return value
	}
	out := append([]ir.Diagnostic{}, value[:limit-1]...)
	return append(out, ir.Diagnostic{Code: ir.InputLimitExceeded, Severity: ir.SeverityWarning, FieldPath: "/diagnostics", Message: "Additional diagnostics are omitted from this bounded response."})
}
