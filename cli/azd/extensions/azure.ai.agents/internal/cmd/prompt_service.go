// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"strings"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/paths"
	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// promptServiceContext carries everything the prompt-agent commands
// (show/invoke/list/delete) need to talk to the harness for a resolved
// azure.ai.agent service of kind=prompt.
type promptServiceContext struct {
	AzdClient   *azdext.AzdClient
	ServiceName string
	ServiceDir  string
	Settings    *project.PromptAgentSettings
	Agent       agent_yaml.PromptAgent
}

// promptDefinitionForService returns the prompt-agent definition backing a
// service, and whether the service is a prompt agent at all.
//
// The definition is normally inline on the azure.yaml service entry, which is
// also where `kind: prompt` identifies it; a `$ref:` include is expanded by the
// same call. Classification intentionally requires an explicit prompt kind;
// the optional promptAgent settings block is not a discriminator.
func promptDefinitionForService(
	svc *azdext.ServiceConfig,
	projectPath, serviceDir string,
) (agent_yaml.PromptAgent, bool) {
	if def, found, err := project.PromptAgentFromResolvedService(svc, projectPath); err == nil && found {
		return def, true
	}

	if !project.ServiceIsPromptAgent(svc) {
		return agent_yaml.PromptAgent{}, false
	}

	return agent_yaml.PromptAgent{}, false
}

// resolvePromptAgentService resolves the named (or sole) azure.ai.agent service
// and, when it is a prompt (kind=prompt) agent, returns its harness settings
// and parsed definition. The bool is false when the resolved service is NOT a
// prompt agent, so callers can fall back to the hosted code path.
func resolvePromptAgentService(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	name string,
	noPrompt bool,
) (*promptServiceContext, bool, error) {
	svc, proj, err := resolveAgentService(ctx, azdClient, name, noPrompt)
	if err != nil {
		return nil, false, err
	}

	projectPath := ""
	serviceDir := ""
	if proj != nil {
		projectPath = proj.Path
		if dir, joinErr := paths.JoinAllowRoot(proj.Path, svc.RelativePath); joinErr == nil {
			serviceDir = dir
		}
	}

	agentDef, isPrompt := promptDefinitionForService(svc, projectPath, serviceDir)
	if !isPrompt {
		return nil, false, nil
	}

	// Resolve the harness target exactly as deploy does: the subscription,
	// resource group, workspace, and project endpoint come from the azd
	// environment. The
	// environment read is best-effort — when it cannot be read, expansion falls
	// back to the process environment and unset references collapse to the
	// defaults, which is what lets these commands run in a project that has not
	// been provisioned yet.
	envValues, envErr := promptEnvValues(ctx, azdClient)
	settings, err := project.ResolvePromptAgentSettings(envValues)
	if err != nil {
		return nil, false, err
	}

	// Apply the same azd environment-derived target resolution that deploy uses
	// so lifecycle commands (show/invoke/list/delete) hit the identical managed
	// workspace route (<account>@<project>@AML) the agent was created on. Without
	// this, these commands resolve the workspace verbatim and query a
	// non-existent one, yielding an HTML 404 the client cannot parse.
	if envErr == nil {
		if _, mapErr := project.ResolvePromptTargetFromEnv(settings, envValues); mapErr != nil {
			return nil, false, mapErr
		}
	}

	pctx := &promptServiceContext{
		AzdClient:   azdClient,
		ServiceName: svc.Name,
		ServiceDir:  serviceDir,
		Settings:    settings,
		Agent:       agentDef,
	}
	if strings.TrimSpace(pctx.Agent.Name) == "" {
		pctx.Agent.Name = svc.Name
	}

	return pctx, true, nil
}

// promptAgentNameForService returns the harness agent identity for a prompt
// service: the `name` its definition declares, falling back to the azure.yaml
// service key. It is the lightweight counterpart of
// promptServiceContext.AgentName for callers (like the down handlers) that only
// have a ServiceConfig.
func promptAgentNameForService(svc *azdext.ServiceConfig, projectPath string) string {
	if svc == nil {
		return ""
	}
	serviceDir := ""
	if dir, err := paths.JoinAllowRoot(projectPath, svc.RelativePath); err == nil {
		serviceDir = dir
	}
	def, _ := promptDefinitionForService(svc, projectPath, serviceDir)
	if name := strings.TrimSpace(def.Name); name != "" {
		return name
	}
	return svc.Name
}

// AgentName returns the harness agent identity for the resolved service.
func (p *promptServiceContext) AgentName() string {
	if p.Agent.Name != "" {
		return p.Agent.Name
	}
	return p.ServiceName
}

// agentKey returns the config-store key used to persist per-agent multi-turn
// state (the last response id) for this prompt service. It mirrors the hosted
// key scheme (buildAgentKey) so lookups and cleanup share one code path.
func (p *promptServiceContext) agentKey(agentName string) string {
	return buildAgentKey(strings.TrimSpace(p.Settings.ProjectEndpoint), agentName, "", false)
}

// newClient builds a harness client for the resolved prompt service.
func (p *promptServiceContext) newClient(ctx context.Context) (*agent_api.AgentClient, error) {
	credential, err := project.ResolvePromptCredential(ctx, p.AzdClient, p.Settings)
	if err != nil {
		return nil, err
	}
	return project.NewPromptAgentClient(p.Settings, credential)
}

// promptEnvValues returns the current azd environment as a key/value map. It is
// used to apply the same Foundry project -> managed workspace resolution that
// deploy performs, so lifecycle commands target the route the agent lives on.
func promptEnvValues(ctx context.Context, azdClient *azdext.AzdClient) (map[string]string, error) {
	envResp, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return nil, err
	}
	values, err := azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{
		Name: envResp.Environment.Name,
	})
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(values.KeyValues))
	for _, kv := range values.KeyValues {
		out[kv.Key] = kv.Value
	}
	return out, nil
}
