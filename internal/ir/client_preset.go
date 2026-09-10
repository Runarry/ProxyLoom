package ir

import "encoding/json"

const (
	PresetLoopbackAddress = "127.0.0.1"
	PresetSOCKSPort       = 1080
	SingBoxControlPort    = 17812
	MihomoControlPort     = 17813
)

type LocalListener struct {
	Protocol string `json:"protocol"`
	Listen   string `json:"listen"`
	Port     int    `json:"port"`
}

func (v LocalListener) Validate() error { return validateValue(v, "local_listener") }
func (v *LocalListener) UnmarshalJSON(data []byte) error {
	type plain LocalListener
	var next plain
	if err := decodePlain(data, "local_listener", &next); err != nil {
		return err
	}
	*v = LocalListener(next)
	return nil
}

type ControlAPIPreset struct {
	Enabled bool   `json:"enabled"`
	Listen  string `json:"listen,omitempty"`
	Port    int    `json:"port,omitempty"`
}

func (v ControlAPIPreset) Validate() error { return validateValue(v, "control_api_preset") }
func (v *ControlAPIPreset) UnmarshalJSON(data []byte) error {
	type plain ControlAPIPreset
	var next plain
	if err := decodePlain(data, "control_api_preset", &next); err != nil {
		return err
	}
	*v = ControlAPIPreset(next)
	return nil
}

// P0 presets describe reviewed Linux file imports with loopback listeners.
// Runtime selection uses only the explicitly approved family-specific control
// endpoints. ReviewedAt records supplied evidence, never current time.
type ClientPreset struct {
	SchemaVersion int              `json:"schema_version"`
	CoreFamily    CoreFamily       `json:"core_family"`
	Platform      string           `json:"platform"`
	Format        OutputFormat     `json:"format"`
	LocalListener LocalListener    `json:"local_listener"`
	DNSMode       string           `json:"dns_mode"`
	ControlAPI    ControlAPIPreset `json:"control_api"`
	ImportMethod  string           `json:"import_method"`
	ReviewStatus  string           `json:"review_status"`
	ReviewedAt    string           `json:"reviewed_at"`
}

func (*ClientPreset) resourcePayload() {}
func (v ClientPreset) Validate() error {
	if err := validateValue(v, "client_preset"); err != nil {
		return err
	}
	if v.LocalListener.Protocol != "socks5" {
		return Diagnostics{issue(InvalidValue, "/local_listener/protocol")}
	}
	if v.LocalListener.Listen != PresetLoopbackAddress {
		return Diagnostics{issue(InvalidValue, "/local_listener/listen")}
	}
	if v.LocalListener.Port != PresetSOCKSPort {
		return Diagnostics{issue(InvalidValue, "/local_listener/port")}
	}
	if !v.ControlAPI.Enabled {
		return nil
	}
	var controlPort int
	switch v.CoreFamily {
	case SingBox:
		controlPort = SingBoxControlPort
	case Mihomo:
		controlPort = MihomoControlPort
	default:
		return Diagnostics{issue(InvalidValue, "/control_api/enabled")}
	}
	if v.ControlAPI.Port != controlPort {
		return Diagnostics{issue(InvalidValue, "/control_api/port")}
	}
	return nil
}
func (v *ClientPreset) UnmarshalJSON(data []byte) error {
	type plain ClientPreset
	var next plain
	if err := decodePlain(data, "client_preset", &next); err != nil {
		return err
	}
	candidate := ClientPreset(next)
	if err := candidate.Validate(); err != nil {
		return err
	}
	*v = candidate
	return nil
}
func DecodeClientPreset(data []byte) (ClientPreset, error) {
	var value ClientPreset
	err := json.Unmarshal(data, &value)
	return value, safeDecodeError(err)
}
func (v ClientPreset) Clone() ClientPreset { return v }
