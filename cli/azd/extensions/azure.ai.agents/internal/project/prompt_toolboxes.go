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
	return &promptNode{
		Kind: nodeToolbox, ID: "toolboxes",
		Validate: func() error {
			for _, ref := range refs {
				if strings.TrimSpace(ref.Name) == "" {
					return exterrors.Validation(exterrors.CodeInvalidAgentManifest,
						"toolbox reference is missing a name", "set each toolboxes entry to an azure.ai.toolbox service name")
				}
			}
			return nil
		},
		Resolve: func(ctx context.Context) error {
			if err := validateToolboxDependencies(ctx, g, refs); err != nil {
				return err
			}
			tools, attachments, err := planToolboxAttachments(g.managed.Tools, refs, g.projectEndpoint(), g.env)
			if err != nil {
				return err
			}
			ensurer, err := newEnsurer()
			if err != nil {
				return err
			}
			if err := ensurer.VerifyProject(ctx); err != nil {
				return err
			}
			for _, attachment := range attachments {
				connection, err := ensurer.EnsureConnection(ctx, attachment)
				if err != nil {
					return err
				}
				tools[attachment.toolIndex].(map[string]any)["project_connection_id"] = connection
			}
			g.managed.Tools = tools
			return nil
		},
	}
}

// planToolboxAttachments is pure: reject ambiguous authoring before constructing
// clients or writing connections, and never mutate the authored tool maps.
func planToolboxAttachments(
	authored []any, refs []agent_yaml.ToolboxReference, projectEndpoint string, env map[string]string,
) ([]any, []toolboxAttachment, error) {
	tools := slices.Clone(authored)
	for i, raw := range tools {
		if tool, ok := raw.(map[string]any); ok {
			tools[i] = maps.Clone(tool)
		}
	}
	seen := map[string]bool{}
	var attachments []toolboxAttachment
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
		if seen[endpoint] {
			continue
		}
		seen[endpoint] = true
		index, err := toolboxToolIndex(tools, service, endpoint)
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
		attachments = append(attachments, toolboxAttachment{
			name: name, version: version, endpoint: endpoint, toolIndex: index,
		})
	}
	if err := validateUniqueMcpServerLabels(tools); err != nil {
		return nil, nil, err
	}
	return tools, attachments, nil
}

func toolboxToolIndex(tools []any, service, endpoint string) (int, error) {
	// Exact endpoints take precedence over service-key placeholders. An explicit
	// URL is never repinned just because its label happens to match a service.
	for i, raw := range tools {
		if tool, ok := raw.(map[string]any); ok && tool["type"] == "mcp" && tool["server_url"] == endpoint {
			return i, nil
		}
	}
	for i, raw := range tools {
		tool, ok := raw.(map[string]any)
		if !ok || tool["type"] != "mcp" || tool["server_label"] != service {
			continue
		}
		if existing, _ := tool["server_url"].(string); strings.TrimSpace(existing) != "" {
			return -1, exterrors.Validation(exterrors.CodeInvalidAgentManifest,
				fmt.Sprintf("MCP tool %q declares server_url %q, which conflicts with toolbox endpoint %q",
					service, redactURLForDiagnostic(existing), redactURLForDiagnostic(endpoint)),
				"remove server_url to use the deployed toolbox endpoint, or give the explicit MCP tool a distinct server_label")
		}
		return i, nil
	}
	return -1, nil
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
