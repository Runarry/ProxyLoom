package importparse

import (
	"bytes"
	"context"
	"encoding/base64"
	"strings"
	"unicode/utf8"
)

// Parse preserves order and decoded physical line numbers. Batch failures
// return no partial result. Individual URI failures remain visible candidates.
// Text format disables envelope detection; base64 requires at least one layer.
func Parse(ctx context.Context, format string, input []byte) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if format == "" {
		format = FormatAuto
	}
	if format != FormatAuto && format != FormatText && format != FormatBase64 {
		return Result{}, failure(InvalidFormat, "/format")
	}
	if len(input) > MaxInputBytes {
		return Result{}, failure(InputTooLarge, "")
	}
	decodedSize := len(input)
	data := bytes.TrimPrefix(input, []byte{0xef, 0xbb, 0xbf})
	if len(bytes.TrimSpace(data)) == 0 {
		return Result{}, failure(EmptyInput, "")
	}
	if compressed(data) {
		return Result{}, failure(NativeConfig, "")
	}
	if !utf8.Valid(data) {
		return Result{}, failure(InvalidUTF8, "")
	}
	layers := 0
	if format != FormatText {
		for {
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
			if nativeDocument(data) {
				return Result{}, failure(NativeConfig, "")
			}
			if (format != FormatBase64 || layers > 0) && containsURI(data) {
				break
			}
			if layers == MaxBase64Layers {
				return Result{}, failure(TooManyLayers, "")
			}
			decoded, err := decodeBase64(string(data), true)
			if err != nil {
				return Result{}, failure(InvalidBase64, "")
			}
			layers++
			decodedSize = len(decoded)
			data = bytes.TrimPrefix(decoded, []byte{0xef, 0xbb, 0xbf})
			if compressed(data) {
				return Result{}, failure(NativeConfig, "")
			}
			if !utf8.Valid(data) {
				return Result{}, failure(InvalidUTF8, "")
			}
			if len(bytes.TrimSpace(data)) == 0 {
				return Result{}, failure(EmptyInput, "")
			}
		}
	}
	if decodedSize > MaxDecodedBytes {
		return Result{}, failure(DecodedTooLarge, "")
	}
	if nativeDocument(data) {
		return Result{}, failure(NativeConfig, "")
	}
	// Counting precedes URI parsing; an oversized list never gives a truncated
	// preview. Accept CRLF and CR alongside LF without changing entry order.
	text := strings.ReplaceAll(strings.ReplaceAll(string(data), "\r\n", "\n"), "\r", "\n")
	count := 0
	for line := range strings.SplitSeq(text, "\n") {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		if strings.TrimSpace(line) != "" {
			count++
			if count > MaxEntries {
				return Result{}, failure(TooManyEntries, "")
			}
		}
	}
	if count == 0 {
		return Result{}, failure(EmptyInput, "")
	}
	result := Result{Format: FormatText, Base64Layers: layers, Candidates: make([]Candidate, 0, count)}
	if layers > 0 {
		result.Format = FormatBase64
	}
	line := 0
	for raw := range strings.SplitSeq(text, "\n") {
		line++
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		candidate := ParseURI(raw)
		candidate.Index, candidate.Line = len(result.Candidates), line
		result.Candidates = append(result.Candidates, candidate)
	}
	return result, nil
}

func decodeBase64(raw string, whitespace bool) ([]byte, error) {
	if whitespace {
		raw = strings.Map(func(r rune) rune {
			switch r {
			case ' ', '\t', '\r', '\n':
				return -1
			default:
				return r
			}
		}, raw)
	}
	if raw == "" || strings.ContainsAny(raw, "\r\n \t") {
		return nil, failure(InvalidBase64, "")
	}
	encoding := base64.StdEncoding
	if strings.ContainsAny(raw, "-_") {
		encoding = base64.URLEncoding
	}
	if !strings.ContainsRune(raw, '=') {
		encoding = encoding.WithPadding(base64.NoPadding)
	}
	decoded, err := encoding.Strict().DecodeString(raw)
	if err == nil {
		return decoded, nil
	}
	return nil, failure(InvalidBase64, "")
}

func containsURI(data []byte) bool {
	for line := range bytes.SplitSeq(data, []byte{'\n'}) {
		if _, ok := uriScheme(strings.TrimSpace(string(line))); ok {
			return true
		}
	}
	return false
}

func nativeDocument(data []byte) bool {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return false
	}
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") || strings.HasPrefix(trimmed, "---") || strings.HasPrefix(trimmed, "%YAML") {
		return true
	}
	for line := range strings.SplitSeq(trimmed, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if _, ok := uriScheme(line); ok {
			return false
		}
		if strings.HasPrefix(line, "- ") {
			return true
		}
		if at := strings.IndexByte(line, ':'); at >= 0 && (at == len(line)-1 || line[at+1] == ' ' || line[at+1] == '\t') {
			return true
		}
		return false
	}
	return false
}

func compressed(data []byte) bool {
	return bytes.HasPrefix(data, []byte{0x1f, 0x8b}) || bytes.HasPrefix(data, []byte{'P', 'K', 3, 4}) || bytes.HasPrefix(data, []byte{0xfd, '7', 'z', 'X', 'Z', 0})
}

func uriScheme(raw string) (string, bool) {
	at := strings.Index(raw, "://")
	if at <= 0 {
		return "", false
	}
	for i, b := range []byte(raw[:at]) {
		if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || i > 0 && (b >= '0' && b <= '9' || b == '+' || b == '-' || b == '.') {
			continue
		}
		return "", false
	}
	return strings.ToLower(raw[:at]), true
}
