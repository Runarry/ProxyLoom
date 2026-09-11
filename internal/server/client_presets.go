package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/gin-gonic/gin"
)

type clientPresetRepository interface {
	ListClientPresets(context.Context, ir.ID, catalog.ClientPresetListOptions) (catalog.ClientPresetPage, error)
}

type clientPresetHandler struct {
	*nodeHandler
	listStore clientPresetRepository
}

func mountClientPresets(router *gin.Engine, auth *Authentication, dependencies NodeDependencies) error {
	store, ok := dependencies.Repository.(clientPresetRepository)
	if router == nil || auth == nil || !ok || dependencies.Cursor == nil {
		return errors.New("client_preset_dependencies_invalid")
	}
	h := &clientPresetHandler{nodeHandler: &nodeHandler{auth: auth, store: dependencies.Repository, cursor: dependencies.Cursor}, listStore: store}
	router.GET("/api/v1/client-presets", auth.RequireSession(), h.list)
	return nil
}

func (h *clientPresetHandler) list(c *gin.Context) {
	values, err := nodeQueries(c, "limit", "cursor", "core_family", "platform")
	if err != nil {
		h.fail(c, err)
		return
	}
	pagination, err := apicontract.ParsePagination(values)
	if err != nil {
		h.fail(c, err)
		return
	}
	options := catalog.ClientPresetListOptions{Limit: pagination.Limit, CoreFamily: ir.CoreFamily(values.Get("core_family")), Platform: values.Get("platform")}
	if values.Has("core_family") && options.CoreFamily != ir.Xray && options.CoreFamily != ir.SingBox && options.CoreFamily != ir.Mihomo {
		h.fail(c, apicontract.NewError(apicontract.MalformedRequest))
		return
	}
	if values.Has("platform") {
		switch options.Platform {
		case "linux", "windows", "macos", "android", "ios":
		default:
			h.fail(c, apicontract.NewError(apicontract.MalformedRequest))
			return
		}
	}
	filters := url.Values{}
	for _, field := range []string{"core_family", "platform"} {
		if value := values.Get(field); value != "" {
			filters.Set(field, value)
		}
	}
	binding, err := nodeBinding(nodeScope(c), "client-presets", apicontract.CreatedAtIDSort, filters)
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
	page, err := h.listStore.ListClientPresets(c.Request.Context(), nodeScope(c), options)
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
	response, err := apicontract.NewClientPresetListResponse(apicontract.RequestID(c.Request.Context()), page.Items, info)
	if err != nil {
		h.fail(c, err)
		return
	}
	h.response(c, http.StatusOK, "ClientPresetListResponse", response)
}
