package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/identity"
	"github.com/Runarry/ProxyLoom/internal/importparse"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/subscriptions"
	"github.com/gin-gonic/gin"
)

func mountExports(router *gin.Engine, auth *Authentication, nodes NodeDependencies, publications subscriptions.Repository) error {
	if router == nil || auth == nil || nodes.Repository == nil {
		return errors.New("export_dependencies_invalid")
	}
	h := &nodeHandler{auth: auth, store: nodes.Repository, cursor: nodes.Cursor}
	router.POST("/api/v1/exports", auth.RequireSession(), func(c *gin.Context) { h.exportWithPublications(c, publications) })
	return nil
}

func (h *nodeHandler) export(c *gin.Context) { h.exportWithPublications(c, nil) }

func (h *nodeHandler) exportWithPublications(c *gin.Context, publications subscriptions.Repository) {
	var request apicontract.ExportRequest
	if !h.readRequest(c, "ExportRequest", &request) {
		return
	}
	if request.Type == "publication" && publications != nil {
		session, _ := SessionFromContext(c.Request.Context())
		if request.IncludeSecrets {
			now := time.Now()
			if session.ReauthenticatedAt.IsZero() || session.ReauthenticatedAt.After(now) || !session.ReauthenticatedAt.Add(identity.ReauthenticationLifetime).After(now) {
				h.fail(c, identity.ErrReauthenticationRequired)
				return
			}
			source, err := h.auth.source(c.Request)
			if err == nil {
				err = h.auth.service.AuditSensitive(c.Request.Context(), session.ID, identity.SensitiveExportPrivate, source)
			}
			if err != nil {
				h.fail(c, err)
				return
			}
		}
		artifacts, err := publications.Export(c.Request.Context(), subscriptions.Actor{ScopeID: session.User.ScopeID, ID: session.User.ID}, request.PublicationID, request.TargetKeys, ir.OutputFormat(request.Format), request.IncludeSecrets)
		if err != nil {
			if errors.Is(err, subscriptions.ErrBlocked) {
				err = apicontract.NewError(apicontract.PublicationBlocked)
			}
			h.fail(c, err)
			return
		}
		h.response(c, 200, "ExportResponse", apicontract.ExportResponse{RequestID: apicontract.RequestID(c.Request.Context()), Data: apicontract.ExportData{Artifacts: artifacts, CreatedAt: time.Now().UTC()}})
		return
	}
	if request.Type != "resources" {
		h.fail(c, apicontract.NewError(apicontract.ValidationFailed, apicontract.Detail{FieldPath: "/type"}))
		return
	}
	if !request.IncludeSecrets {
		h.fail(c, apicontract.NewError(apicontract.ValidationFailed, apicontract.Detail{FieldPath: "/include_secrets"}))
		return
	}
	session, _ := SessionFromContext(c.Request.Context())
	now := time.Now()
	if session.ReauthenticatedAt.IsZero() || session.ReauthenticatedAt.After(now) || !session.ReauthenticatedAt.Add(identity.ReauthenticationLifetime).After(now) {
		h.fail(c, identity.ErrReauthenticationRequired)
		return
	}
	source, err := h.auth.source(c.Request)
	if err == nil {
		err = h.auth.service.AuditSensitive(c.Request.Context(), session.ID, identity.SensitiveExportPrivate, source)
	}
	if err != nil {
		h.fail(c, err)
		return
	}
	if request.Format != "uri_list" && request.Format != "base64_uri_list" && request.Format != "proxyloom_json" {
		h.fail(c, apicontract.NewError(apicontract.ValidationFailed, apicontract.Detail{FieldPath: "/format"}))
		return
	}
	nodes := make([]ir.Resource, 0, len(request.Resources))
	for i, ref := range request.Resources {
		if ref.Kind != ir.KindNode {
			h.fail(c, apicontract.NewError(apicontract.ValidationFailed, apicontract.Detail{FieldPath: "/resources/" + strconv.Itoa(i) + "/kind"}))
			return
		}
		resource, err := h.store.Revision(c.Request.Context(), nodeScope(c), ref.ResourceID, int64(ref.Revision))
		if err != nil {
			h.fail(c, err)
			return
		}
		if resource.Metadata.Kind != ir.KindNode {
			h.fail(c, catalog.ErrNotFound)
			return
		}
		nodes = append(nodes, resource)
	}
	artifact, err := encodeExport(request.Format, nodes)
	if err != nil {
		h.fail(c, err)
		return
	}
	response := apicontract.ExportResponse{RequestID: apicontract.RequestID(c.Request.Context()), Data: apicontract.ExportData{
		Artifacts: []apicontract.ExportedArtifact{artifact}, CreatedAt: time.Now().UTC(),
	}}
	h.response(c, http.StatusOK, "ExportResponse", response)
}

func encodeExport(format string, nodes []ir.Resource) (apicontract.ExportedArtifact, error) {
	switch format {
	case "uri_list", "base64_uri_list":
		lines := make([]string, 0, len(nodes))
		for i, resource := range nodes {
			node, ok := resource.Payload.(*ir.Node)
			if !ok {
				return apicontract.ExportedArtifact{}, catalog.ErrInvalidInput
			}
			uri, err := importparse.EncodeURI(resource.Metadata.Name, *node)
			if err != nil {
				path := "/node"
				var diagnostic *importparse.ExportError
				if errors.As(err, &diagnostic) {
					path = diagnostic.FieldPath
				}
				return apicontract.ExportedArtifact{}, apicontract.NewError(apicontract.ValidationFailed, apicontract.Detail{FieldPath: "/resources/" + strconv.Itoa(i) + path, ResourceID: resource.Metadata.ResourceID})
			}
			lines = append(lines, uri)
		}
		content := strings.Join(lines, "\n")
		if format == "base64_uri_list" {
			return apicontract.ExportedArtifact{Filename: "nodes-base64.txt", MediaType: "text/plain", Content: base64.StdEncoding.EncodeToString([]byte(content)), ContainsSecrets: true}, nil
		}
		return apicontract.ExportedArtifact{Filename: "nodes-uri.txt", MediaType: "text/plain", Content: content, ContainsSecrets: true}, nil
	case "proxyloom_json":
		encoded, err := json.Marshal(nodes)
		if err != nil {
			return apicontract.ExportedArtifact{}, err
		}
		return apicontract.ExportedArtifact{Filename: "nodes.json", MediaType: "application/json", Content: string(encoded), ContainsSecrets: true}, nil
	default:
		return apicontract.ExportedArtifact{}, catalog.ErrInvalidInput
	}
}
