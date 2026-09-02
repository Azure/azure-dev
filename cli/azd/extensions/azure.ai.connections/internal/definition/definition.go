// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package definition owns the source-controlled connection definition model.
package definition

// DefaultPath is the connection definition used when no explicit path is supplied.
const DefaultPath = "connection.yaml"

// Definition is the deploy-ready representation of a Foundry project connection.
type Definition struct {
	Name             string            `json:"name" yaml:"name"`
	Category         string            `json:"category" yaml:"category"`
	Target           string            `json:"target" yaml:"target"`
	AuthType         string            `json:"authType" yaml:"authType"`
	Credentials      map[string]any    `json:"credentials,omitempty" yaml:"credentials,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	Audience         string            `json:"audience,omitempty" yaml:"audience,omitempty"`
	AuthorizationURL string            `json:"authorizationUrl,omitempty" yaml:"authorizationUrl,omitempty"`
	TokenURL         string            `json:"tokenUrl,omitempty" yaml:"tokenUrl,omitempty"`
	RefreshURL       string            `json:"refreshUrl,omitempty" yaml:"refreshUrl,omitempty"`
	Scopes           []string          `json:"scopes,omitempty" yaml:"scopes,omitempty"`
	ConnectorName    string            `json:"connectorName,omitempty" yaml:"connectorName,omitempty"`
}
