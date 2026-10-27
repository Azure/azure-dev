// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import "regexp"

// AgentHostingInput contains the supported customer resource selections.
type AgentHostingInput struct {
	ClusterResourceID                   string
	StorageAccountResourceID            string
	HostingManagementIdentityResourceID string
	WorkloadIdentityResourceID          string
}

func resolveOptionalHostingValue(
	value string, field string, env map[string]string, resolve bool, pattern *regexp.Regexp,
) (string, error) {
	if value == "" {
		return "", nil
	}
	return resolveAgentHostingValue(value, field, env, resolve, pattern)
}
