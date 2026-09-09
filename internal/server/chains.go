package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"unicode/utf8"

	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/gin-gonic/gin"
)

type chainHandler struct {
	auth   *Authentication
	store  NodeRepository
	cursor *apicontract.CursorCodec
}

func mountChains(router *gin.Engine, auth *Authentication, dependencies NodeDependencies) error {
	if router == nil || auth == nil || dependencies.Repository == nil || dependencies.Cursor == nil {
		return errors.New("chain_dependencies_invalid")
	}
	h := &chainHandler{auth: auth, store: dependencies.Repository, cursor: dependencies.Cursor}
	chains := router.Group("/api/v1/chains", auth.RequireSession())
	chains.GET("", h.list)
	chains.POST("", h.create)
	chains.GET("/:id", h.get)
	chains.PATCH("/:id", h.patch)
	chains.DELETE("/:id", h.delete)
	return nil
}

func (h *chainHandler) fail(c *gin.Context, err error) { h.auth.fail(c, err) }

func (h *chainHandler) nodes() *nodeHandler {
	return &nodeHandler{auth: h.auth, store: h.store, cursor: h.cursor}
}

func checkedChain(resource ir.Resource, expected int64) error {
	if resource.Metadata.Kind != ir.KindChain {
		return catalog.ErrNotFound
	}
	if expected != 0 && resource.Metadata.Revision != expected {
		return catalog.ErrRevisionConflict
	}
	return nil
}

func requireEnabledHops(ctx context.Context, tx catalog.AuditedTx, hops []ir.NodeRef) error {
	if len(hops) != 2 {
		return apicontract.NewError(apicontract.ValidationFailed, apicontract.Detail{FieldPath: "/hops"})
	}
	details := []apicontract.Detail{}
	if hops[0].NodeID == hops[1].NodeID {
		details = append(details, apicontract.Detail{FieldPath: "/hops/1/node_id", ResourceID: hops[1].NodeID})
	}
	for i, hop := range hops {
		path := "/hops/" + strconv.Itoa(i) + "/node_id"
		resource, err := tx.Probe(ctx, hop.NodeID)
		if errors.Is(err, catalog.ErrNotFound) || errors.Is(err, catalog.ErrInvalidInput) {
			details = append(details, apicontract.Detail{FieldPath: path, ResourceID: hop.NodeID})
			continue
		}
		if err != nil {
			return err
		}
		if resource.Metadata.Kind != ir.KindNode || !resource.Metadata.Enabled {
			details = append(details, apicontract.Detail{FieldPath: path, ResourceID: hop.NodeID})
		}
	}
	if len(details) > 0 {
		return apicontract.NewError(apicontract.ValidationFailed, details...)
	}
	return nil
}

func (h *chainHandler) mutate(c *gin.Context, action catalog.MutationAction, change func(catalog.AuditedTx) (ir.Resource, error)) (ir.Resource, error) {
	return h.nodes().mutate(c, action, change)
}

func (h *chainHandler) create(c *gin.Context) {
	var request apicontract.ChainCreateRequest
	if !h.nodes().readRequest(c, "ChainCreateRequest", &request) {
		return
	}
	input, err := request.Input()
	if err != nil {
		h.fail(c, err)
		return
	}
	resource, err := h.mutate(c, catalog.AuditChainCreate, func(tx catalog.AuditedTx) (ir.Resource, error) {
		if err := requireEnabledHops(c.Request.Context(), tx, request.Hops); err != nil {
			return ir.Resource{}, err
		}
		return tx.Create(c.Request.Context(), input)
	})
	if err != nil {
		h.fail(c, err)
		return
	}
	h.resource(c, http.StatusCreated, resource)
}

func (h *chainHandler) get(c *gin.Context) {
	id, ok := h.nodes().id(c)
	if !ok {
		return
	}
	resource, err := h.store.Head(c.Request.Context(), nodeScope(c), id)
	if err == nil {
		err = checkedChain(resource, 0)
	}
	if err != nil {
		h.fail(c, err)
		return
	}
	h.resource(c, http.StatusOK, resource)
}

func (h *chainHandler) patch(c *gin.Context) {
	id, ok := h.nodes().id(c)
	if !ok {
		return
	}
	expected, ok := h.nodes().expected(c)
	if !ok {
		return
	}
	var request apicontract.ChainPatchRequest
	if !h.nodes().readRequest(c, "ChainPatchRequest", &request) {
		return
	}
	resource, err := h.mutate(c, catalog.AuditChainUpdate, func(tx catalog.AuditedTx) (ir.Resource, error) {
		old, err := tx.Head(c.Request.Context(), id)
		if err == nil {
			err = checkedChain(old, expected)
		}
		if err != nil {
			return ir.Resource{}, err
		}
		input, err := request.Merge(old)
		if err != nil {
			return ir.Resource{}, err
		}
		chain := input.Payload.(*ir.Chain)
		if err := requireEnabledHops(c.Request.Context(), tx, chain.Hops); err != nil {
			return ir.Resource{}, err
		}
		return tx.Update(c.Request.Context(), id, expected, input)
	})
	if err != nil {
		h.fail(c, err)
		return
	}
	h.resource(c, http.StatusOK, resource)
}

func (h *chainHandler) delete(c *gin.Context) {
	id, ok := h.nodes().id(c)
	if !ok {
		return
	}
	expected, ok := h.nodes().expected(c)
	if !ok || !h.nodes().emptyBody(c) {
		return
	}
	resource, err := h.mutate(c, catalog.AuditChainDelete, func(tx catalog.AuditedTx) (ir.Resource, error) {
		old, err := tx.Head(c.Request.Context(), id)
		if err == nil {
			err = checkedChain(old, expected)
		}
		if err != nil {
			return ir.Resource{}, err
		}
		return tx.Delete(c.Request.Context(), id, expected)
	})
	if err != nil {
		h.fail(c, err)
		return
	}
	etag, _ := apicontract.ETag(resource.Metadata.Revision)
	c.Header("ETag", etag)
	h.nodes().response(c, http.StatusOK, "MutationResponse", apicontract.MutationResponse{RequestID: apicontract.RequestID(c.Request.Context()),
		Data: apicontract.MutationReceipt{ResourceID: id, Revision: apicontract.Revision(resource.Metadata.Revision),
			SecurityEpoch: apicontract.Revision(resource.Metadata.SecurityEpoch), Status: catalog.ReceiptDeleted}})
}

func (h *chainHandler) resource(c *gin.Context, status int, resource ir.Resource) {
	response, err := apicontract.NewChainReadResponse(apicontract.RequestID(c.Request.Context()), resource)
	if err != nil {
		h.fail(c, err)
		return
	}
	etag, err := apicontract.ETag(resource.Metadata.Revision)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.Header("ETag", etag)
	h.nodes().response(c, status, "ChainReadResponse", response)
}

func (h *chainHandler) list(c *gin.Context) {
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
	options := catalog.ChainListOptions{Limit: pagination.Limit, Tag: values.Get("tag")}
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
	binding, err := nodeBinding(nodeScope(c), "chains", apicontract.CreatedAtIDSort, filters)
	if err != nil {
		h.fail(c, err)
		return
	}
	if pagination.Cursor != "" {
		position, err := h.cursor.Decode(pagination.Cursor, binding)
		if err != nil {
			h.fail(c, err)
			return
		}
		options.After = &catalog.Position{CreatedAt: position.CreatedAt, ID: position.ID}
	}
	page, err := h.store.ListChains(c.Request.Context(), nodeScope(c), options)
	if err != nil {
		h.fail(c, err)
		return
	}
	info := apicontract.PageInfo{Limit: pagination.Limit}
	if page.Next != nil {
		info.NextCursor, err = h.cursor.Encode(binding, apicontract.CursorPosition{CreatedAt: page.Next.CreatedAt, ID: page.Next.ID})
		if err != nil {
			h.fail(c, err)
			return
		}
	}
	response, err := apicontract.NewChainListResponse(apicontract.RequestID(c.Request.Context()), page.Items, info)
	if err != nil {
		h.fail(c, err)
		return
	}
	h.nodes().response(c, http.StatusOK, "ChainListResponse", response)
}
