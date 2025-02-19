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

func (e *Extension) HasCapability(capability ...CapabilityType) bool {
	for _, cap := range capability {
		found := false
		for _, existing := range e.Capabilities {
			if existing == cap {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
