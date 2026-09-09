package apicontract

import (
	"encoding/json"
	"slices"
	"strconv"

	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

// Revision/Counter preserve int64 precision in JavaScript clients. Their wire
// representation is a canonical decimal string, never a JSON number.
type Revision int64
type Counter int64

func decimal(data []byte, positive bool) (int64, error) {
	var value string
	if json.Unmarshal(data, &value) != nil {
		return 0, NewError(MalformedRequest)
	}
	number, err := strconv.ParseInt(value, 10, 64)
	if err != nil || number < 0 || (positive && number == 0) || strconv.FormatInt(number, 10) != value {
		return 0, NewError(MalformedRequest)
	}
	return number, nil
}
func (r Revision) MarshalJSON() ([]byte, error) {
	if r < 1 {
		return nil, NewError(InternalError)
	}
	return json.Marshal(strconv.FormatInt(int64(r), 10))
}
func (r *Revision) UnmarshalJSON(data []byte) error {
	number, err := decimal(data, true)
	if err == nil {
		*r = Revision(number)
	}
	return err
}
func (c Counter) MarshalJSON() ([]byte, error) {
	if c < 0 {
		return nil, NewError(InternalError)
	}
	return json.Marshal(strconv.FormatInt(int64(c), 10))
}
func (c *Counter) UnmarshalJSON(data []byte) error {
	number, err := decimal(data, false)
	if err == nil {
		*c = Counter(number)
	}
	return err
}

type ResourceMetadata struct {
	ResourceID    ir.ID           `json:"resource_id"`
	ScopeID       ir.ID           `json:"scope_id"`
	Kind          ir.ResourceKind `json:"kind"`
	Revision      Revision        `json:"revision"`
	SchemaVersion int             `json:"schema_version"`
	Name          string          `json:"name"`
	Tags          []string        `json:"tags"`
	Enabled       bool            `json:"enabled"`
	SecurityEpoch Revision        `json:"security_epoch"`
}

func metadata(value ir.Metadata) ResourceMetadata {
	return ResourceMetadata{ResourceID: value.ResourceID, ScopeID: value.ScopeID, Kind: value.Kind, Revision: Revision(value.Revision), SchemaVersion: value.SchemaVersion, Name: value.Name, Tags: slices.Clone(value.Tags), Enabled: value.Enabled, SecurityEpoch: Revision(value.SecurityEpoch)}
}

// These response types contain no ir.Secret, raw JSON or credential-bearing
// interfaces, including nested REALITY parameters. Presence replaces raw values.
type AuthRead struct {
	Kind        ir.AuthKind           `json:"kind"`
	Method      *ir.ShadowsocksMethod `json:"method,omitempty"`
	Cipher      *ir.VMessCipher       `json:"cipher,omitempty"`
	HasPassword *bool                 `json:"has_password,omitempty"`
	HasUUID     *bool                 `json:"has_uuid,omitempty"`
	HasUsername *bool                 `json:"has_username,omitempty"`
}

type SecurityRead struct {
	Mode              ir.SecurityMode `json:"mode"`
	ServerName        *string         `json:"server_name,omitempty"`
	VerifyCertificate *bool           `json:"verify_certificate,omitempty"`
	ALPN              []string        `json:"alpn,omitempty"`
	ClientFingerprint *string         `json:"client_fingerprint,omitempty"`
	HasPublicKey      *bool           `json:"has_public_key,omitempty"`
	HasShortID        *bool           `json:"has_short_id,omitempty"`
}

type NodeRead struct {
	SchemaVersion int           `json:"schema_version"`
	Protocol      ir.Protocol   `json:"protocol"`
	Endpoint      ir.Endpoint   `json:"endpoint"`
	Auth          AuthRead      `json:"auth"`
	Transport     Transport     `json:"transport"`
	Security      SecurityRead  `json:"security"`
	Features      ir.Features   `json:"features"`
	Extensions    ir.Extensions `json:"extensions"`
	Origin        *ir.Origin    `json:"origin,omitempty"`
}

type NodeResource struct {
	Metadata ResourceMetadata `json:"metadata"`
	Node     NodeRead         `json:"node"`
}
type NodeReadResponse struct {
	RequestID string       `json:"request_id"`
	Data      NodeResource `json:"data"`
}

func NewNodeReadResponse(requestID string, resource ir.Resource) (NodeReadResponse, error) {
	if resource.Validate() != nil {
		return NodeReadResponse{}, NewError(InternalError)
	}
	node, ok := resource.Payload.(*ir.Node)
	if !ok {
		return NodeReadResponse{}, NewError(InternalError)
	}
	read := NodeRead{SchemaVersion: node.SchemaVersion, Protocol: node.Protocol, Endpoint: node.Endpoint, Features: ir.Features{UDP: clonePointer(node.Features.UDP), Multiplex: clonePointer(node.Features.Multiplex), ProtocolVariant: clonePointer(node.Features.ProtocolVariant)}, Origin: clonePointer(node.Origin)}
	configured := func(secret ir.Secret) *bool { value := secret != ""; return &value }
	switch auth := node.Auth.(type) {
	case *ir.NoAuth:
		read.Auth = AuthRead{Kind: auth.Kind}
	case *ir.PasswordAuth:
		read.Auth = AuthRead{Kind: auth.Kind, HasPassword: configured(auth.Password)}
	case *ir.UUIDAuth:
		read.Auth = AuthRead{Kind: auth.Kind, HasUUID: configured(auth.UUID)}
	case *ir.VMessAuth:
		read.Auth = AuthRead{Kind: auth.Kind, Cipher: clonePointer(&auth.Cipher), HasUUID: configured(auth.UUID)}
	case *ir.MethodPasswordAuth:
		read.Auth = AuthRead{Kind: auth.Kind, Method: clonePointer(&auth.Method), HasPassword: configured(auth.Password)}
	case *ir.UsernamePasswordAuth:
		read.Auth = AuthRead{Kind: auth.Kind, HasUsername: configured(auth.Username), HasPassword: configured(auth.Password)}
	}
	switch transport := node.Transport.(type) {
	case *ir.NativeTCPTransport:
		read.Transport = Transport{Kind: transport.Kind}
	case *ir.WebSocketTransport:
		read.Transport = Transport{Kind: transport.Kind, Path: clonePointer(&transport.Path), Host: clonePointer(transport.Host)}
	}
	switch security := node.Security.(type) {
	case *ir.NoSecurity:
		read.Security = SecurityRead{Mode: security.Mode}
	case *ir.TLSSecurity:
		read.Security = SecurityRead{Mode: security.Mode, ServerName: clonePointer(&security.ServerName), VerifyCertificate: clonePointer(security.VerifyCertificate), ALPN: slices.Clone(security.ALPN), ClientFingerprint: clonePointer(security.ClientFingerprint)}
	case *ir.RealitySecurity:
		read.Security = SecurityRead{Mode: security.Mode, ServerName: clonePointer(&security.ServerName), ALPN: slices.Clone(security.ALPN), ClientFingerprint: clonePointer(&security.ClientFingerprint), HasPublicKey: configured(security.PublicKey), HasShortID: configured(security.ShortID)}
	}
	return NodeReadResponse{RequestID: safeRequestID(requestID), Data: NodeResource{Metadata: metadata(resource.Metadata), Node: read}}, nil
}

type ChainCreateRequest struct {
	Name          string           `json:"name"`
	Tags          []string         `json:"tags,omitempty"`
	Enabled       *bool            `json:"enabled,omitempty"`
	Hops          []ir.NodeRef     `json:"hops"`
	FailurePolicy ir.FailurePolicy `json:"failure_policy"`
}

func (request ChainCreateRequest) Input() (catalog.CreateInput, error) {
	if err := ValidateDTO("ChainCreateRequest", request); err != nil {
		return catalog.CreateInput{}, err
	}
	chain := ir.Chain{SchemaVersion: ir.SchemaVersion, Hops: slices.Clone(request.Hops), FailurePolicy: request.FailurePolicy}
	if err := chain.Validate(); err != nil {
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
	return catalog.CreateInput{Name: request.Name, Tags: tags, Enabled: enabled, Payload: &chain}, nil
}

type ChainRead struct {
	SchemaVersion int              `json:"schema_version"`
	Hops          []ir.NodeRef     `json:"hops"`
	FailurePolicy ir.FailurePolicy `json:"failure_policy"`
}
type ChainResource struct {
	Metadata ResourceMetadata `json:"metadata"`
	Chain    ChainRead        `json:"chain"`
}
type ChainReadResponse struct {
	RequestID string        `json:"request_id"`
	Data      ChainResource `json:"data"`
}

func NewChainReadResponse(requestID string, resource ir.Resource) (ChainReadResponse, error) {
	if resource.Validate() != nil {
		return ChainReadResponse{}, NewError(InternalError)
	}
	chain, ok := resource.Payload.(*ir.Chain)
	if !ok {
		return ChainReadResponse{}, NewError(InternalError)
	}
	return ChainReadResponse{RequestID: safeRequestID(requestID), Data: ChainResource{Metadata: metadata(resource.Metadata), Chain: ChainRead{SchemaVersion: chain.SchemaVersion, Hops: slices.Clone(chain.Hops), FailurePolicy: chain.FailurePolicy}}}, nil
}

type ChainListResponse struct {
	RequestID string          `json:"request_id"`
	Data      []ChainResource `json:"data"`
	Page      PageInfo        `json:"page"`
}

func NewChainListResponse(requestID string, items []ir.Resource, page PageInfo) (ChainListResponse, error) {
	response := ChainListResponse{RequestID: safeRequestID(requestID), Data: make([]ChainResource, 0, len(items)), Page: page}
	for _, resource := range items {
		read, err := NewChainReadResponse(requestID, resource)
		if err != nil {
			return ChainListResponse{}, err
		}
		response.Data = append(response.Data, read.Data)
	}
	return response, nil
}

type ChainPatchRequest struct {
	Name          *string           `json:"name,omitempty"`
	Tags          *[]string         `json:"tags,omitempty"`
	Enabled       *bool             `json:"enabled,omitempty"`
	Hops          *[]ir.NodeRef     `json:"hops,omitempty"`
	FailurePolicy *ir.FailurePolicy `json:"failure_policy,omitempty"`
}

func (request ChainPatchRequest) Merge(old ir.Resource) (catalog.UpdateInput, error) {
	if err := ValidateDTO("ChainPatchRequest", request); err != nil {
		return catalog.UpdateInput{}, err
	}
	if old.Validate() != nil {
		return catalog.UpdateInput{}, NewError(InternalError)
	}
	chain, ok := old.Payload.(*ir.Chain)
	if !ok {
		return catalog.UpdateInput{}, NewError(ValidationFailed)
	}
	next := ir.Chain{SchemaVersion: chain.SchemaVersion, Hops: slices.Clone(chain.Hops), FailurePolicy: chain.FailurePolicy}
	if request.Hops != nil {
		next.Hops = slices.Clone(*request.Hops)
	}
	if request.FailurePolicy != nil {
		next.FailurePolicy = *request.FailurePolicy
	}
	if err := next.Validate(); err != nil {
		return catalog.UpdateInput{}, AsError(err)
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
