// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/envkey"
)

// siblingOwnsConnection reports whether an azure.ai.connection sibling
// provisioned name into the same Foundry project targeted by this agent.
func siblingOwnsConnection(name, projectEndpoint string, env map[string]string) bool {
	if env == nil {
		return false
	}
	declared := strings.TrimSpace(env[envkey.ConnectionProjectEndpoint])
	if declared != "" && !sameProjectEndpoint(declared, projectEndpoint) {
		return false
	}
	for entry := range strings.SplitSeq(env["AZURE_AI_PROJECT_CONNECTION_NAMES"], ",") {
		if strings.EqualFold(strings.TrimSpace(entry), strings.TrimSpace(name)) {
			return true
		}
	}
	return false
}

// connectionsNode verifies that every referenced connection sibling completed
// before the agent is published.
func connectionsNode(g *promptGraph) *promptNode {
	connections := g.managed.Connections
	if g.managed.Toolbox != nil && strings.TrimSpace(g.managed.Toolbox.Connection) != "" {
		connections = append(connections, g.managed.Toolbox.Connection)
	}
	if len(connections) == 0 {
		return nil
	}

	return &promptNode{
		Kind: nodeConnection,
		ID:   "connections",
		Validate: func() error {
			for _, name := range connections {
				if strings.TrimSpace(name) == "" {
					return exterrors.Validation(
						exterrors.CodeInvalidAgentManifest,
						"connections contains an empty sibling reference",
						"set each connections entry to an azure.ai.connection service name",
					)
				}
			}
			return nil
		},
		Resolve: func(context.Context) error {
			for _, name := range connections {
				if !siblingOwnsConnection(name, g.projectEndpoint(), g.env) {
					return exterrors.Dependency(
						exterrors.CodeFoundryDependencyNotReady,
						fmt.Sprintf("connection %q has not been provisioned by an azure.ai.connection service", name),
						fmt.Sprintf("add %q to the agent service's uses list and run 'azd deploy --all'", name),
					)
				}
			}
			return nil
		},
	}
}
