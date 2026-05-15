// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/azure"
)

// projectConnection is the minimal slice of an Azure project connection that
// toolbox commands need: the ARM `id` (used as `project_connection_id`), the
// category (drives the tool-entry shape), the short name, and the data-plane
// `target` (becomes `server_url` on MCP tool entries).
type projectConnection struct {
	ID       string
	Category azure.ConnectionType
	Name     string
	Target   string
}

// connectionResolver is the seam that tests substitute with stubConnectionResolver.
type connectionResolver interface {
	resolveConnection(ctx context.Context, endpoint, name string) (*projectConnection, error)
}

type defaultConnectionResolver struct{}

func (defaultConnectionResolver) resolveConnection(
	ctx context.Context, endpoint, name string,
) (*projectConnection, error) {
	client, err := newProjectsClientFromEndpoint(endpoint)
	if err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("failed to build a project client for %s: %s", endpoint, err),
			"verify the project endpoint is well-formed",
		)
	}

	conn, err := client.GetConnection(ctx, name)
	if err != nil {
		if isAzureNotFound(err) {
			return nil, connectionNotFoundError(name)
		}
		return nil, exterrors.ServiceFromAzure(err, exterrors.OpResolveProjectConnection)
	}

	return &projectConnection{
		ID:       conn.ID,
		Category: conn.Type,
		Name:     conn.Name,
		Target:   conn.Target,
	}, nil
}

func connectionNotFoundError(name string) error {
	return exterrors.Validation(
		exterrors.CodeConnectionNotFound,
		fmt.Sprintf("connection %q was not found on the project", name),
		"run `azd ai connection list` to see available connections",
	)
}
