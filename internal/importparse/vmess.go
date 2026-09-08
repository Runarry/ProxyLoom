package importparse

import (
	"bytes"
	"encoding/json"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Runarry/ProxyLoom/internal/ir"
)

func parseVMess(raw string, c *Candidate) (ir.Node, error) {
	body := raw[strings.Index(raw, "://")+3:]
	if strings.ContainsAny(body, "?#") {
		return ir.Node{}, failure(InvalidURI, "")
	}
	data, err := decodeBase64(body, false)
	if err != nil {
		return ir.Node{}, failure(InvalidBase64, "")
	}
	values, err := vmessObject(data)
	if err != nil {
		return ir.Node{}, err
	}
	allowed := fields("v", "ps", "add", "port", "id", "aid", "scy", "net", "type", "host", "path", "tls", "sni", "alpn", "fp", "allowInsecure")
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	unsupported := false
	for _, key := range keys {
		if !allowed[key] {
			c.metadata(key, values[key])
			c.warnMetadata()
			unsupported = unsupported || !metadataKey(key)
		}
	}
	if unsupported {
		return ir.Node{}, failure(Unsupported, "/vmess")
	}
	version, err := vmessScalar(values, "v", true, true)
	if err != nil {
		return ir.Node{}, err
	}
	if version != "2" {
		return ir.Node{}, failure(Unsupported, "/vmess/v")
	}
	name, err := vmessScalar(values, "ps", false, false)
	if err != nil {
		return ir.Node{}, err
	}
	c.Name = name
	host, err := vmessScalar(values, "add", true, false)
	if err != nil {
		return ir.Node{}, err
	}
	host, err = normalizeHost(host)
	if err != nil {
		return ir.Node{}, failure(InvalidValue, "/endpoint/host")
	}
	portText, err := vmessScalar(values, "port", true, true)
	if err != nil {
		return ir.Node{}, err
	}
	port, err := decimalPort(portText)
	if err != nil {
		return ir.Node{}, err
	}
	id, err := vmessScalar(values, "id", true, false)
	if err != nil {
		return ir.Node{}, err
	}
	alterID, err := vmessScalar(values, "aid", true, true)
	if err != nil {
		return ir.Node{}, err
	}
	if alterID != "0" {
		return ir.Node{}, failure(Unsupported, "/vmess/aid")
	}
	cipher, err := vmessScalar(values, "scy", true, false)
	if err != nil {
		return ir.Node{}, err
	}
	switch ir.VMessCipher(cipher) {
	case ir.VMessAuto, ir.VMessAES128GCM, ir.VMessChaCha20Poly1305, ir.VMessNone, ir.VMessZero:
	default:
		return ir.Node{}, failure(Unsupported, "/auth/cipher")
	}
	network, err := vmessScalar(values, "net", true, false)
	if err != nil {
		return ir.Node{}, err
	}
	headerType, err := vmessScalar(values, "type", false, false)
	if err != nil {
		return ir.Node{}, err
	}
	if headerType != "" && headerType != "none" {
		return ir.Node{}, failure(Unsupported, "/vmess/type")
	}
	tlsMode, err := vmessScalar(values, "tls", false, false)
	if err != nil {
		return ir.Node{}, err
	}
	if _, ok := values["tls"]; !ok {
		return ir.Node{}, failure(RequiredField, "/vmess/tls")
	}
	if tlsMode == "" {
		tlsMode = "none"
	}
	q := map[string]string{"type": network, "security": tlsMode}
	for _, key := range []string{"host", "path", "sni", "alpn", "fp"} {
		value, err := vmessScalar(values, key, false, false)
		if err != nil {
			return ir.Node{}, err
		}
		// VMess JSON v2 conventionally emits empty placeholders for inactive
		// optional fields. Nonempty inactive values are rejected by shared IR
		// conversion instead of being silently ignored.
		if value != "" {
			q[key] = value
		}
	}
	if value, ok := values["allowInsecure"]; ok {
		switch string(value) {
		case "true", "1", "false", "0":
			q["allowInsecure"] = string(value)
		default:
			var text string
			if json.Unmarshal(value, &text) != nil || string(value) == "null" {
				return ir.Node{}, failure(InvalidValue, "/security/verify_certificate")
			}
			q["allowInsecure"] = text
		}
	}
	node := newNode(ir.VMess)
	node.Endpoint = ir.Endpoint{Host: host, Port: port}
	node.Auth = &ir.VMessAuth{Kind: ir.AuthVMessAEAD, UUID: ir.Secret(strings.ToLower(id)), Cipher: ir.VMessCipher(cipher)}
	node.Transport, err = parseTransport(q, network)
	if err != nil {
		return ir.Node{}, err
	}
	node.Security, err = parseSecurity(q, tlsMode, host)
	if err != nil {
		return ir.Node{}, err
	}
	return node, nil
}

func vmessScalar(values map[string]json.RawMessage, key string, required, number bool) (string, error) {
	value, exists := values[key]
	if !exists {
		if required {
			return "", failure(RequiredField, "/vmess/"+key)
		}
		return "", nil
	}
	var text string
	if string(value) == "null" {
		return "", failure(InvalidValue, "/vmess/"+key)
	}
	if json.Unmarshal(value, &text) != nil {
		if !number {
			return "", failure(InvalidValue, "/vmess/"+key)
		}
		text = string(value)
		for _, b := range []byte(text) {
			if b < '0' || b > '9' {
				return "", failure(InvalidValue, "/vmess/"+key)
			}
		}
	}
	if required && text == "" {
		return "", failure(RequiredField, "/vmess/"+key)
	}
	return text, nil
}

// Validate all nesting before unmarshalling. Unknown metadata can contain
// nested objects, duplicate keys, invalid surrogate pairs, or expensive depth.
// Their raw keys are never interpolated into diagnostic paths.
func vmessObject(data []byte) (map[string]json.RawMessage, error) {
	if !utf8.Valid(data) || !validSurrogates(data) {
		return nil, failure(InvalidValue, "/vmess")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := scanJSONValue(decoder, 0); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, failure(InvalidValue, "/vmess")
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(data, &values); err != nil || values == nil {
		return nil, failure(InvalidValue, "/vmess")
	}
	return values, nil
}

func scanJSONValue(decoder *json.Decoder, depth int) error {
	if depth > 32 {
		return failure(InvalidValue, "/vmess")
	}
	token, err := decoder.Token()
	if err != nil {
		return failure(InvalidValue, "/vmess")
	}
	delim, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]bool)
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return failure(InvalidValue, "/vmess")
			}
			text, ok := key.(string)
			if !ok {
				return failure(InvalidValue, "/vmess")
			}
			if seen[text] {
				return failure(DuplicateField, "/vmess")
			}
			seen[text] = true
			if err := scanJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		if end, err := decoder.Token(); err != nil || end != json.Delim('}') {
			return failure(InvalidValue, "/vmess")
		}
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		if end, err := decoder.Token(); err != nil || end != json.Delim(']') {
			return failure(InvalidValue, "/vmess")
		}
	default:
		return failure(InvalidValue, "/vmess")
	}
	return nil
}

func validSurrogates(data []byte) bool {
	inString := false
	for i := 0; i < len(data); i++ {
		if data[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || data[i] != '\\' || i+1 >= len(data) {
			continue
		}
		if data[i+1] != 'u' {
			i++
			continue
		}
		if i+6 > len(data) {
			return false
		}
		n, err := strconv.ParseUint(string(data[i+2:i+6]), 16, 16)
		if err != nil || n >= 0xdc00 && n <= 0xdfff {
			return false
		}
		if n >= 0xd800 && n <= 0xdbff {
			if i+12 > len(data) || data[i+6] != '\\' || data[i+7] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(data[i+8:i+12]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
		i += 5
	}
	return true
}
