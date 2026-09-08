package server

import (
	"net/http"
	"slices"

	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/gin-gonic/gin"
)

func (h *nodeHandler) batch(c *gin.Context) {
	var request apicontract.NodeBatchRequest
	if !h.readRequest(c, "NodeBatchRequest", &request) {
		return
	}
	preconditions := make(map[ir.ID]int64, len(request.Preconditions))
	for _, item := range request.Preconditions {
		if _, duplicate := preconditions[item.NodeID]; duplicate || !slices.Contains(request.NodeIDs, item.NodeID) {
			h.fail(c, apicontract.NewError(apicontract.ValidationFailed))
			return
		}
		preconditions[item.NodeID] = int64(item.Revision)
	}
	action := catalog.AuditNodeSetEnabled
	if request.Operation == "add_tags" {
		action = catalog.AuditNodeAddTags
	}
	if request.Operation == "remove_tags" {
		action = catalog.AuditNodeRemoveTags
	}
	response := apicontract.NodeBatchResponse{RequestID: apicontract.RequestID(c.Request.Context()), Data: make([]apicontract.NodeBatchItem, 0, len(request.NodeIDs))}
	for _, id := range request.NodeIDs {
		expected := preconditions[id]
		var resource ir.Resource
		var err error
		if expected == 0 {
			err = apicontract.NewError(apicontract.PreconditionRequired)
		} else {
			resource, err = h.mutate(c, action, func(tx catalog.AuditedTx) (ir.Resource, error) {
				old, err := tx.Head(c.Request.Context(), id)
				if err == nil {
					err = checkedNode(old, expected)
				}
				if err != nil {
					return ir.Resource{}, err
				}
				input := catalog.UpdateInput{Name: old.Metadata.Name, Tags: slices.Clone(old.Metadata.Tags), Enabled: old.Metadata.Enabled, Payload: old.Payload}
				switch request.Operation {
				case "set_enabled":
					input.Enabled = *request.Enabled
				case "add_tags":
					for _, tag := range request.Tags {
						if !slices.Contains(input.Tags, tag) {
							input.Tags = append(input.Tags, tag)
						}
					}
				case "remove_tags":
					input.Tags = slices.DeleteFunc(input.Tags, func(tag string) bool { return slices.Contains(request.Tags, tag) })
				}
				return tx.Update(c.Request.Context(), id, expected, input)
			})
		}
		item := apicontract.NodeBatchItem{NodeID: id, HTTPStatus: http.StatusOK}
		if err != nil {
			failure := apicontract.AsError(err)
			// Infrastructure failures fail the envelope closed. Earlier items
			// retain their committed outcomes and are safely discoverable by GET.
			if failure.HTTPStatus() >= 500 {
				h.fail(c, failure)
				return
			}
			body := failure.Response(response.RequestID).Error
			item.HTTPStatus, item.Error = failure.HTTPStatus(), &body
		} else {
			revision := apicontract.Revision(resource.Metadata.Revision)
			item.Revision = &revision
		}
		response.Data = append(response.Data, item)
	}
	h.response(c, http.StatusOK, "NodeBatchResponse", response)
}
