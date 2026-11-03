// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_api

// AgentEndpointAPIVersion is the API version for hosted agent endpoint protocol and session requests.
const AgentEndpointAPIVersion = "v1"

// StandbyPoolAPIVersion is the preview management API for endpoint standby configuration.
// Invocation and session requests continue to use AgentEndpointAPIVersion.
const StandbyPoolAPIVersion = "2025-11-15-preview"

func endpointManagementAPIVersion(endpoint *AgentEndpoint, apiVersion string) string {
	if endpoint != nil && endpoint.StandbyPool != nil && endpoint.StandbyPool.Size != nil {
		return StandbyPoolAPIVersion
	}
	return apiVersion
}
