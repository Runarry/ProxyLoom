package apicontract

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"unicode/utf8"

	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

// SecretPatch preserves absent/null/string without allowing masked responses to
// become credentials. Only Apply may turn its value into the internal IR.
type SecretPatch struct {
	present bool
	clear   bool
	value   ir.Secret
}

func ReplaceSecret(value string) SecretPatch {
	return SecretPatch{present: true, value: ir.Secret(value)}
}
func ClearSecret() SecretPatch      { return SecretPatch{present: true, clear: true} }
func (s SecretPatch) IsZero() bool  { return !s.present }
func (s SecretPatch) Present() bool { return s.present }
func (s SecretPatch) IsNull() bool  { return s.present && s.clear }
func (s SecretPatch) MarshalJSON() ([]byte, error) {
	if !s.present || s.clear {
		return []byte("null"), nil
	}
	if !utf8.ValidString(string(s.value)) {
		return nil, NewError(MalformedRequest)
	}
	return json.Marshal(string(s.value))
}
func (s *SecretPatch) UnmarshalJSON(data []byte) error {
	value, err := parseJSON(data)
	if err != nil {
		return err
	}
	if value == nil {
		*s = ClearSecret()
		return nil
	}
	text, ok := value.(string)
	if !ok {
		return NewError(MalformedRequest)
	}
	*s = ReplaceSecret(text)
	return nil
}
func (s SecretPatch) apply(old ir.Secret) ir.Secret {
	if !s.present {
		return old
	}
	if s.clear {
		return ""
	}
	return s.value
}
func (SecretPatch) Format(state fmt.State, _ rune) { _, _ = fmt.Fprint(state, "[REDACTED]") }
func (SecretPatch) LogValue() slog.Value           { return slog.StringValue("[REDACTED]") }

// Request DTOs deliberately have concrete whitelisted fields. OpenAPI validates
// discriminated branches before conversion into ir.Authentication/ir.Security.
type AuthInput struct {
	Kind     ir.AuthKind           `json:"kind"`
	Method   *ir.ShadowsocksMethod `json:"method,omitempty"`
	Cipher   *ir.VMessCipher       `json:"cipher,omitempty"`
	Password *ir.Secret            `json:"password,omitempty"`
	UUID     *ir.Secret            `json:"uuid,omitempty"`
	Username *ir.Secret            `json:"username,omitempty"`
}

type Transport struct {
	Kind ir.TransportKind `json:"kind"`
	Path *string          `json:"path,omitempty"`
	Host *string          `json:"host,omitempty"`
}

type SecurityInput struct {
	Mode              ir.SecurityMode `json:"mode"`
	ServerName        *string         `json:"server_name,omitempty"`
	VerifyCertificate *bool           `json:"verify_certificate,omitempty"`
	ALPN              []string        `json:"alpn,omitempty"`
	ClientFingerprint *string         `json:"client_fingerprint,omitempty"`
	PublicKey         *ir.Secret      `json:"public_key,omitempty"`
	ShortID           *ir.Secret      `json:"short_id,omitempty"`
}

type NodeInput struct {
	SchemaVersion int           `json:"schema_version"`
	Protocol      ir.Protocol   `json:"protocol"`
	Endpoint      ir.Endpoint   `json:"endpoint"`
	Auth          AuthInput     `json:"auth"`
	Transport     Transport     `json:"transport"`
	Security      SecurityInput `json:"security"`
	Features      ir.Features   `json:"features"`
	Extensions    ir.Extensions `json:"extensions"`
	Origin        *ir.Origin    `json:"origin,omitempty"`
}

type NodeCreateRequest struct {
	Name    string    `json:"name"`
	Tags    []string  `json:"tags,omitempty"`
	Enabled *bool     `json:"enabled,omitempty"`
	Node    NodeInput `json:"node"`
}

func (request NodeCreateRequest) Input() (catalog.CreateInput, error) {
	if err := ValidateDTO("NodeCreateRequest", request); err != nil {
		return catalog.CreateInput{}, err
	}
	encoded, err := json.Marshal(request.Node)
	if err != nil {
		return catalog.CreateInput{}, NewError(ValidationFailed)
	}
	node, err := ir.DecodeNode(encoded)
	if err != nil {
		return catalog.CreateInput{}, AsError(err)
	}
	tags := slices.Clone(request.Tags)
	if tags == nil {
		tags = []string{}
	}
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	return catalog.CreateInput{Name: request.Name, Tags: tags, Enabled: enabled, Payload: &node}, nil
}

type AuthPatch struct {
	Kind     ir.AuthKind           `json:"kind"`
	Method   *ir.ShadowsocksMethod `json:"method,omitempty"`
	Cipher   *ir.VMessCipher       `json:"cipher,omitempty"`
	Password SecretPatch           `json:"password,omitzero"`
	UUID     SecretPatch           `json:"uuid,omitzero"`
	Username SecretPatch           `json:"username,omitzero"`
}

type SecurityPatch struct {
	Mode              ir.SecurityMode `json:"mode"`
	ServerName        *string         `json:"server_name,omitempty"`
	VerifyCertificate *bool           `json:"verify_certificate,omitempty"`
	ALPN              *[]string       `json:"alpn,omitempty"`
	ClientFingerprint *string         `json:"client_fingerprint,omitempty"`
	PublicKey         SecretPatch     `json:"public_key,omitzero"`
	ShortID           SecretPatch     `json:"short_id,omitzero"`
}

type NodePatch struct {
	Protocol  *ir.Protocol   `json:"protocol,omitempty"`
	Endpoint  *ir.Endpoint   `json:"endpoint,omitempty"`
	Auth      *AuthPatch     `json:"auth,omitempty"`
	Transport *Transport     `json:"transport,omitempty"`
	Security  *SecurityPatch `json:"security,omitempty"`
	Features  *ir.Features   `json:"features,omitempty"`
}

type NodePatchRequest struct {
	Name    *string    `json:"name,omitempty"`
	Tags    *[]string  `json:"tags,omitempty"`
	Enabled *bool      `json:"enabled,omitempty"`
	Node    *NodePatch `json:"node,omitempty"`
}

func (request NodePatchRequest) Merge(old ir.Resource) (catalog.UpdateInput, error) {
	if err := ValidateDTO("NodePatchRequest", request); err != nil {
		return catalog.UpdateInput{}, err
	}
	if old.Validate() != nil {
		return catalog.UpdateInput{}, NewError(InternalError)
	}
	node, ok := old.Payload.(*ir.Node)
	if !ok {
		return catalog.UpdateInput{}, NewError(ValidationFailed)
	}
	next, err := cloneNode(*node)
	if err != nil {
		return catalog.UpdateInput{}, err
	}
	if request.Node != nil {
		next, err = request.Node.Apply(next)
		if err != nil {
			return catalog.UpdateInput{}, err
		}
	}
	input := catalog.UpdateInput{Name: old.Metadata.Name, Tags: slices.Clone(old.Metadata.Tags), Enabled: old.Metadata.Enabled, Payload: &next}
	if request.Name != nil {
		input.Name = *request.Name
	}
	if request.Tags != nil {
		input.Tags = slices.Clone(*request.Tags)
	}
	if request.Enabled != nil {
		input.Enabled = *request.Enabled
	}
	return input, nil
}

func cloneNode(node ir.Node) (ir.Node, error) {
	if node.Validate() != nil {
		return ir.Node{}, NewError(InternalError)
	}
	data, err := json.Marshal(node)
	if err != nil {
		return ir.Node{}, NewError(InternalError)
	}
	copy, err := ir.DecodeNode(data)
	if err != nil {
		return ir.Node{}, NewError(InternalError)
	}
	return copy, nil
}

func (patch NodePatch) Apply(old ir.Node) (ir.Node, error) {
	if err := ValidateDTO("NodePatch", patch); err != nil {
		return ir.Node{}, err
	}
	next, err := cloneNode(old)
	if err != nil {
		return ir.Node{}, err
	}
	if patch.Protocol != nil {
		next.Protocol = *patch.Protocol
	}
	if patch.Endpoint != nil {
		next.Endpoint = *patch.Endpoint
	}
	if patch.Auth != nil {
		next.Auth = patch.Auth.apply(next.Auth)
	}
	if patch.Transport != nil {
		switch patch.Transport.Kind {
		case ir.NativeTCP:
			next.Transport = &ir.NativeTCPTransport{Kind: ir.NativeTCP}
		case ir.WebSocket:
			path := ""
			if patch.Transport.Path != nil {
				path = *patch.Transport.Path
			}
			next.Transport = &ir.WebSocketTransport{Kind: ir.WebSocket, Path: path, Host: clonePointer(patch.Transport.Host)}
		default:
			return ir.Node{}, NewError(ValidationFailed)
		}
	}
	if patch.Security != nil {
		// REALITY requires an explicit short_id, while the empty string is a
		// valid explicit IR value. Clearing the field is distinct from replacing
		// it with that permitted empty value.
		if patch.Security.Mode == ir.Reality && patch.Security.ShortID.IsNull() {
			return ir.Node{}, NewError(ValidationFailed, Detail{FieldPath: "/security/short_id"})
		}
		next.Security = patch.Security.apply(next.Security)
	}
	if patch.Features != nil {
		next.Features = ir.Features{UDP: clonePointer(patch.Features.UDP), Multiplex: clonePointer(patch.Features.Multiplex), ProtocolVariant: clonePointer(patch.Features.ProtocolVariant)}
	}
	if err := next.Validate(); err != nil {
		return ir.Node{}, AsError(err)
	}
	return next, nil
}

func (patch AuthPatch) apply(old ir.Authentication) ir.Authentication {
	switch patch.Kind {
	case ir.AuthNone:
		return &ir.NoAuth{Kind: ir.AuthNone}
	case ir.AuthPassword:
		next := ir.PasswordAuth{Kind: patch.Kind}
		if prior, ok := old.(*ir.PasswordAuth); ok && prior != nil {
			next = *prior
		}
		next.Password = patch.Password.apply(next.Password)
		return &next
	case ir.AuthUUID:
		next := ir.UUIDAuth{Kind: patch.Kind}
		if prior, ok := old.(*ir.UUIDAuth); ok && prior != nil {
			next = *prior
		}
		next.UUID = patch.UUID.apply(next.UUID)
		return &next
	case ir.AuthVMessAEAD:
		next := ir.VMessAuth{Kind: patch.Kind}
		if prior, ok := old.(*ir.VMessAuth); ok && prior != nil {
			next = *prior
		}
		next.UUID = patch.UUID.apply(next.UUID)
		if patch.Cipher != nil {
			next.Cipher = *patch.Cipher
		}
		return &next
	case ir.AuthMethodPassword:
		next := ir.MethodPasswordAuth{Kind: patch.Kind}
		if prior, ok := old.(*ir.MethodPasswordAuth); ok && prior != nil {
			next = *prior
		}
		next.Password = patch.Password.apply(next.Password)
		if patch.Method != nil {
			next.Method = *patch.Method
		}
		return &next
	case ir.AuthUsernamePassword:
		next := ir.UsernamePasswordAuth{Kind: patch.Kind}
		if prior, ok := old.(*ir.UsernamePasswordAuth); ok && prior != nil {
			next = *prior
		}
		next.Username = patch.Username.apply(next.Username)
		next.Password = patch.Password.apply(next.Password)
		return &next
	default:
		return nil
	}
}

func (patch SecurityPatch) apply(old ir.Security) ir.Security {
	switch patch.Mode {
	case ir.SecurityNone:
		return &ir.NoSecurity{Mode: ir.SecurityNone}
	case ir.TLS:
		next := ir.TLSSecurity{Mode: ir.TLS}
		if prior, ok := old.(*ir.TLSSecurity); ok && prior != nil {
			next = *prior
		}
		if patch.ServerName != nil {
			next.ServerName = *patch.ServerName
		}
		if patch.VerifyCertificate != nil {
			next.VerifyCertificate = clonePointer(patch.VerifyCertificate)
		}
		if patch.ALPN != nil {
			next.ALPN = slices.Clone(*patch.ALPN)
		}
		if patch.ClientFingerprint != nil {
			next.ClientFingerprint = clonePointer(patch.ClientFingerprint)
		}
		return &next
	case ir.Reality:
		next := ir.RealitySecurity{Mode: ir.Reality}
		if prior, ok := old.(*ir.RealitySecurity); ok && prior != nil {
			next = *prior
		}
		if patch.ServerName != nil {
			next.ServerName = *patch.ServerName
		}
		if patch.ALPN != nil {
			next.ALPN = slices.Clone(*patch.ALPN)
		}
		if patch.ClientFingerprint != nil {
			next.ClientFingerprint = *patch.ClientFingerprint
		}
		next.PublicKey = patch.PublicKey.apply(next.PublicKey)
		next.ShortID = patch.ShortID.apply(next.ShortID)
		return &next
	default:
		return nil
	}
}

func clonePointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

// ValidateDTO checks direct Go construction as well as decoded requests. Invalid
// UTF-8 is rejected before encoding/json could silently replace secret bytes.
func ValidateDTO(schema string, value any) error {
	if !validUTF8Value(reflect.ValueOf(value)) {
		return NewError(MalformedRequest)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return NewError(MalformedRequest)
	}
	_, err = CanonicalRequest(encoded, schema)
	return err
}

func validUTF8Value(value reflect.Value) bool {
	if !value.IsValid() {
		return true
	}
	switch value.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !value.IsNil() {
			return validUTF8Value(value.Elem())
		}
	case reflect.String:
		return utf8.ValidString(value.String())
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			if value.Type().Field(i).PkgPath == "" && !validUTF8Value(value.Field(i)) {
				return false
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			if !validUTF8Value(value.Index(i)) {
				return false
			}
		}
	case reflect.Map:
		iterator := value.MapRange()
		for iterator.Next() {
			if !validUTF8Value(iterator.Key()) || !validUTF8Value(iterator.Value()) {
				return false
			}
		}
	}
	return true
}

func (AuthInput) Format(s fmt.State, _ rune)         { _, _ = fmt.Fprint(s, "[REDACTED]") }
func (AuthInput) LogValue() slog.Value               { return slog.StringValue("[REDACTED]") }
func (SecurityInput) Format(s fmt.State, _ rune)     { _, _ = fmt.Fprint(s, "[REDACTED]") }
func (SecurityInput) LogValue() slog.Value           { return slog.StringValue("[REDACTED]") }
func (NodeInput) Format(s fmt.State, _ rune)         { _, _ = fmt.Fprint(s, "[REDACTED]") }
func (NodeInput) LogValue() slog.Value               { return slog.StringValue("[REDACTED]") }
func (NodeCreateRequest) Format(s fmt.State, _ rune) { _, _ = fmt.Fprint(s, "[REDACTED]") }
func (NodeCreateRequest) LogValue() slog.Value       { return slog.StringValue("[REDACTED]") }
func (NodePatchRequest) Format(s fmt.State, _ rune)  { _, _ = fmt.Fprint(s, "[REDACTED]") }
func (NodePatchRequest) LogValue() slog.Value        { return slog.StringValue("[REDACTED]") }
func (NodePatch) Format(s fmt.State, _ rune)         { _, _ = fmt.Fprint(s, "[REDACTED]") }
func (NodePatch) LogValue() slog.Value               { return slog.StringValue("[REDACTED]") }
func (AuthPatch) Format(s fmt.State, _ rune)         { _, _ = fmt.Fprint(s, "[REDACTED]") }
func (AuthPatch) LogValue() slog.Value               { return slog.StringValue("[REDACTED]") }
func (SecurityPatch) Format(s fmt.State, _ rune)     { _, _ = fmt.Fprint(s, "[REDACTED]") }
func (SecurityPatch) LogValue() slog.Value           { return slog.StringValue("[REDACTED]") }
