package compiler

import (
	"encoding/json"
	"fmt"
	"github.com/Runarry/ProxyLoom/internal/adapter"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"gopkg.in/yaml.v3"
	"slices"
	"strconv"
	"strings"
)

// CheckPublication validates the final document after all target mapping. Its
// preset is frozen; no mutable registry or current time participates.
func CheckPublication(data []byte, target ir.Target, preset ir.ClientPreset) error {
	fail := func(path string) error {
		return ir.Diagnostics{compileIssue(ir.CompileUnmappedField, path, target.Key, target.ClientPresetID)}
	}
	if len(data) == 0 || len(data) > adapter.MaxArtifactBytes || preset.Validate() != nil {
		return fail("/artifact")
	}
	doc, err := nativeDocument(data, target.Format)
	if err != nil {
		return fail("/artifact")
	}
	allowed := map[ir.CoreFamily][]string{
		ir.Xray:    {"log", "inbounds", "outbounds", "routing", "dns", "observatory"},
		ir.SingBox: {"log", "inbounds", "outbounds", "route", "dns", "experimental"},
		ir.Mihomo:  {"socks-port", "bind-address", "allow-lan", "mode", "log-level", "external-controller", "external-controller-cors", "ipv6", "geodata-mode", "geo-auto-update", "find-process-mode", "proxies", "rules", "proxy-groups", "dns", "rule-providers"},
	}
	for key := range doc {
		if !slices.Contains(allowed[target.CoreFamily], key) {
			return fail("/artifact/" + key)
		}
	}
	// Reserved names must retain the compiler's built-in meaning after native
	// mapping; they cannot be repurposed as a user proxy or policy group.
	for _, field := range []string{"outbounds", "proxies", "proxy-groups"} {
		items, _ := doc[field].([]any)
		seen := map[string]bool{}
		for i, item := range items {
			entry, ok := item.(map[string]any)
			if !ok {
				return fail("/artifact/" + field)
			}
			nameKey, kindKey := "tag", "type"
			if target.CoreFamily == ir.Mihomo {
				nameKey = "name"
			} else if target.CoreFamily == ir.Xray {
				kindKey = "protocol"
			}
			name, _ := entry[nameKey].(string)
			path := "/artifact/" + field + "/" + strconv.Itoa(i) + "/" + nameKey
			if name == "" || seen[name] {
				return fail(path)
			}
			seen[name] = true
			if adapter.IsReservedTag(name) {
				builtin := field == "outbounds" && ((target.CoreFamily == ir.Xray && ((name == "block" && entry[kindKey] == "blackhole") || (name == "direct" && entry[kindKey] == "freedom"))) || (target.CoreFamily == ir.SingBox && name == "direct" && entry[kindKey] == "direct"))
				if !builtin {
					return fail(path)
				}
			}
		}
	}
	if target.CoreFamily == ir.Mihomo {
		if doc["bind-address"] != "127.0.0.1" || doc["allow-lan"] != false || nativeInt(doc["socks-port"]) != preset.LocalListener.Port || doc["geo-auto-update"] != false || doc["find-process-mode"] != "off" {
			return fail("/artifact/listener")
		}
		want := ""
		if preset.ControlAPI.Enabled {
			want = "127.0.0.1:" + strconv.Itoa(preset.ControlAPI.Port)
		}
		if doc["external-controller"] != want {
			return fail("/artifact/external-controller")
		}
	} else {
		inbounds, ok := doc["inbounds"].([]any)
		if !ok || len(inbounds) != 1 {
			return fail("/artifact/inbounds")
		}
		in, ok := inbounds[0].(map[string]any)
		if !ok || in["listen"] != "127.0.0.1" || in["tag"] != adapter.InboundTag {
			return fail("/artifact/inbounds/0/listen")
		}
		portKey, kindKey, kind := "port", "protocol", "socks"
		if target.CoreFamily == ir.SingBox {
			portKey, kindKey, kind = "listen_port", "type", "socks"
		}
		if nativeInt(in[portKey]) != preset.LocalListener.Port || in[kindKey] != kind {
			return fail("/artifact/inbounds/0")
		}
		experimental, present := doc["experimental"]
		if target.CoreFamily == ir.SingBox {
			if present != preset.ControlAPI.Enabled {
				return fail("/artifact/experimental")
			}
			if present {
				ex, ok := experimental.(map[string]any)
				if !ok || len(ex) != 1 {
					return fail("/artifact/experimental")
				}
				ca, ok := ex["clash_api"].(map[string]any)
				if !ok || ca["external_controller"] != "127.0.0.1:"+strconv.Itoa(preset.ControlAPI.Port) {
					return fail("/artifact/experimental/clash_api")
				}
			}
		}
	}
	var walk func(any, string) error
	walk = func(value any, path string) error {
		switch v := value.(type) {
		case map[string]any:
			keys := make([]string, 0, len(v))
			for key := range v {
				keys = append(keys, key)
			}
			slices.Sort(keys)
			for _, key := range keys {
				child := path + "/" + key
				switch strings.ToLower(strings.ReplaceAll(key, "_", "-")) {
				case "tun", "script", "scripts", "plugin", "plugins", "external-ui", "external-ui-url", "external-ui-name", "cache-file", "certificate-path", "key-path", "certificatepath", "keypath", "rule-set-path", "auto-route", "auto-detect-interface", "interface-name", "bind-interface", "download-detour", "geoip", "geosite":
					return fail(child)
				case "insecure", "allowinsecure", "skip-cert-verify", "access-control-allow-private-network":
					if v[key] != false {
						return fail(child)
					}
				case "path":
					if !strings.Contains(path, "/transport") && !strings.Contains(path, "/wsSettings") && !strings.Contains(path, "/ws-opts") {
						return fail(child)
					}
				case "output", "access", "error":
					if strings.HasPrefix(path, "/artifact/log") {
						return fail(child)
					}
				case "type":
					if (strings.Contains(path, "rule-providers") || strings.Contains(path, "rule_set")) && v[key] != "inline" {
						return fail(child)
					}
				case "listen":
					if v[key] != "127.0.0.1" {
						return fail(child)
					}
				}
				if err := walk(v[key], child); err != nil {
					return err
				}
			}
		case []any:
			for i, item := range v {
				if err := walk(item, path+"/"+strconv.Itoa(i)); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(doc, "/artifact")
}
func nativeInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case json.Number:
		i, _ := strconv.Atoi(string(n))
		return i
	}
	return -1
}
func nativeDocument(data []byte, format ir.OutputFormat) (map[string]any, error) {
	var doc map[string]any
	var err error
	if format == ir.MihomoYAML {
		err = yaml.Unmarshal(data, &doc)
	} else {
		err = json.Unmarshal(data, &doc)
	}
	if err != nil || doc == nil {
		return nil, fmt.Errorf("invalid_native_document")
	}
	return doc, nil
}

// RedactedNative removes structural credential fields, including less obvious
// VMess IDs, REALITY material, user names and WebSocket headers/paths. It is a
// display artifact only; it must never be used as the validated output.
func RedactedNative(data []byte, format ir.OutputFormat) (string, error) {
	doc, err := nativeDocument(data, format)
	if err != nil {
		return "", err
	}
	var walk func(any)
	walk = func(v any) {
		switch value := v.(type) {
		case map[string]any:
			for key, child := range value {
				normal := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(key))
				switch normal {
				case "password", "passwd", "uuid", "id", "username", "user", "secret", "token", "publickey", "privatekey", "shortid", "path", "headers":
					value[key] = "[REDACTED]"
				default:
					walk(child)
				}
			}
		case []any:
			for _, child := range value {
				walk(child)
			}
		}
	}
	walk(doc)
	if format == ir.MihomoYAML {
		out, err := yaml.Marshal(doc)
		return string(out), err
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	return string(append(out, '\n')), err
}

// PublicationEvidence keeps the broad candidate catalog unverified. This
// narrower eligibility requires the reviewed M1 mapping plus per-output native
// validation. The archived suite covers linux/amd64, not client import/arm64.
func (c *Compiler) PublicationEvidence(input ir.FrozenInput, target ir.Target) error {
	graph, diags, err := c.Prepare(input, target)
	if err != nil {
		return err
	}
	if graph.Build.Arch != "amd64" || graph.Build.OS != "linux" || graph.Preset == nil || graph.Routing == nil || graph.DNS == nil {
		return ir.Diagnostics{compileIssue(ir.CapabilityUnverified, "/target", target.Key, target.ClientPresetID)}
	}
	if err := matchPublicationEvidence(graph); err != nil {
		return err
	}
	_ = diags
	return nil
}
