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

type dnsRepository interface {
	ListDNSProfiles(context.Context, ir.ID, catalog.DNSListOptions) (catalog.DNSPage, error)
}

type dnsHandler struct {
	*nodeHandler
	listStore dnsRepository
}

func mountDNSProfiles(router *gin.Engine, auth *Authentication, dependencies NodeDependencies) error {
	store, ok := dependencies.Repository.(dnsRepository)
	if router == nil || auth == nil || !ok || dependencies.Cursor == nil {
		return errors.New("dns_dependencies_invalid")
	}
	h := &dnsHandler{nodeHandler: &nodeHandler{auth: auth, store: dependencies.Repository, cursor: dependencies.Cursor}, listStore: store}
	routes := router.Group("/api/v1/dns-profiles", auth.RequireSession())
	routes.GET("", h.list)
	routes.POST("", h.create)
	routes.GET("/:id", h.get)
	routes.PATCH("/:id", h.patch)
	routes.DELETE("/:id", h.delete)
	return nil
}

func checkedDNSProfile(resource ir.Resource, expected int64) error {
	if resource.Metadata.Kind != ir.KindDNSProfile {
		return catalog.ErrNotFound
	}
	if expected != 0 && resource.Metadata.Revision != expected {
		return catalog.ErrRevisionConflict
	}
	return nil
}

func requireDNSReferences(ctx context.Context, tx catalog.AuditedTx, scope ir.ID, profile ir.DNSProfile) error {
	_, err := catalog.ResolveDNSReferences(scope, profile, func(id ir.ID) (ir.Resource, error) { return tx.Probe(ctx, id) })
	var diagnostics ir.Diagnostics
	if errors.As(err, &diagnostics) {
		details := make([]apicontract.Detail, 0, len(diagnostics))
		for _, diagnostic := range diagnostics {
			details = append(details, apicontract.Detail{FieldPath: "/dns_profile" + diagnostic.FieldPath, ResourceID: diagnostic.ResourceID})
		}
		return apicontract.NewError(apicontract.ValidationFailed, details...)
	}
	return err
}

func (h *dnsHandler) create(c *gin.Context) {
	var request apicontract.DNSProfileCreateRequest
	if !h.readRequest(c, "DNSProfileCreateRequest", &request) {
		return
	}
	input, err := request.Input()
	if err != nil {
		h.fail(c, err)
		return
	}
	resource, err := h.mutate(c, catalog.AuditDNSProfileCreate, func(tx catalog.AuditedTx) (ir.Resource, error) {
		if err := requireDNSReferences(c.Request.Context(), tx, nodeScope(c), request.DNSProfile); err != nil {
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

func (h *dnsHandler) get(c *gin.Context) {
	id, ok := h.id(c)
	if !ok {
		return
	}
	resource, err := h.store.Head(c.Request.Context(), nodeScope(c), id)
	if err == nil {
		err = checkedDNSProfile(resource, 0)
	}
	if err != nil {
		h.fail(c, err)
		return
	}
	h.resource(c, http.StatusOK, resource)
}

func (h *dnsHandler) patch(c *gin.Context) {
	id, ok := h.id(c)
	if !ok {
		return
	}
	expected, ok := h.expected(c)
	if !ok {
		return
	}
	var request apicontract.DNSProfilePatchRequest
	if !h.readRequest(c, "DNSProfilePatchRequest", &request) {
		return
	}
	resource, err := h.mutate(c, catalog.AuditDNSProfileUpdate, func(tx catalog.AuditedTx) (ir.Resource, error) {
		old, err := tx.Head(c.Request.Context(), id)
		if err == nil {
			err = checkedDNSProfile(old, expected)
		}
		if err != nil {
			return ir.Resource{}, err
		}
		input, err := request.Merge(old)
		if err != nil {
			return ir.Resource{}, err
		}
		if err := requireDNSReferences(c.Request.Context(), tx, nodeScope(c), *input.Payload.(*ir.DNSProfile)); err != nil {
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

func (h *dnsHandler) delete(c *gin.Context) {
	id, ok := h.id(c)
	if !ok {
		return
	}
	expected, ok := h.expected(c)
	if !ok || !h.emptyBody(c) {
		return
	}
	resource, err := h.mutate(c, catalog.AuditDNSProfileDelete, func(tx catalog.AuditedTx) (ir.Resource, error) {
		old, err := tx.Head(c.Request.Context(), id)
		if err == nil {
			err = checkedDNSProfile(old, expected)
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
		Data: apicontract.MutationReceipt{ResourceID: id, Revision: apicontract.Revision(resource.Metadata.Revision),
			SecurityEpoch: apicontract.Revision(resource.Metadata.SecurityEpoch), Status: catalog.ReceiptDeleted}})
}

func (h *dnsHandler) resource(c *gin.Context, status int, resource ir.Resource) {
	response, err := apicontract.NewDNSProfileResponse(apicontract.RequestID(c.Request.Context()), resource)
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
	h.response(c, status, "DNSProfileResponse", response)
}

func (h *dnsHandler) list(c *gin.Context) {
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
	options := catalog.DNSListOptions{Limit: pagination.Limit, Tag: values.Get("tag")}
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
	binding, err := nodeBinding(nodeScope(c), "dns-profiles", apicontract.CreatedAtIDSort, filters)
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
	page, err := h.listStore.ListDNSProfiles(c.Request.Context(), nodeScope(c), options)
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
	response, err := apicontract.NewDNSProfileListResponse(apicontract.RequestID(c.Request.Context()), page.Items, info)
	if err != nil {
		h.fail(c, err)
		return
	}
	h.response(c, http.StatusOK, "DNSProfileListResponse", response)
}
