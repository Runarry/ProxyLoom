package server

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/identity"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/override"
	"github.com/gin-gonic/gin"
)

type NodeRepository interface {
	catalog.Repository
	ListNodes(context.Context, ir.ID, catalog.NodeListOptions) (catalog.NodePage, error)
	ListChains(context.Context, ir.ID, catalog.ChainListOptions) (catalog.ChainPage, error)
	NodeRevisions(context.Context, ir.ID, ir.ID, int64, int) (catalog.NodeRevisionPage, error)
	NodeReferences(context.Context, ir.ID, ir.ID, catalog.NodeReferenceOptions) (catalog.ReferencePage, error)
	NodeBinding(context.Context, ir.ID, ir.ID) (override.Binding, error)
	ListNodeBindings(context.Context, ir.ID, []ir.ID) (map[ir.ID]override.Binding, error)
}

type NodeDependencies struct {
	Repository NodeRepository
	Cursor     *apicontract.CursorCodec
}

type nodeHandler struct {
	auth   *Authentication
	store  NodeRepository
	cursor *apicontract.CursorCodec
}

func mountNodes(router *gin.Engine, auth *Authentication, dependencies NodeDependencies) error {
	if router == nil || auth == nil || dependencies.Repository == nil || dependencies.Cursor == nil {
		return errors.New("node_dependencies_invalid")
	}
	h := &nodeHandler{auth: auth, store: dependencies.Repository, cursor: dependencies.Cursor}
	nodes := router.Group("/api/v1/nodes", auth.RequireSession())
	nodes.GET("", h.list)
	nodes.POST("", h.create)
	nodes.POST("/batch", h.batch)
	nodes.GET("/:id", h.get)
	nodes.PATCH("/:id", h.patch)
	nodes.DELETE("/:id", h.delete)
	nodes.POST("/:id/clone", h.clone)
	nodes.GET("/:id/revisions", h.revisions)
	nodes.GET("/:id/references", h.references)
	nodes.POST("/:id/reveal", auth.RequireRecentAuthentication(), h.prepareReveal, auth.AuditSensitive(identity.SensitiveRevealSecret), h.reveal)
	return nil
}

func (h *nodeHandler) fail(c *gin.Context, err error) { h.auth.fail(c, err) }

func (h *nodeHandler) readRequest(c *gin.Context, schema string, target any) bool {
	mediaType, params, err := mime.ParseMediaType(c.Request.Header.Get("Content-Type"))
	if len(c.Request.Header.Values("Content-Type")) != 1 || err != nil || mediaType != "application/json" || (params["charset"] != "" && params["charset"] != "utf-8") {
		h.fail(c, apicontract.NewError(apicontract.MalformedRequest))
		return false
	}
	if c.Request.ContentLength > apicontract.MaxJSONBytes {
		h.fail(c, apicontract.NewError(apicontract.InputLimitExceeded))
		return false
	}
	data, err := io.ReadAll(io.LimitReader(c.Request.Body, apicontract.MaxJSONBytes+1))
	defer clear(data)
	if err != nil {
		h.fail(c, apicontract.NewError(apicontract.MalformedRequest))
		return false
	}
	if err = apicontract.Decode(data, schema, target); err != nil {
		h.fail(c, err)
		return false
	}
	return true
}

func nodeScope(c *gin.Context) ir.ID {
	session, _ := SessionFromContext(c.Request.Context())
	return session.User.ScopeID
}

func (h *nodeHandler) id(c *gin.Context) (ir.ID, bool) {
	id := ir.ID(c.Param("id"))
	if id.Validate() != nil {
		h.fail(c, apicontract.NewError(apicontract.MalformedRequest))
		return "", false
	}
	return id, true
}

func (h *nodeHandler) expected(c *gin.Context) (int64, bool) {
	expected, err := apicontract.ParseIfMatch(c.Request.Header)
	if err != nil {
		h.fail(c, err)
		return 0, false
	}
	return expected, true
}

func (h *nodeHandler) emptyBody(c *gin.Context) bool {
	data, err := io.ReadAll(io.LimitReader(c.Request.Body, 1))
	if err != nil || len(data) != 0 {
		h.fail(c, apicontract.NewError(apicontract.MalformedRequest))
		return false
	}
	return true
}

func (h *nodeHandler) response(c *gin.Context, status int, schema string, body any) {
	if apicontract.ValidateDTO(schema, body) != nil {
		h.fail(c, apicontract.NewError(apicontract.InternalError))
		return
	}
	respond(c, status, body)
}

func (h *nodeHandler) resource(c *gin.Context, status int, resource ir.Resource) {
	response, err := apicontract.NewNodeReadResponse(apicontract.RequestID(c.Request.Context()), resource)
	if err != nil {
		h.fail(c, err)
		return
	}
	if binding, err := h.store.NodeBinding(c.Request.Context(), nodeScope(c), resource.Metadata.ResourceID); err == nil {
		dto := originDTO(binding)
		response.Data.Binding = &dto
	} else if !errors.Is(err, catalog.ErrNotFound) {
		h.fail(c, err)
		return
	}
	etag, err := apicontract.ETag(resource.Metadata.Revision)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.Header("ETag", etag)
	h.response(c, status, "NodeReadResponse", response)
}

func checkedNode(resource ir.Resource, expected int64) error {
	if resource.Metadata.Kind != ir.KindNode {
		return catalog.ErrNotFound
	}
	if expected != 0 && resource.Metadata.Revision != expected {
		return catalog.ErrRevisionConflict
	}
	return nil
}

// mutate records only safe server-generated metadata. Audit failure rolls back
// the resource revision. Captured boundary errors preserve their intended HTTP
// status across the catalog's intentionally narrower persistence error mapping.
func (h *nodeHandler) mutate(c *gin.Context, action catalog.MutationAction, change func(catalog.AuditedTx) (ir.Resource, error)) (ir.Resource, error) {
	return h.mutateAction(c, &action, change)
}

func (h *nodeHandler) mutateAction(c *gin.Context, action *catalog.MutationAction, change func(catalog.AuditedTx) (ir.Resource, error)) (ir.Resource, error) {
	var resource ir.Resource
	var boundary error
	err := h.store.Transact(c.Request.Context(), nodeScope(c), func(tx catalog.Tx) error {
		audited, ok := tx.(catalog.AuditedTx)
		if !ok {
			return catalog.ErrUnavailable
		}
		var err error
		resource, err = change(audited)
		if err != nil {
			var typed *apicontract.Error
			if errors.As(err, &typed) {
				boundary = typed
				return catalog.ErrInvalidInput
			}
			return err
		}
		session, _ := SessionFromContext(c.Request.Context())
		return audited.Audit(c.Request.Context(), catalog.MutationAudit{PrincipalID: session.User.ID,
			ObjectID: resource.Metadata.ResourceID, Revision: resource.Metadata.Revision,
			RequestID: apicontract.RequestID(c.Request.Context()), Action: *action})
	})
	if err != nil && boundary != nil {
		return ir.Resource{}, boundary
	}
	return resource, err
}

func (h *nodeHandler) create(c *gin.Context) {
	var request apicontract.NodeCreateRequest
	if !h.readRequest(c, "NodeCreateRequest", &request) {
		return
	}
	input, err := request.Input()
	if err != nil {
		h.fail(c, err)
		return
	}
	// The request cannot establish unchecked Source provenance. Source import
	// operations will receive their own authorization and persistence boundary.
	if request.Node.Origin != nil {
		h.fail(c, apicontract.NewError(apicontract.ValidationFailed))
		return
	}
	resource, err := h.mutate(c, catalog.AuditNodeCreate, func(tx catalog.AuditedTx) (ir.Resource, error) {
		return tx.Create(c.Request.Context(), input)
	})
	if err != nil {
		h.fail(c, err)
		return
	}
	h.resource(c, http.StatusCreated, resource)
}

func (h *nodeHandler) get(c *gin.Context) {
	id, ok := h.id(c)
	if !ok {
		return
	}
	resource, err := h.store.Head(c.Request.Context(), nodeScope(c), id)
	if err == nil {
		err = checkedNode(resource, 0)
	}
	if err != nil {
		h.fail(c, err)
		return
	}
	h.resource(c, http.StatusOK, resource)
}

func (h *nodeHandler) patch(c *gin.Context) {
	id, ok := h.id(c)
	if !ok {
		return
	}
	expected, ok := h.expected(c)
	if !ok {
		return
	}
	var request apicontract.NodePatchRequest
	if !h.readRequest(c, "NodePatchRequest", &request) {
		return
	}
	action := catalog.AuditNodeUpdate
	resource, err := h.mutateAction(c, &action, func(tx catalog.AuditedTx) (ir.Resource, error) {
		old, err := tx.Head(c.Request.Context(), id)
		if err == nil {
			err = checkedNode(old, expected)
		}
		if err != nil {
			return ir.Resource{}, err
		}
		if origin, ok := tx.(originMutator); ok {
			if _, bindErr := origin.LoadOrigin(c.Request.Context(), old.Metadata.ResourceID); bindErr == nil {
				next, nextAction, err := applyBoundPatch(c.Request.Context(), origin, old, request)
				if err != nil {
					return ir.Resource{}, err
				}
				action = nextAction
				return next, nil
			} else if !errors.Is(bindErr, catalog.ErrNotFound) {
				return ir.Resource{}, bindErr
			}
		}
		if request.BindingRevision != nil || request.OriginAction != "" || len(request.RestoreFields) > 0 || request.SourceItemID != "" {
			return ir.Resource{}, apicontract.NewError(apicontract.ValidationFailed, apicontract.Detail{FieldPath: "/binding_revision"})
		}
		input, err := request.Merge(old)
		if err != nil {
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

func (h *nodeHandler) clone(c *gin.Context) {
	id, ok := h.id(c)
	if !ok {
		return
	}
	expected, ok := h.expected(c)
	if !ok {
		return
	}
	var request apicontract.NodeCloneRequest
	if !h.readRequest(c, "NodeCloneRequest", &request) {
		return
	}
	resource, err := h.mutate(c, catalog.AuditNodeClone, func(tx catalog.AuditedTx) (ir.Resource, error) {
		old, err := tx.Head(c.Request.Context(), id)
		if err == nil {
			err = checkedNode(old, expected)
		}
		if err != nil {
			return ir.Resource{}, err
		}
		return tx.Create(c.Request.Context(), catalog.CreateInput{Name: request.Name,
			Tags: slices.Clone(old.Metadata.Tags), Enabled: old.Metadata.Enabled, Payload: old.Payload})
	})
	if err != nil {
		h.fail(c, err)
		return
	}
	h.resource(c, http.StatusCreated, resource)
}

func (h *nodeHandler) delete(c *gin.Context) {
	id, ok := h.id(c)
	if !ok {
		return
	}
	expected, ok := h.expected(c)
	if !ok || !h.emptyBody(c) {
		return
	}
	resource, err := h.mutate(c, catalog.AuditNodeDelete, func(tx catalog.AuditedTx) (ir.Resource, error) {
		old, err := tx.Head(c.Request.Context(), id)
		if err == nil {
			err = checkedNode(old, expected)
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

func nodeQueries(c *gin.Context, allowed ...string) (url.Values, error) {
	values, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		return nil, apicontract.NewError(apicontract.MalformedRequest)
	}
	for key, items := range values {
		if !slices.Contains(allowed, key) || len(items) != 1 || !utf8.ValidString(items[0]) || strings.ContainsAny(items[0], "\x00\r\n") {
			return nil, apicontract.NewError(apicontract.MalformedRequest)
		}
	}
	return values, nil
}

func nodeBinding(scope ir.ID, collection, sort string, filters url.Values) (apicontract.CursorBinding, error) {
	hash, err := apicontract.FilterHash(filters)
	return apicontract.CursorBinding{ScopeID: scope, Collection: collection, Sort: sort, FilterHash: hash}, err
}

func (h *nodeHandler) list(c *gin.Context) {
	values, err := nodeQueries(c, "limit", "cursor", "q", "protocol", "tag", "enabled")
	if err != nil {
		h.fail(c, err)
		return
	}
	pagination, err := apicontract.ParsePagination(values)
	if err != nil {
		h.fail(c, err)
		return
	}
	options := catalog.NodeListOptions{Limit: pagination.Limit, Search: values.Get("q"), Tag: values.Get("tag"), Protocol: ir.Protocol(values.Get("protocol"))}
	if utf8.RuneCountInString(options.Search) > 256 || utf8.RuneCountInString(options.Tag) > 64 || (values.Has("tag") && options.Tag == "") {
		h.fail(c, apicontract.NewError(apicontract.MalformedRequest))
		return
	}
	if values.Has("protocol") {
		switch options.Protocol {
		case ir.Shadowsocks, ir.VMess, ir.VLESS, ir.Trojan, ir.SOCKS5, ir.HTTP:
		default:
			h.fail(c, apicontract.NewError(apicontract.MalformedRequest))
			return
		}
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
	for _, field := range []string{"q", "protocol", "tag", "enabled"} {
		if value := values.Get(field); value != "" {
			filters.Set(field, value)
		}
	}
	binding, err := nodeBinding(nodeScope(c), "nodes", apicontract.CreatedAtIDSort, filters)
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
	page, err := h.store.ListNodes(c.Request.Context(), nodeScope(c), options)
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
	ids := make([]ir.ID, 0, len(page.Items))
	for _, item := range page.Items {
		ids = append(ids, item.Metadata.ResourceID)
	}
	origins, err := h.store.ListNodeBindings(c.Request.Context(), nodeScope(c), ids)
	if err != nil {
		h.fail(c, err)
		return
	}
	dtos := map[ir.ID]apicontract.NodeOriginBinding{}
	for id, origin := range origins {
		dtos[id] = originDTO(origin)
	}
	response, err := apicontract.NewNodeListResponseWithBindings(apicontract.RequestID(c.Request.Context()), page.Items, info, dtos)
	if err != nil {
		h.fail(c, err)
		return
	}
	h.response(c, http.StatusOK, "NodeListResponse", response)
}

func (h *nodeHandler) revisions(c *gin.Context) {
	id, ok := h.id(c)
	if !ok {
		return
	}
	values, err := nodeQueries(c, "limit", "cursor")
	if err != nil {
		h.fail(c, err)
		return
	}
	pagination, err := apicontract.ParsePagination(values)
	if err != nil {
		h.fail(c, err)
		return
	}
	binding, err := nodeBinding(nodeScope(c), "nodes/revisions", apicontract.NodeRevisionSort, url.Values{"id": {string(id)}})
	if err != nil {
		h.fail(c, err)
		return
	}
	var after int64
	if pagination.Cursor != "" {
		after, err = h.cursor.DecodeNodeRevision(pagination.Cursor, binding)
		if err != nil {
			h.fail(c, err)
			return
		}
	}
	page, err := h.store.NodeRevisions(c.Request.Context(), nodeScope(c), id, after, pagination.Limit)
	if err != nil {
		h.fail(c, err)
		return
	}
	info := apicontract.PageInfo{Limit: pagination.Limit}
	if page.Next != 0 {
		info.NextCursor, err = h.cursor.EncodeNodeRevision(binding, page.Next)
		if err != nil {
			h.fail(c, err)
			return
		}
	}
	response, err := apicontract.NewNodeListResponse(apicontract.RequestID(c.Request.Context()), page.Items, info)
	if err != nil {
		h.fail(c, err)
		return
	}
	h.response(c, http.StatusOK, "NodeListResponse", response)
}

func (h *nodeHandler) references(c *gin.Context) {
	id, ok := h.id(c)
	if !ok {
		return
	}
	values, err := nodeQueries(c, "limit", "cursor", "reference_state")
	if err != nil {
		h.fail(c, err)
		return
	}
	pagination, err := apicontract.ParsePagination(values)
	if err != nil {
		h.fail(c, err)
		return
	}
	state := values.Get("reference_state")
	if !values.Has("reference_state") {
		state = "active"
	}
	if state != "active" && state != "historical" && state != "all" {
		h.fail(c, apicontract.NewError(apicontract.MalformedRequest))
		return
	}
	binding, err := nodeBinding(nodeScope(c), "nodes/references", apicontract.NodeReferenceSort, url.Values{"id": {string(id)}, "reference_state": {state}})
	if err != nil {
		h.fail(c, err)
		return
	}
	options := catalog.NodeReferenceOptions{State: state, Limit: pagination.Limit}
	if pagination.Cursor != "" {
		position, err := h.cursor.DecodeNodeReference(pagination.Cursor, binding)
		if err != nil {
			h.fail(c, err)
			return
		}
		options.After = &position
	}
	page, err := h.store.NodeReferences(c.Request.Context(), nodeScope(c), id, options)
	if err != nil {
		h.fail(c, err)
		return
	}
	response := apicontract.ReferenceListResponse{RequestID: apicontract.RequestID(c.Request.Context()), Data: make([]apicontract.ResourceReference, 0, len(page.Items)), Page: apicontract.PageInfo{Limit: pagination.Limit}}
	if page.Next != nil {
		response.Page.NextCursor, err = h.cursor.EncodeNodeReference(binding, *page.Next)
		if err != nil {
			h.fail(c, err)
			return
		}
	}
	for _, item := range page.Items {
		ref := apicontract.ResourceReference{SourceResourceID: item.SourceID, SourceRevision: apicontract.Revision(item.SourceRevision),
			SourceKind: item.SourceKind, TargetResourceID: item.TargetID, State: "historical", FieldPath: item.Path}
		if item.Current {
			ref.State = "active"
		}
		if item.TargetRevision != nil {
			revision := apicontract.Revision(*item.TargetRevision)
			ref.TargetRevision = &revision
		}
		response.Data = append(response.Data, ref)
	}
	h.response(c, http.StatusOK, "ReferenceListResponse", response)
}

func (h *nodeHandler) prepareReveal(c *gin.Context) {
	id, ok := h.id(c)
	if !ok {
		return
	}
	expected, ok := h.expected(c)
	if !ok || !h.emptyBody(c) {
		return
	}
	resource, err := h.store.Head(c.Request.Context(), nodeScope(c), id)
	if err == nil {
		err = checkedNode(resource, expected)
	}
	if err != nil {
		h.fail(c, err)
	}
}

func (h *nodeHandler) reveal(c *gin.Context) {
	// Re-read after the durable sensitive audit. A resource change during that
	// authorization step must not release the previous credential snapshot.
	id := ir.ID(c.Param("id"))
	expected, err := apicontract.ParseIfMatch(c.Request.Header)
	var resource ir.Resource
	if err == nil {
		resource, err = h.store.Head(c.Request.Context(), nodeScope(c), id)
	}
	if err == nil {
		err = checkedNode(resource, expected)
	}
	if err != nil {
		h.fail(c, err)
		return
	}
	response, err := apicontract.NewNodeRevealResponse(apicontract.RequestID(c.Request.Context()), resource)
	if err != nil {
		h.fail(c, err)
		return
	}
	etag, _ := apicontract.ETag(resource.Metadata.Revision)
	c.Header("ETag", etag)
	h.response(c, http.StatusOK, "NodeRevealResponse", response)
}
