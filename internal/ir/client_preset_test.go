package ir_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Runarry/ProxyLoom/api"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

func controlledPreset(family ir.CoreFamily) ir.ClientPreset {
	preset := presetFixture()
	preset.CoreFamily = family
	preset.ControlAPI = ir.ControlAPIPreset{Enabled: true, Listen: ir.PresetLoopbackAddress}
	switch family {
	case ir.SingBox:
		preset.Format, preset.ControlAPI.Port = ir.SingBoxJSON, ir.SingBoxControlPort
	case ir.Mihomo:
		preset.Format, preset.ControlAPI.Port = ir.MihomoYAML, ir.MihomoControlPort
	}
	return preset
}

func TestReviewedControlPresetsUseExactFamilyEndpoints(t *testing.T) {
	for _, family := range []ir.CoreFamily{ir.SingBox, ir.Mihomo} {
		t.Run(string(family), func(t *testing.T) {
			preset := controlledPreset(family)
			if err := preset.Validate(); err != nil {
				t.Fatal(err)
			}
			wire := mustJSON(t, preset)
			decoded, err := ir.DecodeClientPreset(wire)
			if err != nil || !bytes.Equal(wire, mustJSON(t, decoded)) {
				t.Fatalf("approved control preset did not roundtrip: %v", err)
			}
			if err := api.Validate("ClientPreset", parseValue(t, wire)); err != nil {
				t.Fatal("approved variant differs from the existing API contract")
			}
			if err := decoded.ControlAPI.Validate(); err != nil {
				t.Fatal(err)
			}
		})
	}
	for name, mutate := range map[string]func(*ir.ClientPreset){
		"public_control":       func(v *ir.ClientPreset) { v.ControlAPI.Listen = "0.0.0.0" },
		"different_loopback":   func(v *ir.ClientPreset) { v.ControlAPI.Listen = "::1" },
		"wrong_control_port":   func(v *ir.ClientPreset) { v.ControlAPI.Port = 9090 },
		"other_family_port":    func(v *ir.ClientPreset) { v.ControlAPI.Port = ir.MihomoControlPort },
		"missing_control_host": func(v *ir.ClientPreset) { v.ControlAPI.Listen = "" },
		"missing_control_port": func(v *ir.ClientPreset) { v.ControlAPI.Port = 0 },
		"http_data_listener":   func(v *ir.ClientPreset) { v.LocalListener.Protocol = "http" },
		"mixed_data_listener":  func(v *ir.ClientPreset) { v.LocalListener.Protocol = "mixed" },
		"different_data_host":  func(v *ir.ClientPreset) { v.LocalListener.Listen = "::1" },
		"different_data_port":  func(v *ir.ClientPreset) { v.LocalListener.Port = 1081 },
		"xray_unreviewed": func(v *ir.ClientPreset) {
			v.CoreFamily, v.Format = ir.Xray, ir.XrayJSON
		},
	} {
		t.Run(name, func(t *testing.T) {
			preset := controlledPreset(ir.SingBox)
			mutate(&preset)
			if preset.Validate() == nil {
				t.Fatal("unreviewed control variant accepted")
			}
			if _, err := ir.DecodeClientPreset(mustJSON(t, preset)); err == nil {
				t.Fatal("unreviewed control variant accepted at JSON boundary")
			}
		})
	}
	wrongFamily := controlledPreset(ir.SingBox)
	wrongFamily.ControlAPI.Port = ir.MihomoControlPort
	_, err := ir.DecodeClientPreset(mustJSON(t, wrongFamily))
	hasDiagnostic(t, err, ir.InvalidValue, "/control_api/port")
}

func TestReviewedControlPresetRejectsMixedNativeFields(t *testing.T) {
	preset := controlledPreset(ir.SingBox)
	valid := mustJSON(t, preset)
	for _, fields := range []string{
		`"enabled":true,"listen":"127.0.0.1","port":17812,"secret":"SYNTHETIC_SECRET"`,
		`"enabled":true,"listen":"127.0.0.1","port":17812,"external_ui":"SYNTHETIC_SECRET"`,
		`"enabled":true,"listen":"127.0.0.1","port":17812,"native":{}`,
		`"enabled":true,"listen":"127.0.0.1","port":17812,"enabled":false`,
	} {
		var wire map[string]json.RawMessage
		if err := json.Unmarshal(valid, &wire); err != nil {
			t.Fatal(err)
		}
		wire["control_api"] = json.RawMessage("{" + fields + "}")
		err := json.Unmarshal(mustJSON(t, wire), &preset)
		if err == nil || strings.Contains(err.Error(), "SYNTHETIC_SECRET") || !bytes.Equal(valid, mustJSON(t, preset)) {
			t.Fatal("native fields, duplicate discriminator, redaction or receiver isolation failed")
		}
	}
}

func TestDisabledControlPresetsStillRequireReviewedListener(t *testing.T) {
	for name, mutate := range map[string]func(*ir.ClientPreset){
		"http":                   func(v *ir.ClientPreset) { v.LocalListener.Protocol = "http" },
		"mixed":                  func(v *ir.ClientPreset) { v.LocalListener.Protocol = "mixed" },
		"ipv6":                   func(v *ir.ClientPreset) { v.LocalListener.Listen = "::1" },
		"port":                   func(v *ir.ClientPreset) { v.LocalListener.Port = 1081 },
		"ignored_control_listen": func(v *ir.ClientPreset) { v.ControlAPI.Listen = "127.0.0.1" },
		"ignored_control_port":   func(v *ir.ClientPreset) { v.ControlAPI.Port = ir.SingBoxControlPort },
	} {
		t.Run(name, func(t *testing.T) {
			preset := presetFixture()
			mutate(&preset)
			wire := mustJSON(t, preset)
			if preset.Validate() == nil {
				t.Fatal("disabled control bypassed reviewed preset constraints")
			}
			if _, err := ir.DecodeClientPreset(wire); err == nil {
				t.Fatal("JSON preset bypassed reviewed listener")
			}
			if err := api.Validate("ClientPreset", parseValue(t, wire)); err == nil {
				t.Fatal("API schema accepted an unreviewed preset")
			}
		})
	}
}

func TestFrozenReviewedControlPresetsKeepIndependentValues(t *testing.T) {
	spec := orchestrationSpec(t)
	for i, resource := range spec.Resources {
		if preset, ok := resource.Payload.(*ir.ClientPreset); ok && preset.CoreFamily != ir.Xray {
			next := controlledPreset(preset.CoreFamily)
			spec.Resources[i].Payload = &next
		}
	}
	frozen, err := ir.NewFrozenInput(spec)
	if err != nil {
		t.Fatal(err)
	}
	before := mustJSON(t, frozen)
	if _, err := ir.DecodeFrozenInput(before); err != nil {
		t.Fatal(err)
	}
	mutate := func(resources []ir.Resource) {
		for _, resource := range resources {
			if preset, ok := resource.Payload.(*ir.ClientPreset); ok && preset.ControlAPI.Enabled {
				preset.ControlAPI.Port = 9090
				preset.ControlAPI.Listen = "0.0.0.0"
				preset.LocalListener.Port = 1081
			}
		}
	}
	mutate(spec.Resources)
	copy := frozen.Spec()
	mutate(copy.Resources)
	if frozen.Validate() != nil || !bytes.Equal(before, mustJSON(t, frozen)) {
		t.Fatal("controlled preset values leaked a mutable frozen alias")
	}
}
