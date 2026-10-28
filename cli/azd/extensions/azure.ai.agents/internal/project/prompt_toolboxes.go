// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/envkey"
)

type toolboxAttachment struct {
	name      string
	version   string
	endpoint  string
	toolIndex int
	services  []string
}

type toolboxConnectionEnsurer interface {
	VerifyProject(context.Context) error
	EnsureConnection(context.Context, toolboxAttachment) (string, error)
}

func toolboxNode(
	g *promptGraph, refs []agent_yaml.ToolboxReference,
	newEnsurer func() (toolboxConnectionEnsurer, error),
) *promptNode {
	if len(refs) == 0 {
		return nil
	}
	var plannedTools []any
	var attachments []toolboxAttachment
	var toolChoice any
	var authoredCount int
	prepare := func() error {
		var err error
		plannedTools, attachments, err = planToolboxAttachments(g.managed.Tools, refs, g.projectEndpoint(), g.env)
		if err != nil {
			return err
		}
		toolChoice = toolboxToolChoice(g.managed.ToolChoice, plannedTools, attachments)
		authoredCount = len(g.managed.Tools)
		return nil
	}
	return &promptNode{
		Kind: nodeToolbox, ID: "toolboxes",
		Validate: func() error {
			for _, ref := range refs {
				if strings.TrimSpace(ref.Name) == "" {
					return exterrors.Validation(exterrors.CodeInvalidAgentManifest,
						"toolbox reference is missing a name", "set each toolboxes entry to an azure.ai.toolbox service name")
				}
			}
			return validateToolboxMarkerKeys(refs)
		},
		Prepare: prepare,
		Resolve: func(ctx context.Context) error {
			if err := validateToolboxDependencies(ctx, g, refs); err != nil {
				return err
			}
			// The graph prepares the pure plan before any node can write.
			// Also support resolving this node independently.
			if plannedTools == nil {
				if err := prepare(); err != nil {
					return err
				}
			}
			ensurer, err := newEnsurer()
			if err != nil {
				return err
			}
			if err := ensurer.VerifyProject(ctx); err != nil {
				return err
			}
			// Earlier nodes may have injected or replaced unrelated tools (memory,
			// for example). Apply only the planned MCP entries to their current output.
			tools := slices.Clone(g.managed.Tools)
			for _, attachment := range attachments {
				connection, err := ensurer.EnsureConnection(ctx, attachment)
				if err != nil {
					return err
				}
				tool := maps.Clone(plannedTools[attachment.toolIndex].(map[string]any))
				tool["project_connection_id"] = connection
				if attachment.toolIndex < authoredCount {
					tools[attachment.toolIndex] = tool
				} else {
					tools = append(tools, tool)
				}
			}
			g.managed.Tools = tools
			g.managed.ToolChoice = toolChoice
			plannedTools = nil
			return nil
		},
	}
}

// planToolboxAttachments is pure: reject ambiguous authoring before constructing
// clients or writing connections, and never mutate the authored tool maps.
func planToolboxAttachments(
	authored []any, refs []agent_yaml.ToolboxReference, projectEndpoint string, env map[string]string,
) ([]any, []toolboxAttachment, error) {
	if err := validateToolboxMarkerKeys(refs); err != nil {
		return nil, nil, err
	}
	tools := slices.Clone(authored)
	for i, raw := range tools {
		if tool, ok := raw.(map[string]any); ok {
			tools[i] = maps.Clone(tool)
		}
	}
	type endpointGroup struct {
		services   []string
		attachment toolboxAttachment
	}
	var groups []endpointGroup
	byEndpoint := map[string]int{}
	for _, ref := range refs {
		service := strings.TrimSpace(ref.Name)
		endpoint, err := siblingToolboxEndpoint(service, projectEndpoint, env)
		if err != nil {
			return nil, nil, err
		}
		if endpoint == "" {
			return nil, nil, exterrors.Dependency(exterrors.CodeFoundryDependencyNotReady,
				fmt.Sprintf("toolbox %q has not been deployed by an azure.ai.toolbox service", service),
				"add the toolbox service to uses and run 'azd deploy --all'")
		}
		name, version, err := parseToolboxMCPEndpoint(endpoint)
		if err != nil {
			return nil, nil, exterrors.Validation(exterrors.CodeInvalidToolbox,
				fmt.Sprintf("toolbox %q published an invalid MCP endpoint %q: %s",
					service, redactURLForDiagnostic(endpoint), err),
				"redeploy the toolbox service or check its configured endpoint")
		}
		if i, exists := byEndpoint[endpoint]; exists {
			if !slices.Contains(groups[i].services, service) {
				groups[i].services = append(groups[i].services, service)
			}
			continue
		}
		byEndpoint[endpoint] = len(groups)
		groups = append(groups, endpointGroup{services: []string{service},
			attachment: toolboxAttachment{name: name, version: version, endpoint: endpoint}})
	}
	var attachments []toolboxAttachment
	for _, group := range groups {
		service, endpoint := group.services[0], group.attachment.endpoint
		index, err := toolboxToolIndex(tools, group.services, endpoint)
		if err != nil {
			return nil, nil, err
		}
		if index == -1 {
			index = len(tools)
			tools = append(tools, map[string]any{"type": "mcp", "server_label": service})
		}
		tool := tools[index].(map[string]any)
		if label, _ := tool["server_label"].(string); strings.TrimSpace(label) == "" {
			tool["server_label"] = service
		}
		tool["server_url"] = endpoint
		if _, authored := tool["require_approval"]; !authored {
			tool["require_approval"] = "always"
		}
		attachment := group.attachment
		attachment.toolIndex = index
		attachment.services = group.services
		attachments = append(attachments, attachment)
	}
	if err := validateUniqueMcpServerLabels(tools); err != nil {
		return nil, nil, err
	}
	return tools, attachments, nil
}

func validateToolboxMarkerKeys(refs []agent_yaml.ToolboxReference) error {
	owners := map[string]string{}
	for _, ref := range refs {
		service := strings.TrimSpace(ref.Name)
		for _, key := range []string{envkey.ToolboxMCPEndpoint(service), envkey.ToolboxProjectEndpoint(service)} {
			if previous, exists := owners[key]; exists && previous != service {
				return exterrors.Validation(exterrors.CodeInvalidAgentManifest,
					fmt.Sprintf("toolbox services %q and %q share environment key %s", previous, service, key),
					"rename the toolbox services so their normalized environment keys are distinct")
			}
			owners[key] = service
		}
	}
	return nil
}

func toolboxToolChoice(choice any, tools []any, attachments []toolboxAttachment) any {
	selector, ok := choice.(map[string]any)
	if !ok || selector["type"] != "mcp" {
		return choice
	}
	label, _ := selector["server_label"].(string)
	// An authored label that survives planning already identifies a valid tool.
	for _, raw := range tools {
		if tool, ok := raw.(map[string]any); ok && tool["type"] == "mcp" && tool["server_label"] == label {
			return choice
		}
	}
	for _, attachment := range attachments {
		if slices.Contains(attachment.services, label) {
			selector = maps.Clone(selector)
			selector["server_label"] = tools[attachment.toolIndex].(map[string]any)["server_label"]
			return selector
		}
	}
	return choice
}

func toolboxToolIndex(tools []any, services []string, endpoint string) (int, error) {
	// Consider every alias before deduplicating the endpoint. Multiple authored
	// entries are ambiguous; never silently drop one or merge its options.
	index := -1
	for i, raw := range tools {
		tool, ok := raw.(map[string]any)
		if !ok || tool["type"] != "mcp" {
			continue
		}
		label, _ := tool["server_label"].(string)
		existing, _ := tool["server_url"].(string)
		sameService := slices.Contains(services, strings.TrimSpace(label))
		if existing != endpoint && !sameService {
			continue
		}
		if strings.TrimSpace(existing) != "" && existing != endpoint {
			return -1, exterrors.Validation(exterrors.CodeInvalidAgentManifest,
				fmt.Sprintf("MCP tool %q declares server_url %q, which conflicts with toolbox endpoint %q",
					label, redactURLForDiagnostic(existing), redactURLForDiagnostic(endpoint)),
				"remove server_url to use the deployed toolbox endpoint, or give the explicit MCP tool a distinct server_label")
		}
		if index != -1 {
			return -1, exterrors.Validation(exterrors.CodeInvalidAgentManifest,
				fmt.Sprintf("multiple authored MCP entries apply to toolbox services %q at endpoint %q",
					services, redactURLForDiagnostic(endpoint)),
				"keep one MCP entry for the shared endpoint with the desired label and options")
		}
		index = i
	}
	return index, nil
}

func validateUniqueMcpServerLabels(tools []any) error {
	seen := map[string]string{}
	for _, raw := range tools {
		tool, ok := raw.(map[string]any)
		if !ok || tool["type"] != "mcp" {
			continue
		}
		label, _ := tool["server_label"].(string)
		label = strings.TrimSpace(label)
		if label == "" {
			continue
		}
		endpoint, _ := tool["server_url"].(string)
		if previous, exists := seen[label]; exists && previous != endpoint {
			return exterrors.Validation(exterrors.CodeInvalidAgentManifest,
				fmt.Sprintf("MCP tool server_label %q is assigned to conflicting endpoints %q and %q",
					label, redactURLForDiagnostic(previous), redactURLForDiagnostic(endpoint)),
				"assign a unique server_label to each MCP tool")
		}
		seen[label] = endpoint
	}
	return nil
}

func redactURLForDiagnostic(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "<redacted>"
	}
	u.User, u.RawQuery, u.Fragment, u.RawFragment = nil, "", "", ""
	u.ForceQuery = false
	return u.String()
}

func siblingToolboxEndpoint(name, projectEndpoint string, env map[string]string) (string, error) {
	endpoint := strings.TrimSpace(env[envkey.ToolboxMCPEndpoint(name)])
	if endpoint == "" {
		return "", nil
	}
	projectKey := envkey.ToolboxProjectEndpoint(name)
	if declared := strings.TrimSpace(env[projectKey]); declared != "" && !sameProjectEndpoint(declared, projectEndpoint) {
		return "", exterrors.Dependency(exterrors.CodeFoundryDependencyNotReady,
			fmt.Sprintf("toolbox %q was deployed to a different Foundry project (%s)", name, projectKey),
			"run 'azd deploy --all' so the toolbox is redeployed to the project this agent targets")
	}
	return endpoint, nil
}

func parseToolboxMCPEndpoint(endpoint string) (string, string, error) {
	if err := validateAuthenticatedPromptEndpoint(endpoint); err != nil {
		return "", "", fmt.Errorf("endpoint must be an HTTPS Foundry project URL (*.services.ai.azure.com)")
	}
	u, _ := url.Parse(endpoint) // validated above; do not include raw URLs in parsing errors
	if u.User != nil || u.Fragment != "" || u.RawFragment != "" || strings.Contains(endpoint, "#") {
		return "", "", fmt.Errorf("endpoint must not contain user information or a fragment")
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return "", "", fmt.Errorf("endpoint query parameters must be valid")
	}
	for key := range query {
		if key != "api-version" {
			return "", "", fmt.Errorf("endpoint query parameters may only contain api-version")
		}
	}
	parts := strings.Split(strings.Trim(u.EscapedPath(), "/"), "/")
	if len(parts) != 8 || parts[0] != "api" || parts[1] != "projects" || parts[2] == "" ||
		parts[3] != "toolboxes" || parts[5] != "versions" || parts[7] != "mcp" {
		return "", "", fmt.Errorf("endpoint path must match /api/projects/<project>/toolboxes/<name>/versions/<version>/mcp")
	}
	name, err1 := url.PathUnescape(parts[4])
	version, err2 := url.PathUnescape(parts[6])
	if err1 != nil || err2 != nil || strings.TrimSpace(name) == "" || strings.TrimSpace(version) == "" {
		return "", "", fmt.Errorf("endpoint must include a non-empty toolbox name and version")
	}
	return name, version, nil
}

func validateToolboxDependencies(ctx context.Context, g *promptGraph, refs []agent_yaml.ToolboxReference) error {
	if g.agentService == nil || g.projectServices == nil {
		return nil
	}
	for _, ref := range refs {
		name := strings.TrimSpace(ref.Name)
		service := g.projectServices[name]
		if service == nil || service.GetHost() != foundryToolboxHost {
			return exterrors.Dependency(exterrors.CodeFoundryDependencyNotReady,
				fmt.Sprintf("toolbox %q must reference an %s sibling service", name, foundryToolboxHost),
				"declare an azure.ai.toolbox service, add it to uses, and run 'azd deploy --all'")
		}
		if !slices.Contains(g.agentService.GetUses(), name) {
			return exterrors.Dependency(exterrors.CodeFoundryDependencyNotReady,
				fmt.Sprintf("toolbox service %q is not declared in agent service %q uses", name, g.agentService.GetName()),
				fmt.Sprintf("add %q to the agent service's uses list and run 'azd deploy --all'", name))
		}
		if g.dependencyEnabled != nil {
			enabled, err := g.dependencyEnabled(ctx, name)
			if err != nil {
				return err
			}
			if !enabled {
				return exterrors.Dependency(exterrors.CodeFoundryDependencyNotReady,
					fmt.Sprintf("toolbox service %q is disabled by its deployment condition", name),
					"enable the toolbox dependency and run 'azd deploy --all'")
			}
		}
	}
	return nil
}
