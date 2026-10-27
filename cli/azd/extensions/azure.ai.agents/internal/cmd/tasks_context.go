// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

type tasksTargetOptions struct {
	projectEndpoint string
	agentName       string
	environment     string
	noPrompt        bool
	list            bool
}

// tasksProjectEndpoint applies stricter path and URL rules than the shared
// validator, which deliberately tolerates non-project paths and drops URL parts.
func tasksProjectEndpoint(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("invalid project endpoint: %w", err)
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") {
		return "", fmt.Errorf("project endpoint must not contain credentials, query, or fragment")
	}
	normalized, _, err := validateProjectEndpoint(raw)
	if err != nil {
		return "", err
	}
	// Inspect the decoded path as well, rejecting escaped separators and dot
	// segments rather than allowing them to change the resource being addressed.
	path := strings.TrimSuffix(u.Path, "/")
	project, found := strings.CutPrefix(path, projectEndpointPathPrefix)
	if !found || project == "" || project == "." || project == ".." || strings.ContainsAny(project, "/\\") {
		return "", fmt.Errorf("project endpoint must have path /api/projects/<project-name>")
	}
	canonical, _ := url.Parse(normalized)
	canonical.Path = projectEndpointPathPrefix + project
	canonical.RawPath = ""
	return canonical.String(), nil
}

func tasksAgentNameRequired(message string) error {
	return exterrors.Validation(exterrors.CodeInvalidAgentName, message,
		"deploy the agent service, or supply --agent-name <deployed-agent-name> and --project-endpoint <url>")
}

func tasksDeploymentEndpoint(values map[string]string, service string) string {
	prefix := "AGENT_" + toServiceKey(service)
	if endpoint := strings.TrimSpace(values[prefix+"_PROJECT_ENDPOINT"]); endpoint != "" {
		return endpoint
	}
	if endpoint, _, found := strings.Cut(values[prefix+"_ENDPOINT"], "/agents/"); found {
		return endpoint
	}
	return strings.TrimSpace(values["FOUNDRY_PROJECT_ENDPOINT"])
}

func resolveTasksTarget(
	ctx context.Context, host *azdext.AzdClient, opts tasksTargetOptions,
) (*AgentContext, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	explicit := opts.projectEndpoint != ""
	endpoint := ""
	var err error
	if explicit {
		endpoint, err = tasksProjectEndpoint(opts.projectEndpoint)
		if err != nil {
			return nil, err
		}
		// Task IDs are project-scoped. Show never needs local service discovery.
		if !opts.list {
			return &AgentContext{ProjectEndpoint: endpoint}, nil
		}
	}

	// Read only the selected environment; never use workspace defaults, global
	// configuration, or process environment as an endpoint fallback.
	values := map[string]string{}
	var env *azdext.EnvironmentResponse
	if host != nil {
		if opts.environment != "" {
			env, err = host.Environment().Get(ctx, &azdext.GetEnvironmentRequest{Name: opts.environment})
		} else {
			env, err = host.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
		}
		if err == nil && env != nil && env.Environment != nil && env.Environment.Name != "" {
			values, err = getAgentEnvironmentValues(ctx, host, env.Environment.Name)
		} else if err == nil {
			err = fmt.Errorf("selected azd environment is not available")
		}
	} else {
		err = fmt.Errorf("selected azd environment is not available")
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		// Explicit endpoint + name is intentionally usable outside a checkout.
		if !explicit {
			return nil, fmt.Errorf("reading selected azd environment %q: %w", opts.environment, err)
		}
		if opts.agentName == "" {
			return nil, tasksAgentNameRequired("no selected environment is available to discover deployed agents")
		}
		return &AgentContext{ProjectEndpoint: endpoint, Name: opts.agentName}, nil
	}
	if !opts.list && strings.TrimSpace(values["FOUNDRY_PROJECT_ENDPOINT"]) != "" {
		endpoint, err = tasksProjectEndpoint(values["FOUNDRY_PROJECT_ENDPOINT"])
		if err != nil {
			return nil, fmt.Errorf("no valid Foundry project endpoint in selected environment; "+
				"deploy an agent service or supply --project-endpoint: %w", err)
		}
		return &AgentContext{ProjectEndpoint: endpoint}, nil
	}

	var project *azdext.ProjectConfig
	if host != nil {
		response, projectErr := host.Project().Get(ctx, &azdext.EmptyRequest{})
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if projectErr == nil && response != nil {
			project = response.Project
		}
	}
	// Build candidates using deployed outputs in this environment, not inline
	// names or the shared deployed-name lookup (which uses the current env).
	services := map[string]*azdext.ServiceConfig{}
	if project != nil {
		for _, service := range project.Services {
			if service != nil && service.Host == AiAgentHost {
				services[service.Name] = service
			}
		}
	}
	if !opts.list {
		projects := map[string]bool{}
		for _, service := range slices.Sorted(maps.Keys(services)) {
			raw := tasksDeploymentEndpoint(values, service)
			if raw == "" {
				continue
			}
			deploymentEndpoint, endpointErr := tasksProjectEndpoint(raw)
			if endpointErr != nil {
				return nil, fmt.Errorf("invalid deployed project endpoint for service %q; "+
					"deploy the service or supply --project-endpoint: %w", service, endpointErr)
			}
			projects[deploymentEndpoint] = true
		}
		if len(projects) > 1 {
			return nil, fmt.Errorf("multiple deployed Foundry projects in selected environment; supply --project-endpoint")
		}
		for projectEndpoint := range projects {
			return &AgentContext{ProjectEndpoint: projectEndpoint}, nil
		}
		return nil, fmt.Errorf("no valid Foundry project endpoint in selected environment; " +
			"deploy an agent service or supply --project-endpoint")
	}
	name := opts.agentName
	selected := ""
	if service := services[name]; name != "" && service != nil {
		deployed := strings.TrimSpace(values["AGENT_"+toServiceKey(name)+"_NAME"])
		deploymentEndpoint, endpointErr := tasksProjectEndpoint(tasksDeploymentEndpoint(values, name))
		if !explicit || (endpointErr == nil && deploymentEndpoint == endpoint) {
			if deployed == "" {
				return nil, tasksAgentNameRequired("the selected service has no deployed agent name")
			}
			name, selected = deployed, service.Name
		}
	}
	if name == "" {
		candidates := []string{}
		for service := range services {
			if strings.TrimSpace(values["AGENT_"+toServiceKey(service)+"_NAME"]) == "" {
				continue
			}
			deploymentEndpoint, endpointErr := tasksProjectEndpoint(tasksDeploymentEndpoint(values, service))
			if explicit && (endpointErr != nil || deploymentEndpoint != endpoint) {
				continue
			}
			candidates = append(candidates, service)
		}
		slices.Sort(candidates)
		if len(candidates) == 0 {
			return nil, tasksAgentNameRequired("no deployed agent services found for this project")
		}
		index := 0
		if len(candidates) > 1 {
			if opts.noPrompt {
				return nil, tasksAgentNameRequired("multiple deployed agent services found with --no-prompt")
			}
			choices := make([]*azdext.SelectChoice, len(candidates))
			for i, service := range candidates {
				choices[i] = &azdext.SelectChoice{Label: service, Value: service}
			}
			response, err := host.Prompt().Select(ctx, &azdext.SelectRequest{Options: &azdext.SelectOptions{
				Message: "Select an agent service", Choices: choices,
			}})
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if err != nil {
				return nil, exterrors.FromPrompt(err, "selecting an agent service")
			}
			if response == nil || response.Value == nil {
				return nil, exterrors.Cancelled("agent selection cancelled")
			}
			index = int(*response.Value)
			if index < 0 || index >= len(candidates) {
				return nil, fmt.Errorf("invalid agent service selection index")
			}
		}
		selected = candidates[index]
		name = strings.TrimSpace(values["AGENT_"+toServiceKey(selected)+"_NAME"])
	}
	// Raw deployed names may also identify a local service in the selected env.
	if selected == "" && !explicit {
		projects := map[string]bool{}
		for _, service := range slices.Sorted(maps.Keys(services)) {
			if strings.TrimSpace(values["AGENT_"+toServiceKey(service)+"_NAME"]) != name {
				continue
			}
			deploymentEndpoint, endpointErr := tasksProjectEndpoint(tasksDeploymentEndpoint(values, service))
			if endpointErr != nil {
				return nil, fmt.Errorf("no valid Foundry project endpoint for service %q; "+
					"deploy the service or supply --project-endpoint: %w", service, endpointErr)
			}
			projects[deploymentEndpoint] = true
			if selected == "" {
				selected = service
			}
		}
		if len(projects) > 1 {
			return nil, fmt.Errorf("agent name %q matches multiple deployed Foundry projects; "+
				"supply --project-endpoint or use a service alias with --agent-name", name)
		}
	}
	if selected != "" {
		if err := requireSelectedServiceFeatures(ctx, services[selected], project.Path); err != nil {
			return nil, err
		}
	}
	if !explicit {
		endpoint, err = tasksProjectEndpoint(tasksDeploymentEndpoint(values, selected))
		if err != nil {
			return nil, fmt.Errorf("no valid Foundry project endpoint in selected environment; "+
				"deploy an agent service or supply --project-endpoint: %w", err)
		}
	}
	return &AgentContext{ProjectEndpoint: endpoint, Name: name}, ctx.Err()
}
