// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package models

// ComputeResource represents an ARM compute resource returned by the control plane.
type ComputeResource struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
