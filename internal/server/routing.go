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
	"github.com/gin-gonic/gin"
)

type routingRepository interface {
	ListRoutingProfiles(context.Context, ir.ID, catalog.RoutingListOptions) (catalog.RoutingPage, error)
	ListRuleSets(context.Context, ir.ID, catalog.RoutingListOptions) (catalog.RoutingPage, error)
}

type routingHandler struct {
	*nodeHandler
	kind          ir.ResourceKind
	collection    string
	schema        string
	listResources func(context.Context, ir.ID, catalog.RoutingListOptions) (catalog.RoutingPage, error)
}

func mountRoutingProfiles(router *gin.Engine, auth *Authentication, dependencies NodeDependencies) error {
	return mountRoutingResource(router, auth, dependencies, ir.KindRoutingProfile)
}
func mountRuleSets(router *gin.Engine, auth *Authentication, dependencies NodeDependencies) error {
	return mountRoutingResource(router, auth, dependencies, ir.KindRuleSet)
}
func mountRoutingResource(router *gin.Engine, auth *Authentication, dependencies NodeDependencies, kind ir.ResourceKind) error {
	store, ok := dependencies.Repository.(routingRepository)
	if router == nil || auth == nil || !ok || dependencies.Cursor == nil {
		return errors.New("routing_dependencies_invalid")
	}
	h := &routingHandler{nodeHandler: &nodeHandler{auth: auth, store: dependencies.Repository, cursor: dependencies.Cursor}, kind: kind}
	if kind == ir.KindRoutingProfile {
		h.collection, h.schema, h.listResources = "routing-profiles", "RoutingProfile", store.ListRoutingProfiles
	} else {
		h.collection, h.schema, h.listResources = "rule-sets", "RuleSet", store.ListRuleSets
	}
	routes := router.Group("/api/v1/"+h.collection, auth.RequireSession())
	routes.GET("", h.list)
	routes.POST("", h.create)
	routes.GET("/:id", h.get)
	routes.PATCH("/:id", h.patch)
	routes.DELETE("/:id", h.delete)
	return nil
}

func (h *routingHandler) checked(resource ir.Resource, expected int64) error {
	if resource.Metadata.Kind != h.kind {
		return catalog.ErrNotFound
	}
	if expected != 0 && resource.Metadata.Revision != expected {
		return catalog.ErrRevisionConflict
	}
	return nil
}

func requireRoutingReferences(ctx context.Context, tx catalog.AuditedTx, scope ir.ID, payload ir.ResourcePayload) error {
	profile, ok := payload.(*ir.RoutingProfile)
	if !ok {
		return nil
	}
	_, err := catalog.ResolveRoutingReferences(scope, *profile, func(id ir.ID) (ir.Resource, error) { return tx.Probe(ctx, id) })
	var diagnostics ir.Diagnostics
	if errors.As(err, &diagnostics) {
		details := make([]apicontract.Detail, 0, len(diagnostics))
		for _, diagnostic := range diagnostics {
			details = append(details, apicontract.Detail{FieldPath: "/routing_profile" + diagnostic.FieldPath, ResourceID: diagnostic.ResourceID})
		}
		return apicontract.NewError(apicontract.ValidationFailed, details...)
	}
	return err
}

func (h *routingHandler) create(c *gin.Context) {
	var input catalog.CreateInput
	var err error
	if h.kind == ir.KindRoutingProfile {
		var request apicontract.RoutingProfileCreateRequest
		if !h.readRequest(c, h.schema+"CreateRequest", &request) {
			return
		}
		input, err = request.Input()
	} else {
		var request apicontract.RuleSetCreateRequest
		if !h.readRequest(c, h.schema+"CreateRequest", &request) {
			return
		}
		input, err = request.Input()
	}
	if err != nil {
		h.fail(c, err)
		return
	}
	resource, err := h.mutate(c, catalog.MutationAction(string(h.kind)+".create"), func(tx catalog.AuditedTx) (ir.Resource, error) {
		if err := requireRoutingReferences(c.Request.Context(), tx, nodeScope(c), input.Payload); err != nil {
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

func (h *routingHandler) get(c *gin.Context) {
	id, ok := h.id(c)
	if !ok {
		return
	}
	resource, err := h.store.Head(c.Request.Context(), nodeScope(c), id)
	if err == nil {
		err = h.checked(resource, 0)
	}
	if err != nil {
		h.fail(c, err)
		return
	}
	h.resource(c, http.StatusOK, resource)
}

func (h *routingHandler) patch(c *gin.Context) {
	id, ok := h.id(c)
	if !ok {
		return
	}
	expected, ok := h.expected(c)
	if !ok {
		return
	}
	var merge func(ir.Resource) (catalog.UpdateInput, error)
	if h.kind == ir.KindRoutingProfile {
		var request apicontract.RoutingProfilePatchRequest
		if !h.readRequest(c, h.schema+"PatchRequest", &request) {
			return
		}
		merge = request.Merge
	} else {
		var request apicontract.RuleSetPatchRequest
		if !h.readRequest(c, h.schema+"PatchRequest", &request) {
			return
		}
		merge = request.Merge
	}
	resource, err := h.mutate(c, catalog.MutationAction(string(h.kind)+".update"), func(tx catalog.AuditedTx) (ir.Resource, error) {
		old, err := tx.Head(c.Request.Context(), id)
		if err == nil {
			err = h.checked(old, expected)
		}
		if err != nil {
			return ir.Resource{}, err
		}
		input, err := merge(old)
		if err != nil {
			return ir.Resource{}, err
		}
		if err := requireRoutingReferences(c.Request.Context(), tx, nodeScope(c), input.Payload); err != nil {
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

func (h *routingHandler) delete(c *gin.Context) {
	id, ok := h.id(c)
	if !ok {
		return
	}
	expected, ok := h.expected(c)
	if !ok || !h.emptyBody(c) {
		return
	}
	resource, err := h.mutate(c, catalog.MutationAction(string(h.kind)+".delete"), func(tx catalog.AuditedTx) (ir.Resource, error) {
		old, err := tx.Head(c.Request.Context(), id)
		if err == nil {
			err = h.checked(old, expected)
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
	h.response(c, http.StatusOK, "MutationResponse", apicontract.MutationResponse{RequestID: apicontract.RequestID(c.Request.Context()),
		Data: apicontract.MutationReceipt{ResourceID: id, Revision: apicontract.Revision(resource.Metadata.Revision), SecurityEpoch: apicontract.Revision(resource.Metadata.SecurityEpoch), Status: catalog.ReceiptDeleted}})
}

func (h *routingHandler) resource(c *gin.Context, status int, resource ir.Resource) {
	var response any
	var err error
	requestID := apicontract.RequestID(c.Request.Context())
	if h.kind == ir.KindRoutingProfile {
		response, err = apicontract.NewRoutingProfileResponse(requestID, resource)
	} else {
		response, err = apicontract.NewRuleSetResponse(requestID, resource)
	}
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
	h.response(c, status, h.schema+"Response", response)
}

func (h *routingHandler) list(c *gin.Context) {
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
	options := catalog.RoutingListOptions{Limit: pagination.Limit, Tag: values.Get("tag")}
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
	binding, err := nodeBinding(nodeScope(c), h.collection, apicontract.CreatedAtIDSort, filters)
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
	page, err := h.listResources(c.Request.Context(), nodeScope(c), options)
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
	var response any
	requestID := apicontract.RequestID(c.Request.Context())
	if h.kind == ir.KindRoutingProfile {
		response, err = apicontract.NewRoutingProfileListResponse(requestID, page.Items, info)
	} else {
		response, err = apicontract.NewRuleSetListResponse(requestID, page.Items, info)
	}
	if err != nil {
		h.fail(c, err)
		return
	}
	h.response(c, http.StatusOK, h.schema+"ListResponse", response)
}
