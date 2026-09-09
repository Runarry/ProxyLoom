package server

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/importparse"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

func TestBase64ExportPreservesURIListAndRejectsLostFields(t *testing.T) {
	node := &ir.Node{SchemaVersion: 1, Protocol: ir.HTTP,
		Endpoint: ir.Endpoint{Host: "export.example.invalid", Port: 8080},
		Auth:     &ir.NoAuth{Kind: ir.AuthNone}, Transport: &ir.NativeTCPTransport{Kind: ir.NativeTCP},
		Security: &ir.NoSecurity{Mode: ir.SecurityNone}}
	resources := []ir.Resource{
		{Metadata: ir.Metadata{Name: "节点一"}, Payload: node},
		{Metadata: ir.Metadata{Name: "节点二 + 空格"}, Payload: node},
	}
	plain, err := encodeExport("uri_list", resources)
	if err != nil {
		t.Fatal("URI export failed")
	}
	encoded, err := encodeExport("base64_uri_list", resources)
	if err != nil {
		t.Fatal("Base64 export failed")
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded.Content)
	if err != nil || string(decoded) != plain.Content || !encoded.ContainsSecrets {
		t.Fatal("Base64 changed the URI list or omitted sensitivity")
	}
	for index, uri := range strings.Split(string(decoded), "\n") {
		parsed := importparse.ParseURI(uri)
		if !parsed.Valid() || parsed.Name != resources[index].Metadata.Name || parsed.Node.Endpoint != node.Endpoint {
			t.Fatal("Base64 node round-trip changed semantics")
		}
	}
	disabled := false
	node.Features.UDP = &disabled
	_, err = encodeExport("base64_uri_list", resources)
	var failure *apicontract.Error
	if !errors.As(err, &failure) {
		t.Fatal("unsupported explicit UDP field was not rejected with an API diagnostic")
	}
}
