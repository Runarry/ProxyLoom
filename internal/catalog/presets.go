package catalog

import (
	"crypto/sha256"
	"fmt"

	"github.com/Runarry/ProxyLoom/internal/ir"
)

type ClientPresetListOptions struct {
	CoreFamily ir.CoreFamily
	Platform   string
	After      *Position
	Limit      int
}

type ClientPresetPage = RoutingPage

// BuiltinClientPresets is the immutable P0 catalog. Approval covers the
// structured configuration constraints; it makes no client verification claim.
func BuiltinClientPresets(scope ir.ID) ([]ir.Resource, error) {
	if scope.Validate() != nil {
		return nil, ErrInvalidInput
	}
	definitions := []struct {
		family      ir.CoreFamily
		format      ir.OutputFormat
		name        string
		variant     string
		controlPort int
	}{
		{ir.Xray, ir.XrayJSON, "Xray Linux", "", 0},
		{ir.SingBox, ir.SingBoxJSON, "sing-box Linux", "", 0},
		{ir.Mihomo, ir.MihomoYAML, "Mihomo Linux", "", 0},
		{ir.SingBox, ir.SingBoxJSON, "sing-box Linux Control", "/control", ir.SingBoxControlPort},
		{ir.Mihomo, ir.MihomoYAML, "Mihomo Linux Control", "/control", ir.MihomoControlPort},
	}
	resources := make([]ir.Resource, 0, len(definitions))
	for _, definition := range definitions {
		// Scope participates because catalog resource IDs are globally unique.
		id := sha256.Sum256([]byte("proxyloom/client-preset/v1/" + string(scope) + "/" + string(definition.family) + definition.variant))
		id[6], id[8] = (id[6]&0x0f)|0x50, (id[8]&0x3f)|0x80
		control := ir.ControlAPIPreset{Enabled: false}
		if definition.controlPort != 0 {
			control = ir.ControlAPIPreset{Enabled: true, Listen: ir.PresetLoopbackAddress, Port: definition.controlPort}
		}
		resource := ir.Resource{Metadata: ir.Metadata{
			ResourceID: ir.ID(fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:16])),
			ScopeID:    scope, Kind: ir.KindClientPreset, Revision: 1, SchemaVersion: ir.SchemaVersion,
			Name: definition.name, Tags: []string{}, Enabled: true, SecurityEpoch: 1,
		}, Payload: &ir.ClientPreset{
			SchemaVersion: ir.SchemaVersion, CoreFamily: definition.family, Platform: "linux", Format: definition.format,
			LocalListener: ir.LocalListener{Protocol: "socks5", Listen: ir.PresetLoopbackAddress, Port: ir.PresetSOCKSPort},
			DNSMode:       "profile", ControlAPI: control, ImportMethod: "file",
			ReviewStatus: "approved", ReviewedAt: "2026-09-10T00:00:00Z",
		}}
		if resource.Validate() != nil {
			return nil, ErrInvalidInput
		}
		resources = append(resources, resource)
	}
	return resources, nil
}
