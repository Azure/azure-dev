// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package models

// RegisterModelRequest is the body for PUT /models/{name}/versions/{version}.
type RegisterModelRequest struct {
	BlobURI                 string                   `json:"blobUri"`
	Description             string                   `json:"description,omitempty"`
	Tags                    map[string]string        `json:"tags,omitempty"`
	CatalogInfo             *CatalogInfo             `json:"catalogInfo,omitempty"`
	DerivedModelInformation *DerivedModelInformation `json:"derivedModelInformation,omitempty"`
}
