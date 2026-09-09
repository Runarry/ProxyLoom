package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"unicode/utf8"

	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/jobs"
	"github.com/Runarry/ProxyLoom/internal/override"
	"github.com/Runarry/ProxyLoom/internal/source"
	"github.com/gin-gonic/gin"
)

type SourceRepository interface {
	Head(context.Context, ir.ID, ir.ID) (source.Document, error)
	List(context.Context, ir.ID, catalog.SourceListOptions) (source.Page, error)
	Items(context.Context, ir.ID, ir.ID) ([]override.Item, error)
	Create(context.Context, source.Mutation) (source.Document, error)
	Update(context.Context, source.Mutation) (source.Document, error)
	Delete(context.Context, source.Mutation) (source.Document, error)
	EnqueueRefresh(context.Context, source.RefreshRequest) (jobs.Job, bool, error)
}

type sourceHandler struct {
	nodes *nodeHandler
	store SourceRepository
}

func mountSources(router *gin.Engine, auth *Authentication, nodes NodeDependencies, store SourceRepository) error {
	if router == nil || auth == nil || nodes.Repository == nil || nodes.Cursor == nil || store == nil {
		return errors.New("source_dependencies_invalid")
	}
	h := &sourceHandler{nodes: &nodeHandler{auth: auth, store: nodes.Repository, cursor: nodes.Cursor}, store: store}
	group := router.Group("/api/v1/sources", auth.RequireSession())
	group.GET("", h.list)
	group.POST("", h.create)
	group.GET("/:id", h.get)
	group.PATCH("/:id", h.patch)
	group.DELETE("/:id", h.delete)
	group.POST("/:id/refresh", h.refresh)
	return nil
}

func (h *sourceHandler) fail(c *gin.Context, err error) {
	switch {
	case errors.Is(err, jobs.ErrConflict):
		h.nodes.fail(c, apicontract.NewError(apicontract.StateConflict))
	case errors.Is(err, jobs.ErrInvalidInput):
		h.nodes.fail(c, apicontract.NewError(apicontract.MalformedRequest))
	default:
		h.nodes.fail(c, err)
	}
}

func (h *sourceHandler) document(c *gin.Context, status int, document source.Document, items []apicontract.SourceItem) {
	response, err := apicontract.NewSourceResponse(apicontract.RequestID(c.Request.Context()), document)
	if err != nil {
		h.fail(c, err)
		return
	}
	if len(items) > 0 {
		response.Data.Items = items
	}
	etag, err := apicontract.ETag(document.Metadata.Revision)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.Header("ETag", etag)
	h.nodes.response(c, status, "SourceResponse", response)
}

func (h *sourceHandler) create(c *gin.Context) {
	var request apicontract.SourceCreateRequest
	if !h.nodes.readRequest(c, "SourceCreateRequest", &request) {
		return
	}
	cfg, err := request.Config()
	if err != nil {
		h.fail(c, err)
		return
	}
	enabled, tags := apicontract.EnabledTags(request)
	session, _ := SessionFromContext(c.Request.Context())
	document, err := h.store.Create(c.Request.Context(), source.Mutation{ScopeID: nodeScope(c), PrincipalID: session.User.ID,
		RequestID: apicontract.RequestID(c.Request.Context()), Name: request.Name, Tags: tags, Enabled: enabled, Config: cfg})
	if err != nil {
		h.fail(c, err)
		return
	}
	h.document(c, http.StatusCreated, document, nil)
}

func (h *sourceHandler) get(c *gin.Context) {
	id, ok := h.nodes.id(c)
	if !ok {
		return
	}
	document, err := h.store.Head(c.Request.Context(), nodeScope(c), id)
	if err != nil {
		h.fail(c, err)
		return
	}
	raw, err := h.store.Items(c.Request.Context(), nodeScope(c), id)
	if err != nil {
		h.fail(c, err)
		return
	}
	items := make([]apicontract.SourceItem, 0, len(raw))
	for _, item := range raw {
		items = append(items, sourceItemDTO(item))
	}
	h.document(c, http.StatusOK, document, items)
}

func (h *sourceHandler) patch(c *gin.Context) {
	id, ok := h.nodes.id(c)
	if !ok {
		return
	}
	expected, ok := h.nodes.expected(c)
	if !ok {
		return
	}
	var request apicontract.SourcePatchRequest
	if !h.nodes.readRequest(c, "SourcePatchRequest", &request) {
		return
	}
	old, err := h.store.Head(c.Request.Context(), nodeScope(c), id)
	if err != nil {
		h.fail(c, err)
		return
	}
	if old.Metadata.Revision != expected {
		h.fail(c, catalog.ErrRevisionConflict)
		return
	}
	cfg, name, tags, enabled, err := request.Merge(old)
	if err != nil {
		h.fail(c, err)
		return
	}
	session, _ := SessionFromContext(c.Request.Context())
	document, err := h.store.Update(c.Request.Context(), source.Mutation{ScopeID: nodeScope(c), PrincipalID: session.User.ID,
		ResourceID: id, RequestID: apicontract.RequestID(c.Request.Context()), Name: name, Tags: tags, Enabled: enabled, Config: cfg, ExpectedRevision: expected})
	if err != nil {
		h.fail(c, err)
		return
	}
	h.document(c, http.StatusOK, document, nil)
}

func (h *sourceHandler) delete(c *gin.Context) {
	id, ok := h.nodes.id(c)
	if !ok {
		return
	}
	expected, ok := h.nodes.expected(c)
	if !ok || !h.nodes.emptyBody(c) {
		return
	}
	session, _ := SessionFromContext(c.Request.Context())
	document, err := h.store.Delete(c.Request.Context(), source.Mutation{ScopeID: nodeScope(c), PrincipalID: session.User.ID,
		ResourceID: id, RequestID: apicontract.RequestID(c.Request.Context()), ExpectedRevision: expected})
	if err != nil {
		h.fail(c, err)
		return
	}
	etag, _ := apicontract.ETag(document.Metadata.Revision)
	c.Header("ETag", etag)
	h.nodes.response(c, http.StatusOK, "MutationResponse", apicontract.MutationResponse{RequestID: apicontract.RequestID(c.Request.Context()),
		Data: apicontract.MutationReceipt{ResourceID: id, Revision: apicontract.Revision(document.Metadata.Revision),
			SecurityEpoch: apicontract.Revision(document.Metadata.SecurityEpoch), Status: catalog.ReceiptDeleted}})
}

func (h *sourceHandler) refresh(c *gin.Context) {
	id, ok := h.nodes.id(c)
	if !ok {
		return
	}
	expected, ok := h.nodes.expected(c)
	if !ok || !h.nodes.emptyBody(c) {
		return
	}
	session, _ := SessionFromContext(c.Request.Context())
	input := source.RefreshRequest{ScopeID: nodeScope(c), PrincipalID: session.User.ID, SourceID: id,
		RequestID: apicontract.RequestID(c.Request.Context()), ExpectedRevision: expected}
	if len(c.Request.Header.Values("Idempotency-Key")) != 0 {
		metadata, err := apicontract.ReadIdempotency(c.Request.Header, session.User.ScopeID, session.User.ID, "sources.refresh")
		if err != nil {
			h.fail(c, err)
			return
		}
		input.IdempotencyKey = metadata.Key
	}
	job, replayed, err := h.store.EnqueueRefresh(c.Request.Context(), input)
	if err != nil {
		h.fail(c, err)
		return
	}
	if replayed {
		c.Header("Idempotency-Replayed", "true")
	}
	etag, err := apicontract.ETag(job.Revision)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.Header("ETag", etag)
	h.nodes.response(c, http.StatusAccepted, "JobResponse", apicontract.JobResponse{RequestID: apicontract.RequestID(c.Request.Context()), Data: jobRead(job)})
}

func (h *sourceHandler) list(c *gin.Context) {
	values, err := nodeQueries(c, "limit", "cursor", "tag", "enabled")
	if err != nil {
		h.fail(c, err)
		return
	}
	pagination, err := apicontract.ParsePagination(values)
	if err != nil {
		h.fail(c, err)
		return
	}
	options := catalog.SourceListOptions{Limit: pagination.Limit, Tag: values.Get("tag")}
	if utf8.RuneCountInString(options.Tag) > 64 || (values.Has("tag") && options.Tag == "") {
		h.fail(c, apicontract.NewError(apicontract.MalformedRequest))
		return
	}
	if values.Has("enabled") {
		value := values.Get("enabled")
		if value != "true" && value != "false" {
			h.fail(c, apicontract.NewError(apicontract.MalformedRequest))
			return
		}
		enabled := value == "true"
		options.Enabled = &enabled
	}
	filters := url.Values{}
	for _, field := range []string{"tag", "enabled"} {
		if value := values.Get(field); value != "" {
			filters.Set(field, value)
		}
	}
	binding, err := nodeBinding(nodeScope(c), "sources", apicontract.CreatedAtIDSort, filters)
	if err != nil {
		h.fail(c, err)
		return
	}
	if pagination.Cursor != "" {
		position, err := h.nodes.cursor.Decode(pagination.Cursor, binding)
		if err != nil {
			h.fail(c, err)
			return
		}
		options.After = &catalog.Position{CreatedAt: position.CreatedAt, ID: position.ID}
	}
	page, err := h.store.List(c.Request.Context(), nodeScope(c), options)
	if err != nil {
		h.fail(c, err)
		return
	}
	info := apicontract.PageInfo{Limit: pagination.Limit}
	if page.Next != nil {
		info.NextCursor, err = h.nodes.cursor.Encode(binding, apicontract.CursorPosition{CreatedAt: page.Next.CreatedAt, ID: page.Next.ID})
		if err != nil {
			h.fail(c, err)
			return
		}
	}
	response, err := apicontract.NewSourceListResponse(apicontract.RequestID(c.Request.Context()), page.Items, info)
	if err != nil {
		h.fail(c, err)
		return
	}
	h.nodes.response(c, http.StatusOK, "SourceListResponse", response)
}
