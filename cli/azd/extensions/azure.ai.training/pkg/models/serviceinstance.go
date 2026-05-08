// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package models

// ServiceInstance represents the response from the AML history serviceinstances API.
// GET .../history/v1.0/{workspace}/runs/{runId}/serviceinstances/{nodeIndex}
type ServiceInstance struct {
	Instances map[string]ServiceInstanceDetail `json:"instances"`
}

// ServiceInstanceDetail describes a single service running on a node.
type ServiceInstanceDetail struct {
	Type       string                 `json:"type"`   // e.g., "SSH"
	Status     string                 `json:"status"` // e.g., "Running"
	Endpoint   string                 `json:"endpoint,omitempty"`
	Port       int                    `json:"port,omitempty"`
	Properties map[string]interface{} `json:"properties,omitempty"` // contains ProxyEndpoint
}
