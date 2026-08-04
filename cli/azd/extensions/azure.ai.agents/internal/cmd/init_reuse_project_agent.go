// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"log"
	"os"
	"slices"
	"strings"

	"azureaiagent/internal/cmd/nextstep"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
)

// projectAgentService is an agent already declared in the project manifest.
// ServiceName is the key under services:; AgentName is the agent's own name from
// the definition, which may differ from the service key.
type projectAgentService struct {
	ServiceName string
	AgentName   string
}

// findProjectAgentServices returns the agent services the azd host reports for
// the current project, sorted by service name so output is deterministic.
//
// Project discovery is left to the host: it resolves the manifest by walking up
// from the working directory and accepts both azure.yaml and azure.yml, so this
// sees exactly the manifest every other azd command does, including when init
// runs from a subdirectory of the project.
//
// The agent definition is carried inline on the service entry in the unified
// format and nested under config: in older projects; adoptedAgentNameConfig
// resolves the name from either shape.
//
// A project that cannot be loaded (none present, or a manifest azd rejects)
// yields no detections, so init falls through to its normal prompts rather than
// hard-failing on a file the user has not been asked about yet. The cause is
// logged so --debug still surfaces a typo'd manifest.
func findProjectAgentServices(ctx context.Context, azdClient *azdext.AzdClient) []projectAgentService {
	projectResponse, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		log.Printf("agent reuse: project config unavailable, continuing with normal init: %v", err)
		return nil
	}

	return projectAgentServicesFrom(projectResponse.GetProject().GetServices())
}

// projectAgentServicesFrom selects the agent services out of a project's service
// map, resolving each display name and sorting by service name.
func projectAgentServicesFrom(services map[string]*azdext.ServiceConfig) []projectAgentService {
	var found []projectAgentService
	for serviceName, svc := range services {
		if svc.GetHost() != AiAgentHost {
			continue
		}

		agentName, _ := adoptedAgentNameConfig(svc)
		if agentName == "" {
			agentName = serviceName
		}

		found = append(found, projectAgentService{ServiceName: serviceName, AgentName: agentName})
	}

	slices.SortFunc(found, func(a, b projectAgentService) int {
		return strings.Compare(a.ServiceName, b.ServiceName)
	})

	return found
}

// describeProjectAgentServices renders the detected agent services for a prompt
// or status line, e.g. `"chat" (agent: my-chat-agent)`.
func describeProjectAgentServices(services []projectAgentService) string {
	parts := make([]string, 0, len(services))
	for _, svc := range services {
		if svc.AgentName != "" && svc.AgentName != svc.ServiceName {
			parts = append(parts, fmt.Sprintf("%q (agent: %s)", svc.ServiceName, svc.AgentName))
		} else {
			parts = append(parts, fmt.Sprintf("%q", svc.ServiceName))
		}
	}
	return strings.Join(parts, ", ")
}

// runReuseProjectAgentServices completes init for a project whose manifest
// already declares its agents, without re-asking for the values the manifest
// already answers (agent name, protocols, deploy mode, ...).
//
// The definitions already live in the project manifest, so there is nothing to
// write: this ensures an azd environment exists and then hands off to the shared
// next-step resolver. It mirrors runReuseDefinition (issue #7268), which does
// the same for a bare on-disk agent.yaml; the unified format moved the
// definition inline, and this is the inline equivalent.
func runReuseProjectAgentServices(
	ctx context.Context,
	flags *initFlags,
	azdClient *azdext.AzdClient,
	services []projectAgentService,
) error {
	fmt.Println(color.HiBlackString(
		"Detected existing agent configuration: %s.",
		describeProjectAgentServices(services),
	))

	if _, err := ensureProject(ctx, flags, azdClient, "."); err != nil {
		return err
	}

	env := getExistingEnvironment(ctx, flags.env, azdClient)
	if env == nil {
		envName := flags.env
		if envName == "" {
			envName = sanitizeAgentName(services[0].AgentName + "-dev")
		}
		var err error
		env, err = createNewEnvironment(ctx, azdClient, envName)
		if err != nil {
			return fmt.Errorf("failed to create azd environment: %w", err)
		}
		flags.env = env.Name
	}

	fmt.Println(color.HiBlackString("Reusing the agent configuration already in this project."))

	state, _ := nextstep.AssembleState(ctx, azdClient)
	_ = printAllNextIfTerminal(os.Stdout, nextstep.ResolveAfterInit(state, readmeExistsForProject(ctx, azdClient)))

	return nil
}
