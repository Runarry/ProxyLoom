package adapter

import (
	"bytes"
	"regexp"
	"slices"
)

var (
	exampleOnly = regexp.MustCompile(`EXAMPLE_ONLY_[A-Za-z0-9_]+`)
	jsonSecret  = regexp.MustCompile(`(?i)("(?:password|passwd|uuid|secret|token|private_key|public_key)"\s*:\s*")[^"]*(")`)
	yamlSecret  = regexp.MustCompile(`(?i)((?:password|passwd|uuid|secret|token|private-key|public-key):\s*)\S+`)
)

// RedactLogLine returns a new buffer. It never mutates the input.
func RedactLogLine(line []byte) []byte {
	if len(line) == 0 {
		return []byte{}
	}
	out := slices.Clone(line)
	out = exampleOnly.ReplaceAll(out, []byte("EXAMPLE_ONLY_[REDACTED]"))
	out = jsonSecret.ReplaceAll(out, []byte(`${1}[REDACTED]$2`))
	out = yamlSecret.ReplaceAll(out, []byte(`${1}[REDACTED]`))
	if bytes.Equal(out, line) {
		return out
	}
	return out
}
