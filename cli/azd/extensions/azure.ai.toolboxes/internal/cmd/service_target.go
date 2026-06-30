// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"azure.ai.toolboxes/internal/foundry/projectctx"
	"azure.ai.toolboxes/internal/pkg/azure"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/foundry"
)

// aiToolboxHost is the azure.yaml service host kind owned by this extension. A
// `host: azure.ai.toolbox` service entry carries one Foundry toolbox (a named bundle
// of connection-backed tools), keyed by the toolbox name, and is reconciled (a new
// version is upserted) at deploy time by toolboxServiceTarget instead of being layered
// into provisioning.
const aiToolboxHost = "azure.ai.toolbox"

var _ azdext.ServiceTargetProvider = (*toolboxServiceTarget)(nil)

// toolboxServiceConfig is the service-level shape of a `host: azure.ai.toolbox` entry
// (see schemas/azure.ai.toolbox.json). The toolbox name is the azure.yaml service key,
// not a body field. Each tool is a verbatim data-plane tool object; a tool that names a
// `connection` is resolved to its project_connection_id at deploy time.
type toolboxServiceConfig struct {
	Description string           `json:"description,omitempty"`
	Tools       []map[string]any `json:"tools,omitempty"`
}

// toolboxServiceTarget upserts a Foundry toolbox declared as an azure.ai.toolbox
// service. Deploy creates a new toolbox version from the entry's tools; the resource
// name is the service key. Package and Publish are no-ops because a toolbox has no build
// artifact.
type toolboxServiceTarget struct {
	azdClient     *azdext.AzdClient
	serviceConfig *azdext.ServiceConfig
	resolver      connectionResolver
}

// newToolboxServiceTarget creates the azure.ai.toolbox service-target provider.
func newToolboxServiceTarget(azdClient *azdext.AzdClient) azdext.ServiceTargetProvider {
	return &toolboxServiceTarget{azdClient: azdClient, resolver: defaultConnectionResolver{}}
}

// Initialize stores the service configuration; no other setup is required.
func (p *toolboxServiceTarget) Initialize(ctx context.Context, serviceConfig *azdext.ServiceConfig) error {
	p.serviceConfig = serviceConfig
	return nil
}

// Endpoints returns no endpoints; a toolbox service exposes none directly (its MCP
// endpoint is published to the azd environment during Deploy).
func (p *toolboxServiceTarget) Endpoints(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	targetResource *azdext.TargetResource,
) ([]string, error) {
	return nil, nil
}

// GetTargetResource delegates to azd's default resolver and falls back to a minimal
// target so the deploy pipeline can proceed; the toolbox upsert targets the Foundry
// project endpoint, not an ARM resource azd tracks.
func (p *toolboxServiceTarget) GetTargetResource(
	ctx context.Context,
	subscriptionId string,
	serviceConfig *azdext.ServiceConfig,
	defaultResolver func() (*azdext.TargetResource, error),
) (*azdext.TargetResource, error) {
	if defaultResolver != nil {
		if target, err := defaultResolver(); err == nil && target != nil {
			return target, nil
		}
	}
	return &azdext.TargetResource{SubscriptionId: subscriptionId}, nil
}

// Package is a no-op; a toolbox has nothing to build or stage.
func (p *toolboxServiceTarget) Package(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	progress azdext.ProgressReporter,
) (*azdext.ServicePackageResult, error) {
	return &azdext.ServicePackageResult{}, nil
}

// Publish is a no-op; a toolbox has no artifact to publish.
func (p *toolboxServiceTarget) Publish(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	targetResource *azdext.TargetResource,
	publishOptions *azdext.PublishOptions,
	progress azdext.ProgressReporter,
) (*azdext.ServicePublishResult, error) {
	return &azdext.ServicePublishResult{}, nil
}

// Deploy upserts the toolbox by creating a new version from the entry's tools. Tool
// entries that name a `connection` are resolved to their project_connection_id (the
// `uses:` edge guarantees the connection is reconciled first). ${VAR} references resolve
// against the azd environment; Foundry ${{...}} expressions pass through untouched.
// Removing the service from azure.yaml stops azd managing the toolbox but does not delete
// it (use `azd ai toolbox delete`).
func (p *toolboxServiceTarget) Deploy(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	targetResource *azdext.TargetResource,
	progress azdext.ProgressReporter,
) (*azdext.ServiceDeployResult, error) {
	cfg, err := parseToolboxServiceConfig(serviceConfig)
	if err != nil {
		return nil, err
	}
	name := serviceConfig.GetName()

	resolved, err := projectctx.Resolve(ctx, projectctx.ResolveOpts{})
	if err != nil {
		return nil, err
	}
	endpoint := resolved.Endpoint

	env, err := p.currentEnvValues(ctx)
	if err != nil {
		return nil, err
	}

	tools, err := p.buildToolEntries(ctx, endpoint, cfg.Tools, env)
	if err != nil {
		return nil, err
	}

	if progress != nil {
		progress(fmt.Sprintf("Upserting toolbox %q", name))
	}

	client, err := newToolboxClient(endpoint)
	if err != nil {
		return nil, err
	}

	created, err := client.CreateToolboxVersion(ctx, name, &azure.CreateToolboxVersionRequest{
		Description: cfg.Description,
		Tools:       tools,
	})
	if err != nil {
		return nil, fmt.Errorf("upserting toolbox %q: %w", name, err)
	}

	// Surface the toolbox's MCP endpoint to agents (and the developer) without re-running.
	mcpURL := buildToolboxMcpURL(endpoint, name, created.Version)
	if err := setToolboxEndpointEnvFunc(ctx, name, mcpURL); err != nil {
		return nil, err
	}

	return &azdext.ServiceDeployResult{}, nil
}

// buildToolEntries renders each declared tool into a data-plane tool object: ${VAR}
// references are expanded and a tool naming a `connection` has that name resolved to a
// project_connection_id (and server_url when the connection exposes a target).
func (p *toolboxServiceTarget) buildToolEntries(
	ctx context.Context,
	endpoint string,
	tools []map[string]any,
	env map[string]string,
) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		entry, ok := expandToolboxValue(tool, env).(map[string]any)
		if !ok {
			continue
		}
		if connName, isString := entry["connection"].(string); isString && connName != "" {
			conn, err := p.resolver.resolveConnection(ctx, endpoint, connName)
			if err != nil {
				return nil, err
			}
			entry["project_connection_id"] = conn.ID
			if conn.Target != "" {
				if _, set := entry["server_url"]; !set {
					entry["server_url"] = conn.Target
				}
			}
			delete(entry, "connection")
		}
		out = append(out, entry)
	}
	return out, nil
}

// parseToolboxServiceConfig reads the service-level (inline) toolbox properties, falling
// back to the deprecated config: shape for azure.yaml files written before the
// per-resource service split.
func parseToolboxServiceConfig(svc *azdext.ServiceConfig) (*toolboxServiceConfig, error) {
	props := svc.GetAdditionalProperties()
	if props == nil || len(props.GetFields()) == 0 {
		props = svc.GetConfig()
	}
	cfg := &toolboxServiceConfig{}
	if props == nil {
		return cfg, nil
	}
	b, err := json.Marshal(props.AsMap())
	if err != nil {
		return nil, fmt.Errorf("encoding toolbox service %q config: %w", svc.GetName(), err)
	}
	if err := json.Unmarshal(b, cfg); err != nil {
		return nil, fmt.Errorf("parsing toolbox service %q config: %w", svc.GetName(), err)
	}
	return cfg, nil
}

// currentEnvValues loads all key-value pairs from the active azd environment, used to
// resolve ${VAR} references in tool fields at deploy time.
func (p *toolboxServiceTarget) currentEnvValues(ctx context.Context) (map[string]string, error) {
	current, err := p.azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return nil, fmt.Errorf("resolving current azd environment: %w", err)
	}
	resp, err := p.azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{
		Name: current.GetEnvironment().GetName(),
	})
	if err != nil {
		return nil, fmt.Errorf("loading azd environment values: %w", err)
	}
	values := make(map[string]string, len(resp.GetKeyValues()))
	for _, kv := range resp.GetKeyValues() {
		values[kv.GetKey()] = kv.GetValue()
	}
	return values, nil
}

// expandToolboxValue recursively expands ${VAR} references in every string within a tool
// value (maps, slices, scalars) against the azd environment, preserving Foundry
// server-side ${{...}} expressions.
func expandToolboxValue(value any, env map[string]string) any {
	switch typed := value.(type) {
	case string:
		resolved, err := foundry.ExpandEnv(typed, func(name string) string { return env[name] })
		if err != nil {
			return typed
		}
		return resolved
	case map[string]any:
		out := make(map[string]any, len(typed))
		for k, v := range typed {
			out[k] = expandToolboxValue(v, env)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, v := range typed {
			out[i] = expandToolboxValue(v, env)
		}
		return out
	default:
		return value
	}
}
