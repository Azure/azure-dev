// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"azureaiagent/internal/cmd/nextstep"
	"azureaiagent/internal/pkg/paths"
	projectpkg "azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
)

// projectAgentService is an agent already declared in the project manifest.
// ServiceName is the key under services:; AgentName is the agent's own name from
// the definition, which may differ from the service key.
type projectAgentService struct {
	ServiceName string
	AgentName   string
	// RelativePath is the configured service source directory. It lets a
	// positional `.` from that directory reuse the owning service rather than
	// falling through to bare agent.yaml reuse.
	RelativePath string
}

type projectAgentDetection struct {
	services    []projectAgentService
	projectRoot string
}

// detectProjectAgentServices returns the agent services the azd host reports for
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
func detectProjectAgentServices(ctx context.Context, azdClient *azdext.AzdClient) projectAgentDetection {
	projectResponse, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		log.Printf("agent reuse: project config unavailable, continuing with normal init: %v", err)
		return projectAgentDetection{}
	}

	project := projectResponse.GetProject()
	services, diagnostics := projectAgentServicesFrom(project.GetServices(), project.GetPath())
	for _, diagnostic := range diagnostics {
		log.Printf("agent reuse: configured service is not reusable: %s", diagnostic)
	}

	return projectAgentDetection{
		services:    services,
		projectRoot: project.GetPath(),
	}
}

// projectAgentServicesFrom selects the agent services out of a project's service
// map only when their definitions resolve successfully. Invalid or missing
// definitions are returned as diagnostics so no-prompt reuse cannot report
// success for an incomplete service.
func projectAgentServicesFrom(
	services map[string]*azdext.ServiceConfig,
	projectRoot string,
) ([]projectAgentService, []string) {
	var found []projectAgentService
	var diagnostics []string
	for serviceName, svc := range services {
		if svc.GetHost() != AiAgentHost {
			continue
		}

		if _, err := paths.JoinAllowRoot(projectRoot, svc.GetRelativePath()); err != nil {
			diagnostics = append(diagnostics,
				fmt.Sprintf("service %q has invalid project path: %v", serviceName, err))
			continue
		}

		definition, _, _, err := projectpkg.LoadAgentDefinition(svc, projectRoot)
		if err != nil {
			diagnostics = append(diagnostics,
				fmt.Sprintf("service %q: %v", serviceName, err))
			continue
		}

		agentName, _ := adoptedAgentNameConfig(svc)
		if agentName == "" {
			agentName = definition.Name
		}
		if agentName == "" {
			agentName = serviceName
		}

		found = append(found, projectAgentService{
			ServiceName:  serviceName,
			AgentName:    agentName,
			RelativePath: svc.GetRelativePath(),
		})
	}

	slices.SortFunc(found, func(a, b projectAgentService) int {
		return strings.Compare(a.ServiceName, b.ServiceName)
	})

	return found, diagnostics
}

// positionalSourceOptsOutOfReuse reports whether a positional source directory
// selects something below or outside the active project root.
//
// `azd ai agent init .` from the project root is a documented way to initialize
// the current project and may reuse its configured agent. A positional
// `./agents/new`, however, explicitly selects source for a new agent and must
// not be silently ignored by reuse.
func positionalSourceOptsOutOfReuse(
	src string,
	projectRoot string,
	services []projectAgentService,
) bool {
	if src == "" {
		return false
	}
	if projectRoot == "" {
		return true
	}

	srcPath, err := filepath.Abs(src)
	if err != nil {
		return true
	}
	rootPath, err := filepath.Abs(projectRoot)
	if err != nil {
		return true
	}

	srcInfo, err := os.Stat(srcPath)
	if err != nil {
		return true
	}
	rootInfo, err := os.Stat(rootPath)
	if err != nil {
		return true
	}

	if os.SameFile(srcInfo, rootInfo) {
		return false
	}

	for _, service := range services {
		if service.RelativePath == "" {
			continue
		}
		servicePath, err := paths.JoinAllowRoot(projectRoot, service.RelativePath)
		if err != nil {
			continue
		}
		serviceInfo, err := os.Stat(servicePath)
		if err == nil && os.SameFile(srcInfo, serviceInfo) {
			return false
		}
	}

	return true
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
//
// The caller reaches this function only after detectProjectAgentServices has
// loaded the project through the azd host, so no project setup is needed here.
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

	state, _ := nextstep.AssembleState(ctx, azdClient, nextstep.WithEnvironment(env.Name))
	_ = printAllNextIfTerminal(os.Stdout, nextstep.ResolveAfterInit(state, readmeExistsForProject(ctx, azdClient)))

	return nil
}
