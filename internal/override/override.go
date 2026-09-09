// Package override applies typed source-field patches. It does not access the
// database, network, or current time.
package override

import (
	"encoding/json"
	"errors"
	"slices"
	"sort"

	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

const SchemaVersion = 1

var (
	ErrInvalid  = errors.New("override_invalid")
	ErrProtocol = errors.New("override_protocol")
	ErrRestore  = errors.New("override_restore")
)

type State string

const (
	Active   State = "active"
	Stale    State = "stale"
	Conflict State = "conflict"
)

func (s State) Valid() bool {
	return s == Active || s == Stale || s == Conflict
}

// Document is the encrypted overlay. An empty document is stored as a NULL envelope.
type Document struct {
	SchemaVersion int                    `json:"schema_version"`
	Name          *string                `json:"name,omitempty"`
	Node          *apicontract.NodePatch `json:"node,omitempty"`
}

type Binding struct {
	NodeID           ir.ID
	SourceItemID     ir.ID
	SourceResourceID ir.ID
	Revision         int64
	Method           ir.MatchMethod
	State            State
	Patch            Document
}

type Item struct {
	ID              ir.ID
	SourceID        ir.ID
	ExternalKey     string
	Name            string
	State           string
	NodeID          ir.ID
	SuggestedNodeID ir.ID
}

func (d Document) Empty() bool {
	return d.Name == nil && nodePatchEmpty(d.Node)
}

func (d Document) Fields() []string {
	fields := make([]string, 0, 16)
	if d.Name != nil {
		fields = append(fields, "/name")
	}
	if d.Node == nil {
		sort.Strings(fields)
		return fields
	}
	n := d.Node
	if n.Protocol != nil {
		fields = append(fields, "/protocol")
	}
	if n.Endpoint != nil {
		fields = append(fields, "/endpoint")
	}
	if n.Auth != nil {
		fields = append(fields, "/auth")
		if n.Auth.Password.Present() {
			fields = append(fields, "/auth/password")
		}
		if n.Auth.UUID.Present() {
			fields = append(fields, "/auth/uuid")
		}
		if n.Auth.Username.Present() {
			fields = append(fields, "/auth/username")
		}
		if n.Auth.Method != nil {
			fields = append(fields, "/auth/method")
		}
		if n.Auth.Cipher != nil {
			fields = append(fields, "/auth/cipher")
		}
	}
	if n.Transport != nil {
		fields = append(fields, "/transport")
	}
	if n.Security != nil {
		fields = append(fields, "/security")
		if n.Security.ServerName != nil {
			fields = append(fields, "/security/server_name")
		}
		if n.Security.VerifyCertificate != nil {
			fields = append(fields, "/security/verify_certificate")
		}
		if n.Security.ALPN != nil {
			fields = append(fields, "/security/alpn")
		}
		if n.Security.ClientFingerprint.Present() {
			fields = append(fields, "/security/client_fingerprint")
		}
		if n.Security.PublicKey.Present() {
			fields = append(fields, "/security/public_key")
		}
		if n.Security.ShortID.Present() {
			fields = append(fields, "/security/short_id")
		}
	}
	if n.Features != nil {
		fields = append(fields, "/features")
	}
	sort.Strings(fields)
	return fields
}

func Decode(plain []byte) (Document, error) {
	if len(plain) == 0 {
		return Document{}, nil
	}
	var document Document
	if json.Unmarshal(plain, &document) != nil {
		return Document{}, ErrInvalid
	}
	if document.SchemaVersion != SchemaVersion && document.SchemaVersion != 0 {
		return Document{}, ErrInvalid
	}
	document.SchemaVersion = SchemaVersion
	if document.Node != nil && document.Node.Protocol != nil {
		return Document{}, ErrProtocol
	}
	return document, nil
}

func (d Document) Canonical() ([]byte, error) {
	if d.Empty() {
		return nil, nil
	}
	d.SchemaVersion = SchemaVersion
	if d.Node != nil && d.Node.Protocol != nil {
		return nil, ErrProtocol
	}
	return json.Marshal(d)
}

func Merge(base ir.Node, baseName string, patch Document) (ir.Node, string, []string, error) {
	if base.Validate() != nil {
		return ir.Node{}, "", nil, ErrInvalid
	}
	if patch.Node != nil && patch.Node.Protocol != nil {
		return ir.Node{}, "", nil, ErrProtocol
	}
	next := base
	if patch.Node != nil && !nodePatchEmpty(patch.Node) {
		merged, err := patch.Node.Apply(base)
		if err != nil {
			return ir.Node{}, "", nil, err
		}
		merged.Origin = base.Origin
		next = merged
	}
	name := baseName
	if patch.Name != nil {
		name = *patch.Name
	}
	if next.Validate() != nil {
		return ir.Node{}, "", nil, ErrInvalid
	}
	return next, name, patch.Fields(), nil
}

func Combine(old Document, name *string, node *apicontract.NodePatch, restore []string) (Document, error) {
	next := old
	next.SchemaVersion = SchemaVersion
	if name != nil {
		copy := *name
		next.Name = &copy
	}
	if node != nil {
		if node.Protocol != nil {
			return Document{}, ErrProtocol
		}
		merged, err := mergeNodePatch(next.Node, node)
		if err != nil {
			return Document{}, err
		}
		next.Node = merged
	}
	return next.Restore(restore)
}

func (d Document) Restore(paths []string) (Document, error) {
	if len(paths) == 0 {
		return d, nil
	}
	seen := map[string]bool{}
	for _, path := range paths {
		if path == "" || seen[path] {
			return Document{}, ErrRestore
		}
		seen[path] = true
		switch path {
		case "/name":
			d.Name = nil
		case "/protocol":
			if d.Node != nil {
				d.Node.Protocol = nil
			}
		case "/endpoint":
			if d.Node != nil {
				d.Node.Endpoint = nil
			}
		case "/auth":
			if d.Node != nil {
				d.Node.Auth = nil
			}
		case "/auth/password":
			clearSecret(&d, func(auth *apicontract.AuthPatch) { auth.Password = apicontract.SecretPatch{} })
		case "/auth/uuid":
			clearSecret(&d, func(auth *apicontract.AuthPatch) { auth.UUID = apicontract.SecretPatch{} })
		case "/auth/username":
			clearSecret(&d, func(auth *apicontract.AuthPatch) { auth.Username = apicontract.SecretPatch{} })
		case "/auth/method":
			if d.Node != nil && d.Node.Auth != nil {
				d.Node.Auth.Method = nil
			}
		case "/auth/cipher":
			if d.Node != nil && d.Node.Auth != nil {
				d.Node.Auth.Cipher = nil
			}
		case "/transport":
			if d.Node != nil {
				d.Node.Transport = nil
			}
		case "/security":
			if d.Node != nil {
				d.Node.Security = nil
			}
		case "/security/server_name":
			if d.Node != nil && d.Node.Security != nil {
				d.Node.Security.ServerName = nil
			}
		case "/security/verify_certificate":
			if d.Node != nil && d.Node.Security != nil {
				d.Node.Security.VerifyCertificate = nil
			}
		case "/security/alpn":
			if d.Node != nil && d.Node.Security != nil {
				d.Node.Security.ALPN = nil
			}
		case "/security/client_fingerprint":
			if d.Node != nil && d.Node.Security != nil {
				d.Node.Security.ClientFingerprint = apicontract.OptionalStringPatch{}
			}
		case "/security/public_key":
			if d.Node != nil && d.Node.Security != nil {
				d.Node.Security.PublicKey = apicontract.SecretPatch{}
			}
		case "/security/short_id":
			if d.Node != nil && d.Node.Security != nil {
				d.Node.Security.ShortID = apicontract.SecretPatch{}
			}
		case "/features":
			if d.Node != nil {
				d.Node.Features = nil
			}
		default:
			return Document{}, ErrRestore
		}
	}
	if d.Node != nil && nodePatchEmpty(d.Node) {
		d.Node = nil
	}
	return d, nil
}

func mergeNodePatch(old, add *apicontract.NodePatch) (*apicontract.NodePatch, error) {
	if add == nil {
		return old, nil
	}
	if add.Protocol != nil {
		return nil, ErrProtocol
	}
	next := apicontract.NodePatch{}
	if old != nil {
		next = *old
	}
	if add.Endpoint != nil {
		copy := *add.Endpoint
		next.Endpoint = &copy
	}
	if add.Auth != nil {
		next.Auth = add.Auth
	}
	if add.Transport != nil {
		next.Transport = add.Transport
	}
	if add.Security != nil {
		next.Security = add.Security
	}
	if add.Features != nil {
		next.Features = add.Features
	}
	if nodePatchEmpty(&next) {
		return nil, nil
	}
	return &next, nil
}

func nodePatchEmpty(patch *apicontract.NodePatch) bool {
	if patch == nil {
		return true
	}
	return patch.Protocol == nil && patch.Endpoint == nil && patch.Auth == nil && patch.Transport == nil && patch.Security == nil && patch.Features == nil
}

func clearSecret(document *Document, clear func(*apicontract.AuthPatch)) {
	if document.Node == nil || document.Node.Auth == nil {
		return
	}
	clear(document.Node.Auth)
}

func EqualFields(left, right []string) bool {
	return slices.Equal(left, right)
}
