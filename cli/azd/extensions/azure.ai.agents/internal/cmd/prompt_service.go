// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"os"
	"path/filepath"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/paths"
	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"go.yaml.in/yaml/v3"
)

// promptServiceContext carries everything the prompt-agent commands
// (show/invoke/list/delete) need to talk to the harness for a resolved
// azure.ai.agent service of kind=managed.
type promptServiceContext struct {
	ServiceName string
	ServiceDir  string
	Settings    *project.PromptAgentSettings
	Agent       agent_yaml.ManagedAgent
}

// promptSettingsFromService extracts the prompt-agent harness settings from a
// service config. The bool is false when the service is not a prompt agent
// (no promptAgent block), letting callers fall back to the hosted path.
func promptSettingsFromService(svc *azdext.ServiceConfig) (*project.PromptAgentSettings, bool) {
	if svc == nil || svc.Config == nil {
		return nil, false
	}
	var cfg project.ServiceTargetAgentConfig
	if err := project.UnmarshalStruct(svc.Config, &cfg); err != nil {
		return nil, false
	}
	if cfg.PromptAgent == nil {
		return nil, false
	}
	return cfg.PromptAgent, true
}

// resolvePromptAgentService resolves the named (or sole) azure.ai.agent service
// and, when it is a prompt (kind=managed) agent, returns its harness settings
// and parsed agent.yaml. The bool is false when the resolved service is NOT a
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

	settings, ok := promptSettingsFromService(svc)
	if !ok {
		return nil, false, nil
	}
	settings.ApplyEnvOverrides()
	if err := settings.Validate(); err != nil {
		return nil, false, err
	}

	// Apply the same azd environment-derived target resolution that deploy uses
	// so lifecycle commands (show/invoke/list/delete) hit the identical managed
	// workspace route (<account>@<project>@AML) the agent was created on. Without
	// this, these commands resolve promptAgent.workspace from azure.yaml verbatim
	// and query a non-existent workspace, yielding an HTML 404 the client cannot
	// parse.
	if envValues, envErr := promptEnvValues(ctx, azdClient); envErr == nil {
		if _, mapErr := project.ResolvePromptTargetFromEnv(settings, envValues); mapErr != nil {
			return nil, false, mapErr
		}
	}

	pctx := &promptServiceContext{
		ServiceName: svc.Name,
		Settings:    settings,
	}

	if proj != nil {
		if dir, joinErr := paths.JoinAllowRoot(proj.Path, svc.RelativePath); joinErr == nil {
			pctx.ServiceDir = dir
		}
	}

	// Parse the agent.yaml that backs the service to recover the model and
	// (default) agent name. Best-effort: the service Name is used as the agent
	// identity when agent.yaml cannot be read.
	pctx.Agent.Name = svc.Name
	if pctx.ServiceDir != "" {
		if data, readErr := os.ReadFile(filepath.Join(pctx.ServiceDir, "agent.yaml")); readErr == nil {
			var managed agent_yaml.ManagedAgent
			if yaml.Unmarshal(data, &managed) == nil && managed.Name != "" {
				pctx.Agent = managed
			}
		}
	}

	return pctx, true, nil
}

// AgentName returns the harness agent identity for the resolved service.
func (p *promptServiceContext) AgentName() string {
	if p.Agent.Name != "" {
		return p.Agent.Name
	}
	return p.ServiceName
}

// newClient builds a harness client for the resolved prompt service.
func (p *promptServiceContext) newClient() (*agent_api.ManagedAgentClient, error) {
	return project.NewPromptAgentClient(p.Settings)
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
