// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package utils

// ServiceEndpoint returns the `endpoint` URL for the named entry inside a
// job's `properties.services` map, or "" when the entry is missing, has the
// wrong shape, or carries a non-string endpoint.
//
// The Foundry job payload models `services` as map[string]any because each
// service type (Tracking, Studio, SSH, JupyterLab, TensorBoard, ...) carries
// a slightly different schema. This helper centralizes the chain of map
// lookup + type assertions so callers don't reimplement it (and don't drift
// in their tolerance of partial responses).
//
// Returns "" rather than an error so it can be used inline by best-effort
// callers (e.g. populating a Studio URL); callers that must fail loudly
// should check for "" themselves and wrap with their own error message.
func ServiceEndpoint(services map[string]any, name string) string {
	// A nil-map read is already safe in Go (returns zero value), but the
	// explicit guard matches the surrounding codebase style and makes the
	// intent obvious to readers.
	if services == nil {
		return ""
	}
	svc, ok := services[name]
	if !ok {
		return ""
	}
	svcMap, ok := svc.(map[string]any)
	if !ok {
		return ""
	}
	endpoint, ok := svcMap["endpoint"].(string)
	if !ok {
		return ""
	}
	return endpoint
}
