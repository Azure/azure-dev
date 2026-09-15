// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"

type (
	// Layer is the preview project layer contract.
	Layer = v1beta.Layer
	// SetLayerRequest is the preview request for creating or replacing a project layer.
	SetLayerRequest = v1beta.SetLayerRequest
	// GetLayerRequest is the preview request for reading a project layer.
	GetLayerRequest = v1beta.GetLayerRequest
	// LayerResponse is the preview response containing a project layer.
	LayerResponse = v1beta.LayerResponse
	// ListLayersResponse is the preview response containing project layers.
	ListLayersResponse = v1beta.ListLayersResponse
	// RemoveLayerRequest is the preview request for deleting a project layer.
	RemoveLayerRequest = v1beta.RemoveLayerRequest
	// RemoveLayerResponse is the preview response from deleting a project layer.
	RemoveLayerResponse = v1beta.RemoveLayerResponse
)
