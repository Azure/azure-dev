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

// siblingOwnsConnection checks for a Connection service in the same
// Foundry project. The marker key is the service key,
// not its resource name.
func siblingOwnsConnection(serviceKey, projectEndpoint string, env map[string]string) bool {
	if strings.TrimSpace(projectEndpoint) == "" {
		return false
	}
	marker := envkey.ConnectionServiceProjectEndpoint(strings.TrimSpace(serviceKey))
	declared := strings.TrimSpace(env[marker])
	return declared != "" && sameProjectEndpoint(declared, projectEndpoint)
}

// connectionsNode checks that Connection dependencies are ready
// before the agent is published.
func connectionsNode(g *promptGraph) *promptNode {
	connections := g.managed.Connections
	if len(connections) == 0 {
		return nil
	}

	var resolvedConnections []FoundryConnectionServiceIdentity
	return &promptNode{
		Kind: nodeConnection,
		ID:   "connections",
		Validate: func() error {
			resolvedConnections = make([]FoundryConnectionServiceIdentity, 0, len(connections))
			seenServiceKeys := make(map[string]struct{}, len(connections))
			for _, serviceRef := range connections {
				if strings.TrimSpace(serviceRef) == "" {
					return exterrors.Validation(
						exterrors.CodeInvalidAgentManifest,
						"connections contains an empty sibling reference",
						"set each connections entry to a local azure.ai.connection service key or resource name",
					)
				}

				identity, err := ResolveFoundryConnectionServiceIdentity(
					g.projectServices,
					serviceRef,
					g.projectRoot,
				)
				if err != nil {
					return err
				}
				if _, seen := seenServiceKeys[identity.ServiceKey]; seen {
					continue
				}
				seenServiceKeys[identity.ServiceKey] = struct{}{}
				resolvedConnections = append(resolvedConnections, identity)
			}
			return nil
		},
		Resolve: func(context.Context) error {
			for _, identity := range resolvedConnections {
				if !siblingOwnsConnection(identity.ServiceKey, g.projectEndpoint(), g.env) {
					return exterrors.Dependency(
						exterrors.CodeFoundryDependencyNotReady,
						fmt.Sprintf(
							"connection %q has not been deployed by an azure.ai.connection service",
							identity.ResourceName,
						),
						fmt.Sprintf(
							"add %q to the agent service's uses list and run 'azd deploy --all'",
							identity.ServiceKey,
						),
					)
				}
			}
			return nil
		},
	}
}
