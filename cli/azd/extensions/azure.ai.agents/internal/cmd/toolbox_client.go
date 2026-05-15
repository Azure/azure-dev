// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"

	"azureaiagent/internal/pkg/azure"
)

// toolboxClient is the subset of *azure.FoundryToolboxClient that the toolbox
// command implementations rely on. Defining it as an interface lets unit tests
// inject mock implementations without spinning up an HTTP server.
//
// The real *azure.FoundryToolboxClient satisfies this interface directly.
type toolboxClient interface {
	GetToolbox(ctx context.Context, name string) (*azure.ToolboxObject, error)
	CreateToolboxVersion(
		ctx context.Context, name string, req *azure.CreateToolboxVersionRequest,
	) (*azure.ToolboxVersionObject, error)
	DeleteToolbox(ctx context.Context, name string) error

	ListToolboxes(ctx context.Context) ([]azure.ToolboxObject, error)
	GetToolboxVersion(
		ctx context.Context, name, version string,
	) (*azure.ToolboxVersionObject, error)
	ListToolboxVersions(
		ctx context.Context, name string,
	) ([]azure.ToolboxVersionObject, error)
	DeleteToolboxVersion(ctx context.Context, name, version string) error
	SetDefaultVersion(
		ctx context.Context, name, version string,
	) (*azure.ToolboxObject, error)

	// Endpoint returns the project endpoint root this client is bound to.
	// Used by `toolbox show` to compute the runtime MCP consumption URL.
	Endpoint() string
}

// compile-time guard: the real client must satisfy the interface.
var _ toolboxClient = (*azure.FoundryToolboxClient)(nil)
