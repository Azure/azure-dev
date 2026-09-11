// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/envkey"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
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

// promptConnectionName resolves an authored sibling service reference to the
// configured Foundry connection name, falling back to the service key.
func promptConnectionName(
	serviceRef string,
	services map[string]*azdext.ServiceConfig,
) string {
	serviceRef = strings.TrimSpace(serviceRef)
	service := services[serviceRef]
	if service == nil {
		return serviceRef
	}
	props := ServiceConfigProps(service)
	if props != nil {
		if name := strings.TrimSpace(props.GetFields()["name"].GetStringValue()); name != "" {
			return name
		}
	}
	return serviceRef
}

// connectionsNode verifies that every referenced connection sibling completed
// before the agent is published.
func connectionsNode(g *promptGraph) *promptNode {
	connections := g.managed.Connections
	if len(connections) == 0 {
		return nil
	}

	return &promptNode{
		Kind: nodeConnection,
		ID:   "connections",
		Validate: func() error {
			for _, serviceRef := range connections {
				if strings.TrimSpace(serviceRef) == "" {
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
			for _, serviceRef := range connections {
				connectionName := promptConnectionName(serviceRef, g.projectServices)
				if !siblingOwnsConnection(connectionName, g.projectEndpoint(), g.env) {
					return exterrors.Dependency(
						exterrors.CodeFoundryDependencyNotReady,
						fmt.Sprintf("connection %q has not been provisioned by an azure.ai.connection service", connectionName),
						fmt.Sprintf("add %q to the agent service's uses list and run 'azd deploy --all'", serviceRef),
					)
				}
			}
			return nil
		},
	}
}
