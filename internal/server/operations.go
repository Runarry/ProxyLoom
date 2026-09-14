package server

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/operations"
	"github.com/gin-gonic/gin"
)

type operationsHandler struct {
	*networkTestHandler
	operations operations.Repository
}

func mountOperations(router *gin.Engine, auth *Authentication, nodes NodeDependencies, repository operations.Repository) error {
	if auth == nil || nodes.Cursor == nil {
		return errors.New("operations_dependencies_required")
	}
	h := &operationsHandler{&networkTestHandler{nodeHandler: &nodeHandler{auth: auth, store: nodes.Repository, cursor: nodes.Cursor}}, repository}
	r := router.Group("/api/v1", auth.RequireSession())
	r.GET("/system/settings", h.settings)
	r.PATCH("/system/settings", h.update)
	r.GET("/system/overview", h.overview)
	r.GET("/audit-events", h.audit)
	return nil
}
func (h *operationsHandler) fail(c *gin.Context, err error) {
	if errors.Is(err, operations.ErrInvalid) {
		err = apicontract.NewError(apicontract.ValidationFailed)
	}
	h.auth.fail(c, err)
}
func (h *operationsHandler) sendSettings(c *gin.Context, s operations.Settings) {
	tag, _ := apicontract.ETag(int64(s.Revision))
	c.Header("ETag", tag)
	h.response(c, 200, "SettingsResponse", gin.H{"request_id": apicontract.RequestID(c.Request.Context()), "data": s})
}
func (h *operationsHandler) settings(c *gin.Context) {
	if c.Request.URL.RawQuery != "" {
		h.fail(c, operations.ErrInvalid)
		return
	}
	s, err := h.operations.Settings(c.Request.Context(), nodeScope(c))
	if err != nil {
		h.fail(c, err)
		return
	}
	h.sendSettings(c, s)
}
func (h *operationsHandler) update(c *gin.Context) {
	expected, ok := h.expected(c)
	if !ok {
		return
	}
	var patch map[string]json.RawMessage
	if !h.readRequest(c, "SettingsPatchRequest", &patch) {
		return
	}
	s, err := h.operations.Settings(c.Request.Context(), nodeScope(c))
	if err != nil {
		h.fail(c, err)
		return
	}
	data, _ := json.Marshal(s)
	var fields map[string]json.RawMessage
	json.Unmarshal(data, &fields)
	for group, value := range patch {
		if group == "cleanup_paused" || group == "private_proxy_cidrs" {
			fields[group] = value
			continue
		}
		var prior, updates map[string]json.RawMessage
		json.Unmarshal(fields[group], &prior)
		json.Unmarshal(value, &updates)
		for key, item := range updates {
			prior[key] = item
		}
		fields[group], _ = json.Marshal(prior)
	}
	data, _ = json.Marshal(fields)
	if json.Unmarshal(data, &s) != nil {
		h.fail(c, operations.ErrInvalid)
		return
	}
	a, _ := h.actor(c, false)
	s, err = h.operations.Update(c.Request.Context(), operations.Actor{ScopeID: a.ScopeID, ID: a.ID, RequestID: apicontract.RequestID(c.Request.Context())}, expected, s)
	if err != nil {
		h.fail(c, err)
		return
	}
	h.sendSettings(c, s)
}
func (h *operationsHandler) overview(c *gin.Context) {
	if c.Request.URL.RawQuery != "" {
		h.fail(c, operations.ErrInvalid)
		return
	}
	o, err := h.operations.Overview(c.Request.Context(), nodeScope(c))
	if err != nil {
		h.fail(c, err)
		return
	}
	h.response(c, 200, "SystemOverviewResponse", gin.H{"request_id": apicontract.RequestID(c.Request.Context()), "data": o})
}
func (h *operationsHandler) audit(c *gin.Context) {
	p, b, after, f, ok := h.pagination(c, "audit-events", map[string]bool{"actor_id": true, "resource_id": true, "action": true, "from": true, "until": true})
	if !ok {
		return
	}
	items, more, err := h.operations.Audit(c.Request.Context(), nodeScope(c), operations.AuditFilter{ActorID: ir.ID(f.Get("actor_id")), ResourceID: ir.ID(f.Get("resource_id")), Action: f.Get("action"), From: f.Get("from"), Until: f.Get("until"), After: operations.PagePosition{ID: after.ID, CreatedAt: after.CreatedAt}, Limit: p.Limit})
	if err != nil {
		h.fail(c, err)
		return
	}
	page := apicontract.PageInfo{Limit: p.Limit}
	if more && len(items) > 0 {
		var last struct {
			ID      ir.ID     `json:"event_id"`
			Created time.Time `json:"created_at"`
		}
		if json.Unmarshal(items[len(items)-1], &last) != nil {
			h.fail(c, operations.ErrInvalid)
			return
		}
		page.NextCursor, err = h.cursor.Encode(b, apicontract.CursorPosition{ID: last.ID, CreatedAt: last.Created})
		if err != nil {
			h.fail(c, err)
			return
		}
	}
	h.response(c, 200, "AuditEventListResponse", gin.H{"request_id": apicontract.RequestID(c.Request.Context()), "data": items, "page": page})
}
