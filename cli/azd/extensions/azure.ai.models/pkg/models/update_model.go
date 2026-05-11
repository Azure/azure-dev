// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package models

// UpdateModelRequest is the body for PATCH /models/{name}/versions/{version}.
// It follows RFC 7396 JSON Merge Patch semantics:
//   - A non-nil string pointer sets/updates the field
//   - A nil *string value in Tags removes that tag key
//   - Absent keys are left unchanged
type UpdateModelRequest struct {
	Description *string            `json:"description,omitempty"`
	Tags        map[string]*string `json:"tags,omitempty"`
}
