package apicontract

import "github.com/Runarry/ProxyLoom/internal/ir"

type ClientPresetResource struct {
	Metadata ResourceMetadata `json:"metadata"`
	Preset   ir.ClientPreset  `json:"preset"`
}

type ClientPresetListResponse struct {
	RequestID string                 `json:"request_id"`
	Data      []ClientPresetResource `json:"data"`
	Page      PageInfo               `json:"page"`
}

func NewClientPresetListResponse(requestID string, items []ir.Resource, page PageInfo) (ClientPresetListResponse, error) {
	response := ClientPresetListResponse{RequestID: safeRequestID(requestID), Data: make([]ClientPresetResource, 0, len(items)), Page: page}
	for _, resource := range items {
		if resource.Validate() != nil {
			return ClientPresetListResponse{}, NewError(InternalError)
		}
		preset, ok := resource.Payload.(*ir.ClientPreset)
		if !ok {
			return ClientPresetListResponse{}, NewError(InternalError)
		}
		response.Data = append(response.Data, ClientPresetResource{Metadata: metadata(resource.Metadata), Preset: preset.Clone()})
	}
	return response, nil
}
