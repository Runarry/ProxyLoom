package server

import (
	"context"
	"errors"
	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/identity"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/subscriptions"
	"github.com/gin-gonic/gin"
	"net/http"
	"net/url"
	"slices"
	"time"
)

type subscriptionHandler struct {
	*nodeHandler
	publications  subscriptions.Repository
	listResources func(context.Context, ir.ID, catalog.RoutingListOptions) (catalog.RoutingPage, error)
}

func mountSubscriptions(router *gin.Engine, auth *Authentication, nodes NodeDependencies, store subscriptions.Repository) error {
	c, ok := nodes.Repository.(interface {
		ListSubscriptions(context.Context, ir.ID, catalog.RoutingListOptions) (catalog.RoutingPage, error)
	})
	if !ok || auth == nil || store == nil || nodes.Cursor == nil {
		return errors.New("subscription_dependencies_invalid")
	}
	h := &subscriptionHandler{nodeHandler: &nodeHandler{auth: auth, store: nodes.Repository, cursor: nodes.Cursor}, publications: store, listResources: c.ListSubscriptions}
	r := router.Group("/api/v1/subscriptions", auth.RequireSession())
	r.GET("", h.list)
	r.POST("", h.create)
	r.GET("/:id", h.get)
	r.PATCH("/:id", h.patch)
	r.DELETE("/:id", h.delete)
	r.POST("/:id/clone", h.clone)
	r.POST("/:id/compile", h.compile)
	r.POST("/:id/publish", h.publish)
	r.POST("/:id/rollback", h.rollback)
	r.GET("/:id/publications", h.history)
	r.GET("/:id/tokens", h.tokens)
	r.POST("/:id/tokens", auth.RequireRecentAuthentication(), auth.AuditSensitive(identity.SensitiveIssueToken), h.issueToken)
	router.GET("/api/v1/compile-batches/:id", auth.RequireSession(), h.batch)
	router.POST("/api/v1/tokens/:id/revoke", auth.RequireSession(), h.revokeToken)
	router.GET("/api/v1/cores", auth.RequireSession(), h.cores)
	router.POST("/api/v1/cores/:id/disable", auth.RequireSession(), h.disableCore)
	router.GET("/s/:token/:target_key", h.download)
	return nil
}
func (h *subscriptionHandler) fail(c *gin.Context, err error) {
	switch {
	case errors.Is(err, subscriptions.ErrObsolete):
		err = apicontract.NewError(apicontract.CompileObsolete)
	case errors.Is(err, subscriptions.ErrBlocked):
		err = apicontract.NewError(apicontract.PublicationBlocked)
	case errors.Is(err, subscriptions.ErrConfirmation):
		err = apicontract.NewError(apicontract.PreviewConfirmationRequired)
	}
	h.nodeHandler.fail(c, err)
}
func (h *subscriptionHandler) actor(c *gin.Context) (subscriptions.Actor, bool) {
	session, _ := SessionFromContext(c.Request.Context())
	a := subscriptions.Actor{ScopeID: session.User.ScopeID, ID: session.User.ID}
	if len(c.Request.Header.Values("Idempotency-Key")) > 0 {
		m, err := apicontract.ReadIdempotency(c.Request.Header, a.ScopeID, a.ID, "subscription")
		if err != nil {
			h.fail(c, err)
			return a, false
		}
		a.Key = m.Key
	}
	return a, true
}
func subCheck(r ir.Resource, expected int64) error {
	if r.Metadata.Kind != ir.KindSubscriptionProfile {
		return catalog.ErrNotFound
	}
	if expected > 0 && expected != r.Metadata.Revision {
		return catalog.ErrRevisionConflict
	}
	return nil
}
func (h *subscriptionHandler) resource(c *gin.Context, status int, r ir.Resource) {
	head, err := h.publications.Head(c.Request.Context(), nodeScope(c), r.Metadata.ResourceID)
	if err != nil {
		h.fail(c, err)
		return
	}
	etag, _ := apicontract.ETag(r.Metadata.Revision)
	c.Header("ETag", etag)
	h.response(c, status, "SubscriptionResponse", gin.H{"request_id": apicontract.RequestID(c.Request.Context()), "data": gin.H{"metadata": apicontract.SubscriptionMetadata(r), "subscription": r.Payload, "publication_head": head}})
}
func (h *subscriptionHandler) create(c *gin.Context) {
	var req apicontract.SubscriptionCreateRequest
	if !h.readRequest(c, "SubscriptionCreateRequest", &req) {
		return
	}
	in, err := req.Input()
	if err != nil {
		h.fail(c, err)
		return
	}
	r, err := h.mutate(c, "subscription_profile.create", func(tx catalog.AuditedTx) (ir.Resource, error) { return tx.Create(c.Request.Context(), in) })
	if err != nil {
		h.fail(c, err)
		return
	}
	h.resource(c, 201, r)
}
func (h *subscriptionHandler) get(c *gin.Context) {
	id, ok := h.id(c)
	if !ok {
		return
	}
	r, err := h.store.Head(c.Request.Context(), nodeScope(c), id)
	if err == nil {
		err = subCheck(r, 0)
	}
	if err != nil {
		h.fail(c, err)
		return
	}
	h.resource(c, 200, r)
}
func (h *subscriptionHandler) patch(c *gin.Context) {
	id, ok := h.id(c)
	if !ok {
		return
	}
	expected, ok := h.expected(c)
	if !ok {
		return
	}
	var req apicontract.SubscriptionPatchRequest
	if !h.readRequest(c, "SubscriptionPatchRequest", &req) {
		return
	}
	r, err := h.mutate(c, "subscription_profile.update", func(tx catalog.AuditedTx) (ir.Resource, error) {
		old, err := tx.Head(c.Request.Context(), id)
		if err == nil {
			err = subCheck(old, expected)
		}
		if err != nil {
			return ir.Resource{}, err
		}
		in, err := req.Merge(old)
		if err != nil {
			return ir.Resource{}, err
		}
		return tx.Update(c.Request.Context(), id, expected, in)
	})
	if err != nil {
		h.fail(c, err)
		return
	}
	h.resource(c, 200, r)
}
func (h *subscriptionHandler) delete(c *gin.Context) {
	id, ok := h.id(c)
	if !ok {
		return
	}
	expected, ok := h.expected(c)
	if !ok || !h.emptyBody(c) {
		return
	}
	r, err := h.mutate(c, "subscription_profile.delete", func(tx catalog.AuditedTx) (ir.Resource, error) {
		r, err := tx.Head(c.Request.Context(), id)
		if err == nil {
			err = subCheck(r, expected)
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
	etag, _ := apicontract.ETag(r.Metadata.Revision)
	c.Header("ETag", etag)
	h.response(c, 200, "MutationResponse", apicontract.MutationResponse{RequestID: apicontract.RequestID(c.Request.Context()), Data: apicontract.MutationReceipt{ResourceID: id, Revision: apicontract.Revision(r.Metadata.Revision), SecurityEpoch: apicontract.Revision(r.Metadata.SecurityEpoch), Status: catalog.ReceiptDeleted}})
}
func (h *subscriptionHandler) clone(c *gin.Context) {
	id, ok := h.id(c)
	if !ok {
		return
	}
	expected, ok := h.expected(c)
	if !ok {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if !h.readRequest(c, "SubscriptionCloneRequest", &req) {
		return
	}
	r, err := h.mutate(c, "subscription_profile.clone", func(tx catalog.AuditedTx) (ir.Resource, error) {
		old, err := tx.Head(c.Request.Context(), id)
		if err == nil {
			err = subCheck(old, expected)
		}
		if err != nil {
			return ir.Resource{}, err
		}
		p := old.Payload.(*ir.SubscriptionProfile).Clone()
		return tx.Create(c.Request.Context(), catalog.CreateInput{Name: req.Name, Tags: append([]string{}, old.Metadata.Tags...), Enabled: old.Metadata.Enabled, Payload: &p})
	})
	if err != nil {
		h.fail(c, err)
		return
	}
	h.resource(c, 201, r)
}
func (h *subscriptionHandler) list(c *gin.Context) {
	v, err := nodeQueries(c, "limit", "cursor", "tag", "enabled")
	if err != nil {
		h.fail(c, err)
		return
	}
	page, err := apicontract.ParsePagination(v)
	if err != nil {
		h.fail(c, err)
		return
	}
	opts := catalog.RoutingListOptions{Limit: page.Limit, Tag: v.Get("tag")}
	if v.Has("enabled") {
		if v.Get("enabled") != "true" && v.Get("enabled") != "false" {
			h.fail(c, apicontract.NewError(apicontract.MalformedRequest))
			return
		}
		enabled := v.Get("enabled") == "true"
		opts.Enabled = &enabled
	}
	filters := url.Values{}
	for _, k := range []string{"tag", "enabled"} {
		if v.Has(k) {
			filters.Set(k, v.Get(k))
		}
	}
	binding, err := nodeBinding(nodeScope(c), "subscriptions", apicontract.CreatedAtIDSort, filters)
	if err != nil {
		h.fail(c, err)
		return
	}
	if page.Cursor != "" {
		p, err := h.cursor.Decode(page.Cursor, binding)
		if err != nil {
			h.fail(c, err)
			return
		}
		opts.After = &catalog.Position{CreatedAt: p.CreatedAt, ID: p.ID}
	}
	resources, err := h.listResources(c.Request.Context(), nodeScope(c), opts)
	if err != nil {
		h.fail(c, err)
		return
	}
	data := []any{}
	for _, r := range resources.Items {
		head, err := h.publications.Head(c.Request.Context(), nodeScope(c), r.Metadata.ResourceID)
		if err != nil {
			h.fail(c, err)
			return
		}
		data = append(data, gin.H{"metadata": apicontract.SubscriptionMetadata(r), "subscription": r.Payload, "publication_head": head})
	}
	info := apicontract.PageInfo{Limit: page.Limit}
	if resources.Next != nil {
		info.NextCursor, err = h.cursor.Encode(binding, apicontract.CursorPosition{CreatedAt: resources.Next.CreatedAt, ID: resources.Next.ID})
		if err != nil {
			h.fail(c, err)
			return
		}
	}
	h.response(c, 200, "SubscriptionListResponse", gin.H{"request_id": apicontract.RequestID(c.Request.Context()), "data": data, "page": info})
}
func (h *subscriptionHandler) compile(c *gin.Context) {
	id, ok := h.id(c)
	if !ok {
		return
	}
	rev, ok := h.expected(c)
	if !ok {
		return
	}
	a, ok := h.actor(c)
	if !ok {
		return
	}
	var req subscriptions.CompileRequest
	if !h.readRequest(c, "CompileRequest", &req) {
		return
	}
	b, err := h.publications.Compile(c.Request.Context(), a, id, rev, req)
	if err != nil {
		h.fail(c, err)
		return
	}
	h.result(c, 202, "CompileBatchResponse", b, int64(b.Revision))
}
func (h *subscriptionHandler) batch(c *gin.Context) {
	id, ok := h.id(c)
	if !ok {
		return
	}
	a, ok := h.actor(c)
	if !ok {
		return
	}
	b, err := h.publications.GetBatch(c.Request.Context(), a, id)
	if err != nil {
		h.fail(c, err)
		return
	}
	h.result(c, 200, "CompileBatchResponse", b, int64(b.Revision))
}
func (h *subscriptionHandler) publish(c *gin.Context) {
	id, ok := h.id(c)
	if !ok {
		return
	}
	rev, ok := h.expected(c)
	if !ok {
		return
	}
	a, ok := h.actor(c)
	if !ok {
		return
	}
	var req subscriptions.PublishRequest
	if !h.readRequest(c, "PublishRequest", &req) {
		return
	}
	p, err := h.publications.Publish(c.Request.Context(), a, id, rev, req)
	if err != nil {
		h.fail(c, err)
		return
	}
	h.result(c, 201, "PublicationResponse", p, 0)
}
func (h *subscriptionHandler) rollback(c *gin.Context) {
	id, ok := h.id(c)
	if !ok {
		return
	}
	rev, ok := h.expected(c)
	if !ok {
		return
	}
	a, ok := h.actor(c)
	if !ok {
		return
	}
	var req subscriptions.RollbackRequest
	if !h.readRequest(c, "RollbackRequest", &req) {
		return
	}
	p, err := h.publications.Rollback(c.Request.Context(), a, id, rev, req)
	if err != nil {
		h.fail(c, err)
		return
	}
	h.result(c, 201, "PublicationResponse", p, 0)
}
func (h *subscriptionHandler) result(c *gin.Context, status int, schema string, data any, rev int64) {
	if rev > 0 {
		etag, _ := apicontract.ETag(rev)
		c.Header("ETag", etag)
	}
	h.response(c, status, schema, gin.H{"request_id": apicontract.RequestID(c.Request.Context()), "data": data})
}
func (h *subscriptionHandler) issueToken(c *gin.Context) {
	id, ok := h.id(c)
	if !ok {
		return
	}
	a, ok := h.actor(c)
	if !ok {
		return
	}
	var req subscriptions.TokenRequest
	if !h.readRequest(c, "TokenCreateRequest", &req) {
		return
	}
	t, err := h.publications.IssueToken(c.Request.Context(), a, id, req)
	if err != nil {
		h.fail(c, err)
		return
	}
	h.result(c, 201, "TokenIssueResponse", t, 0)
}
func (h *subscriptionHandler) revokeToken(c *gin.Context) {
	id, ok := h.id(c)
	if !ok {
		return
	}
	rev, ok := h.expected(c)
	if !ok {
		return
	}
	a, ok := h.actor(c)
	if !ok {
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if !h.readRequest(c, "ReasonRequest", &req) {
		return
	}
	t, err := h.publications.RevokeToken(c.Request.Context(), a, id, rev)
	if err != nil {
		h.fail(c, err)
		return
	}
	h.result(c, 200, "TokenResponse", t, int64(t.Revision))
}
func (h *subscriptionHandler) disableCore(c *gin.Context) {
	id, ok := h.id(c)
	if !ok {
		return
	}
	rev, ok := h.expected(c)
	if !ok {
		return
	}
	a, ok := h.actor(c)
	if !ok {
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if !h.readRequest(c, "ReasonRequest", &req) {
		return
	}
	core, err := h.publications.DisableCore(c.Request.Context(), a, id, rev)
	if err != nil {
		h.fail(c, err)
		return
	}
	h.result(c, 200, "CoreResponse", core, int64(core.Revision))
}

func (h *subscriptionHandler) collectionPage(c *gin.Context, collection string, profile ir.ID) (apicontract.Pagination, apicontract.CursorBinding, ir.ID, bool) {
	v, err := nodeQueries(c, "limit", "cursor")
	if err != nil {
		h.fail(c, err)
		return apicontract.Pagination{}, apicontract.CursorBinding{}, "", false
	}
	p, err := apicontract.ParsePagination(v)
	if err != nil {
		h.fail(c, err)
		return p, apicontract.CursorBinding{}, "", false
	}
	b, err := nodeBinding(nodeScope(c), collection, apicontract.CreatedAtIDSort, url.Values{"profile_id": {string(profile)}})
	if err != nil {
		h.fail(c, err)
		return p, b, "", false
	}
	var after ir.ID
	if p.Cursor != "" {
		pos, err := h.cursor.Decode(p.Cursor, b)
		if err != nil {
			h.fail(c, err)
			return p, b, "", false
		}
		after = pos.ID
	}
	return p, b, after, true
}
func (h *subscriptionHandler) history(c *gin.Context) {
	id, ok := h.id(c)
	if !ok {
		return
	}
	page, binding, after, ok := h.collectionPage(c, "publications", id)
	if !ok {
		return
	}
	items, err := h.publications.Publications(c.Request.Context(), nodeScope(c), id, after, page.Limit+1)
	if err != nil {
		h.fail(c, err)
		return
	}
	info := apicontract.PageInfo{Limit: page.Limit}
	if len(items) > page.Limit {
		items = items[:page.Limit]
		last := items[len(items)-1]
		info.NextCursor, err = h.cursor.Encode(binding, apicontract.CursorPosition{CreatedAt: last.CreatedAt, ID: last.PublicationID})
		if err != nil {
			h.fail(c, err)
			return
		}
	}
	h.response(c, 200, "PublicationListResponse", gin.H{"request_id": apicontract.RequestID(c.Request.Context()), "data": items, "page": info})
}
func (h *subscriptionHandler) tokens(c *gin.Context) {
	id, ok := h.id(c)
	if !ok {
		return
	}
	page, binding, after, ok := h.collectionPage(c, "subscription-tokens", id)
	if !ok {
		return
	}
	items, err := h.publications.Tokens(c.Request.Context(), nodeScope(c), id, after, page.Limit+1)
	if err != nil {
		h.fail(c, err)
		return
	}
	info := apicontract.PageInfo{Limit: page.Limit}
	if len(items) > page.Limit {
		items = items[:page.Limit]
		last := items[len(items)-1]
		info.NextCursor, err = h.cursor.Encode(binding, apicontract.CursorPosition{CreatedAt: last.CreatedAt, ID: last.TokenID})
		if err != nil {
			h.fail(c, err)
			return
		}
	}
	h.response(c, 200, "TokenListResponse", gin.H{"request_id": apicontract.RequestID(c.Request.Context()), "data": items, "page": info})
}
func (h *subscriptionHandler) cores(c *gin.Context) {
	v, err := nodeQueries(c, "limit", "cursor", "core_family", "enabled")
	if err != nil {
		h.fail(c, err)
		return
	}
	page, err := apicontract.ParsePagination(v)
	if err != nil {
		h.fail(c, err)
		return
	}
	filters := url.Values{}
	for _, key := range []string{"core_family", "enabled"} {
		if v.Has(key) {
			filters.Set(key, v.Get(key))
		}
	}
	binding, err := nodeBinding(nodeScope(c), "cores", apicontract.CreatedAtIDSort, filters)
	if err != nil {
		h.fail(c, err)
		return
	}
	var after apicontract.CursorPosition
	if page.Cursor != "" {
		after, err = h.cursor.Decode(page.Cursor, binding)
		if err != nil {
			h.fail(c, err)
			return
		}
	}
	items, err := h.publications.Cores(c.Request.Context())
	if err != nil {
		h.fail(c, err)
		return
	}
	slices.SortFunc(items, func(a, b subscriptions.Core) int {
		if n := a.RegisteredAt.Compare(b.RegisteredAt); n != 0 {
			return n
		}
		if a.CoreBuildID < b.CoreBuildID {
			return -1
		}
		return 1
	})
	out := []subscriptions.Core{}
	for _, item := range items {
		if v.Has("core_family") && v.Get("core_family") != string(item.CoreFamily) {
			continue
		}
		if v.Has("enabled") && ((v.Get("enabled") == "true") != item.Enabled) {
			continue
		}
		if after.ID != "" && (item.RegisteredAt.Before(after.CreatedAt) || (item.RegisteredAt.Equal(after.CreatedAt) && item.CoreBuildID <= after.ID)) {
			continue
		}
		out = append(out, item)
	}
	info := apicontract.PageInfo{Limit: page.Limit}
	if len(out) > page.Limit {
		out = out[:page.Limit]
		last := out[len(out)-1]
		info.NextCursor, err = h.cursor.Encode(binding, apicontract.CursorPosition{CreatedAt: last.RegisteredAt, ID: last.CoreBuildID})
		if err != nil {
			h.fail(c, err)
			return
		}
	}
	h.response(c, 200, "CoreListResponse", gin.H{"request_id": apicontract.RequestID(c.Request.Context()), "data": out, "page": info})
}
func (h *subscriptionHandler) download(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	d, err := h.publications.Download(ctx, c.Param("token"), c.Param("target_key"))
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	if err != nil {
		status, code := http.StatusServiceUnavailable, "SUBSCRIPTION_BLOCKED"
		if errors.Is(err, subscriptions.ErrToken) {
			status, code = 404, "RESOURCE_NOT_FOUND"
		} else if errors.Is(err, subscriptions.ErrNotReady) {
			code = "SUBSCRIPTION_NOT_READY"
		} else if errors.Is(err, catalog.ErrUnavailable) || errors.Is(err, context.DeadlineExceeded) {
			code = "SERVICE_UNAVAILABLE"
		}
		c.JSON(status, gin.H{"request_id": apicontract.RequestID(c.Request.Context()), "error": gin.H{"code": code, "message": "Subscription is unavailable.", "details": []any{}}})
		return
	}
	defer clear(d.Bytes)
	c.Data(200, d.ContentType+"; charset=utf-8", d.Bytes)
}
