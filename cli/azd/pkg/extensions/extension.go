// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

// Extension represents an installed extension.
type Extension struct {
	Id           string           `json:"id"`
	Namespace    string           `json:"namespace"`
	Capabilities []CapabilityType `json:"capabilities,omitempty"`
	DisplayName  string           `json:"displayName"`
	Description  string           `json:"description"`
	Version      string           `json:"version"`
	Usage        string           `json:"usage"`
	Path         string           `json:"path"`
	Source       string           `json:"source"`
}
