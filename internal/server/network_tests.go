package server

import (
	"encoding/json"
	"errors"
	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/jobs"
	"github.com/Runarry/ProxyLoom/internal/networktest"
	"github.com/gin-gonic/gin"
	"net/url"
	"strconv"
	"time"
)

type networkTestHandler struct {
	*nodeHandler
	tests networktest.Repository
	jobs  jobs.ManagementRepository
}

func mountNetworkTests(router *gin.Engine, auth *Authentication, nodes NodeDependencies, tests networktest.Repository, queue jobs.ManagementRepository) error {
	if auth == nil || nodes.Cursor == nil {
		return errors.New("network_test_dependencies_required")
	}
	h := &networkTestHandler{&nodeHandler{auth: auth, store: nodes.Repository, cursor: nodes.Cursor}, tests, queue}
	r := router.Group("/api/v1", auth.RequireSession())
	r.GET("/test-targets", h.targets)
	r.POST("/test-targets", h.createTarget)
	r.GET("/test-targets/:id", h.target)
	r.PATCH("/test-targets/:id", h.patchTarget)
	r.DELETE("/test-targets/:id", h.deleteTarget)
	r.POST("/tests", h.create)
	r.GET("/test-results", h.results)
	return nil
}
func (h *networkTestHandler) fail(c *gin.Context, err error) {
	switch {
	case errors.Is(err, jobs.ErrBudgetExceeded):
		err = apicontract.NewError(apicontract.BudgetExceeded)
	case errors.Is(err, jobs.ErrInvalidInput), errors.Is(err, networktest.ErrTarget), errors.Is(err, networktest.ErrAddress):
		err = apicontract.NewError(apicontract.ValidationFailed)
	}
	h.auth.fail(c, err)
}
func (h *networkTestHandler) actor(c *gin.Context, key bool) (networktest.Actor, bool) {
	session, _ := SessionFromContext(c.Request.Context())
	a := networktest.Actor{ScopeID: session.User.ScopeID, ID: session.User.ID}
	if key {
		request, err := apicontract.ReadIdempotency(c.Request.Header, a.ScopeID, a.ID, "network_test")
		if err != nil {
			h.fail(c, err)
			return a, false
		}
		a.Key = request.Key
	}
	return a, true
}
func (h *networkTestHandler) sendTarget(c *gin.Context, status int, target networktest.Target) {
	tag, _ := apicontract.ETag(int64(target.Revision))
	c.Header("ETag", tag)
	h.response(c, status, "TestTargetResponse", gin.H{"request_id": apicontract.RequestID(c.Request.Context()), "data": target})
}
func (h *networkTestHandler) target(c *gin.Context) {
	id, ok := h.id(c)
	if !ok {
		return
	}
	t, err := h.tests.Target(c.Request.Context(), nodeScope(c), id)
	if err != nil {
		h.fail(c, err)
		return
	}
	h.sendTarget(c, 200, t)
}
func (h *networkTestHandler) createTarget(c *gin.Context) {
	var request networktest.TargetRequest
	if !h.readRequest(c, "TestTargetCreateRequest", &request) {
		return
	}
	a, ok := h.actor(c, true)
	if !ok {
		return
	}
	t, err := h.tests.WriteTarget(c.Request.Context(), a, "", 0, request)
	if err != nil {
		h.fail(c, err)
		return
	}
	h.sendTarget(c, 201, t)
}
func (h *networkTestHandler) patchTarget(c *gin.Context) {
	id, ok := h.id(c)
	if !ok {
		return
	}
	revision, ok := h.expected(c)
	if !ok {
		return
	}
	var patch struct {
		Name    *string                    `json:"name,omitempty"`
		Enabled *bool                      `json:"enabled,omitempty"`
		Config  map[string]json.RawMessage `json:"target,omitempty"`
	}
	if !h.readRequest(c, "TestTargetPatchRequest", &patch) {
		return
	}
	old, err := h.tests.Target(c.Request.Context(), nodeScope(c), id)
	if err != nil {
		h.fail(c, err)
		return
	}
	if patch.Name != nil {
		old.Name = *patch.Name
	}
	if patch.Enabled != nil {
		old.Enabled = *patch.Enabled
	}
	data, _ := json.Marshal(old.Config)
	var merged map[string]json.RawMessage
	_ = json.Unmarshal(data, &merged)
	for k, v := range patch.Config {
		merged[k] = v
	}
	data, _ = json.Marshal(merged)
	if json.Unmarshal(data, &old.Config) != nil {
		h.fail(c, networktest.ErrTarget)
		return
	}
	a, _ := h.actor(c, false)
	t, err := h.tests.WriteTarget(c.Request.Context(), a, id, revision, networktest.TargetRequest{Name: old.Name, Enabled: &old.Enabled, Config: old.Config})
	if err != nil {
		h.fail(c, err)
		return
	}
	h.sendTarget(c, 200, t)
}
func (h *networkTestHandler) deleteTarget(c *gin.Context) {
	id, ok := h.id(c)
	if !ok {
		return
	}
	revision, ok := h.expected(c)
	if !ok {
		return
	}
	if !h.emptyBody(c) {
		return
	}
	a, _ := h.actor(c, false)
	if err := h.tests.DeleteTarget(c.Request.Context(), a, id, revision); err != nil {
		h.fail(c, err)
		return
	}
	tag, _ := apicontract.ETag(revision + 1)
	c.Header("ETag", tag)
	h.response(c, 200, "MutationResponse", gin.H{"request_id": apicontract.RequestID(c.Request.Context()), "data": gin.H{"resource_id": id, "revision": apicontract.Revision(revision + 1), "security_epoch": apicontract.Revision(revision + 1), "status": "deleted"}})
}
func (h *networkTestHandler) create(c *gin.Context) {
	var request networktest.Request
	if !h.readRequest(c, "TestCreateRequest", &request) {
		return
	}
	a, ok := h.actor(c, true)
	if !ok {
		return
	}
	id, err := h.tests.Create(c.Request.Context(), a, request)
	if err != nil {
		h.fail(c, err)
		return
	}
	snapshot, err := h.jobs.Snapshot(c.Request.Context(), a.ScopeID, id)
	if err != nil {
		h.fail(c, err)
		return
	}
	(&jobHandler{auth: h.auth}).snapshot(c, 202, snapshot)
}
func (h *networkTestHandler) pagination(c *gin.Context, collection string, allowed map[string]bool) (apicontract.Pagination, apicontract.CursorBinding, networktest.PagePosition, url.Values, bool) {
	query, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		h.fail(c, err)
		return apicontract.Pagination{}, apicontract.CursorBinding{}, networktest.PagePosition{}, nil, false
	}
	filters := url.Values{}
	for k, v := range query {
		if len(v) != 1 || k != "cursor" && k != "limit" && !allowed[k] {
			h.fail(c, jobs.ErrInvalidInput)
			return apicontract.Pagination{}, apicontract.CursorBinding{}, networktest.PagePosition{}, nil, false
		}
		if k != "cursor" && k != "limit" {
			filters[k] = v
		}
	}
	p, err := apicontract.ParsePagination(query)
	if err != nil {
		h.fail(c, err)
		return p, apicontract.CursorBinding{}, networktest.PagePosition{}, nil, false
	}
	hash, err := apicontract.FilterHash(filters)
	if err != nil {
		h.fail(c, err)
		return p, apicontract.CursorBinding{}, networktest.PagePosition{}, nil, false
	}
	b := apicontract.CursorBinding{ScopeID: nodeScope(c), Collection: collection, Sort: apicontract.CreatedAtIDSort, FilterHash: hash}
	var after networktest.PagePosition
	if p.Cursor != "" {
		pos, err := h.cursor.Decode(p.Cursor, b)
		if err != nil {
			h.fail(c, err)
			return p, b, after, nil, false
		}
		after = networktest.PagePosition{ID: pos.ID, CreatedAt: pos.CreatedAt}
	}
	return p, b, after, filters, true
}
func (h *networkTestHandler) targets(c *gin.Context) {
	p, b, after, f, ok := h.pagination(c, "test-targets", map[string]bool{"enabled": true})
	if !ok {
		return
	}
	var enabled *bool
	if raw := f.Get("enabled"); raw != "" {
		if raw != "true" && raw != "false" {
			h.fail(c, jobs.ErrInvalidInput)
			return
		}
		value := raw == "true"
		enabled = &value
	}
	items, more, err := h.tests.Targets(c.Request.Context(), nodeScope(c), p.Limit, after, enabled)
	if err != nil {
		h.fail(c, err)
		return
	}
	page := apicontract.PageInfo{Limit: p.Limit}
	if more && len(items) > 0 {
		last := items[len(items)-1]
		page.NextCursor, err = h.cursor.Encode(b, apicontract.CursorPosition{ID: last.ID, CreatedAt: last.CreatedAt})
		if err != nil {
			h.fail(c, err)
			return
		}
	}
	h.response(c, 200, "TestTargetListResponse", gin.H{"request_id": apicontract.RequestID(c.Request.Context()), "data": items, "page": page})
}
func (h *networkTestHandler) results(c *gin.Context) {
	p, b, after, f, ok := h.pagination(c, "test-results", map[string]bool{"subject_id": true, "subject_revision": true, "core_build_id": true, "type": true, "location": true, "from": true, "until": true})
	if !ok {
		return
	}
	var revision int64
	if raw := f.Get("subject_revision"); raw != "" {
		var err error
		revision, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || revision < 1 {
			h.fail(c, jobs.ErrInvalidInput)
			return
		}
	}
	items, more, err := h.tests.Results(c.Request.Context(), nodeScope(c), networktest.ResultFilter{SubjectID: ir.ID(f.Get("subject_id")), SubjectRevision: revision, CoreBuildID: ir.ID(f.Get("core_build_id")), After: after, Type: f.Get("type"), Location: f.Get("location"), From: f.Get("from"), Until: f.Get("until"), Limit: p.Limit})
	if err != nil {
		h.fail(c, err)
		return
	}
	page := apicontract.PageInfo{Limit: p.Limit}
	if more && len(items) > 0 {
		var last struct {
			ID ir.ID     `json:"result_id"`
			At time.Time `json:"completed_at"`
		}
		if json.Unmarshal(items[len(items)-1], &last) != nil {
			h.fail(c, jobs.ErrUnavailable)
			return
		}
		page.NextCursor, err = h.cursor.Encode(b, apicontract.CursorPosition{ID: last.ID, CreatedAt: last.At})
		if err != nil {
			h.fail(c, err)
			return
		}
	}
	h.response(c, 200, "TestResultListResponse", gin.H{"request_id": apicontract.RequestID(c.Request.Context()), "data": items, "page": page})
}
