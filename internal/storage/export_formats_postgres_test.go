package storage

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/importparse"
)

func TestPostgresBase64ExportRevisionReauthAndDiagnostics(t *testing.T) {
	h := newNodeHTTPAcceptance(t)
	node := requireNode(t, h.do(http.MethodPost, "/api/v1/nodes", `{"name":"导出节点","node":{"schema_version":1,"protocol":"http","endpoint":{"host":"export.example.invalid","port":8080},"auth":{"kind":"none"},"transport":{"kind":"native_tcp"},"security":{"mode":"none"},"features":{},"extensions":{}}}`, "", nil), http.StatusCreated)
	request := strings.Replace(exportBody(t, node.Metadata.ResourceID, node.Metadata.Revision), `"uri_list"`, `"base64_uri_list"`, 1)
	requireNodeStatus(t, h.do(http.MethodPost, "/api/v1/exports", request, "", nil), http.StatusForbidden)
	h.setSession(t, h.do(http.MethodPost, "/api/v1/auth/reauth", `{"password":"`+nodeAcceptancePassword+`"}`, "", nil), http.StatusOK)
	updated := requireNode(t, h.do(http.MethodPatch, "/api/v1/nodes/"+string(node.Metadata.ResourceID), `{"name":"当前名称"}`, revisionTag(int64(node.Metadata.Revision)), nil), http.StatusOK)
	response := h.do(http.MethodPost, "/api/v1/exports", request, "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("Base64 export status %d", response.Code)
	}
	var exported apicontract.ExportResponse
	if json.Unmarshal(response.Body.Bytes(), &exported) != nil || len(exported.Data.Artifacts) != 1 {
		t.Fatal("invalid export response")
	}
	decoded, err := base64.StdEncoding.DecodeString(exported.Data.Artifacts[0].Content)
	if err != nil {
		t.Fatal("invalid Base64")
	}
	parsed := importparse.ParseURI(string(decoded))
	if !parsed.Valid() || parsed.Name != "导出节点" {
		t.Fatal("export did not use the explicitly selected immutable revision")
	}
	unsupported := requireNode(t, h.do(http.MethodPatch, "/api/v1/nodes/"+string(node.Metadata.ResourceID), `{"node":{"features":{"udp":false}}}`, revisionTag(int64(updated.Metadata.Revision)), nil), http.StatusOK)
	response = h.do(http.MethodPost, "/api/v1/exports", exportBody(t, node.Metadata.ResourceID, unsupported.Metadata.Revision), "", nil)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unrepresentable field status %d", response.Code)
	}
	var failure apicontract.ErrorResponse
	if json.Unmarshal(response.Body.Bytes(), &failure) != nil || len(failure.Error.Details) != 1 || failure.Error.Details[0].FieldPath != "/resources/0/node/features/udp" || failure.Error.Details[0].ResourceID != node.Metadata.ResourceID {
		t.Fatal("export failure did not identify the exact resource and field")
	}
	if strings.Contains(response.Body.String(), "export.example.invalid") {
		t.Fatal("export error exposed node configuration")
	}
}
