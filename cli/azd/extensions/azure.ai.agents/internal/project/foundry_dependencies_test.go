// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"testing"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/envkey"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func TestValidateFoundryDependencies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		uses       []string
		services   map[string]*azdext.ServiceConfig
		env        map[string]string
		wantErr    bool
		wantDetail []string
	}{
		{
			name: "all supported dependencies ready",
			uses: []string{"project", "connection", "toolbox", "other-agent"},
			services: map[string]*azdext.ServiceConfig{
				"project":     {Name: "project", Host: foundryProjectHost},
				"connection":  {Name: "connection", Host: foundryConnectionHost},
				"toolbox":     {Name: "toolbox", Host: foundryToolboxHost},
				"other-agent": {Name: "other-agent", Host: foundryAgentHost},
			},
			env: map[string]string{
				"FOUNDRY_PROJECT_ENDPOINT":                 "https://example.test/projects/test",
				envkey.ConnectionProjectEndpoint:           "https://example.test/projects/test",
				"AZURE_AI_PROJECT_CONNECTION_NAMES":        "connection, another",
				envkey.ToolboxMCPEndpoint("toolbox"):       "https://example.test/toolbox/mcp",
				envkey.ToolboxProjectEndpoint("toolbox"):   "https://example.test/projects/test",
				"AGENT_OTHER_AGENT_NAME":                   "other-agent",
				"AGENT_OTHER_AGENT_VERSION":                "1",
				envkey.AgentProjectEndpoint("other-agent"): "https://example.test/projects/test",
			},
		},
		{
			name: "missing supported dependencies are aggregated",
			uses: []string{"toolbox", "project"},
			services: map[string]*azdext.ServiceConfig{
				"project": {Name: "project", Host: foundryProjectHost},
				"toolbox": {Name: "toolbox", Host: foundryToolboxHost},
			},
			wantErr: true,
			wantDetail: []string{
				"project (azure.ai.project): FOUNDRY_PROJECT_ENDPOINT is not set",
				"toolbox (azure.ai.toolbox): TOOLBOX_TOOLBOX_MCP_ENDPOINT is not set",
				"azd provision",
				"azd deploy --all",
			},
		},
		{
			name: "transitive Foundry dependency is left to the producer",
			uses: []string{"toolbox"},
			services: map[string]*azdext.ServiceConfig{
				"project": {Name: "project", Host: foundryProjectHost},
				"toolbox": {Name: "toolbox", Host: foundryToolboxHost, Uses: []string{"project"}},
			},
			env: map[string]string{
				"FOUNDRY_PROJECT_ENDPOINT":               "https://example.test/projects/test",
				envkey.ToolboxMCPEndpoint("toolbox"):     "https://example.test/toolbox/mcp",
				envkey.ToolboxProjectEndpoint("toolbox"): "https://example.test/projects/test",
			},
		},
		{
			name: "single deploy dependency has targeted remediation",
			uses: []string{"toolbox"},
			services: map[string]*azdext.ServiceConfig{
				"toolbox": {Name: "toolbox", Host: foundryToolboxHost},
			},
			wantErr: true,
			wantDetail: []string{
				`azd deploy "toolbox"`,
				`azd deploy "agent"`,
			},
		},
		{
			name: "connection uses service name",
			uses: []string{"connection-service"},
			services: map[string]*azdext.ServiceConfig{
				"connection-service": {Name: "connection-service", Host: foundryConnectionHost},
			},
			env: map[string]string{
				"FOUNDRY_PROJECT_ENDPOINT":          "https://example.test/projects/test",
				envkey.ConnectionProjectEndpoint:    "https://example.test/projects/test",
				"AZURE_AI_PROJECT_CONNECTION_NAMES": "connection-service",
			},
		},
		{
			name: "skill with version marker is ready",
			uses: []string{"summarize"},
			services: map[string]*azdext.ServiceConfig{
				"summarize": {Name: "summarize", Host: foundrySkillHost},
			},
			env: map[string]string{
				"FOUNDRY_PROJECT_ENDPOINT":               "https://example.test/projects/current",
				envkey.SkillVersion("summarize"):         "1",
				envkey.SkillProjectEndpoint("summarize"): "https://example.test/projects/current/",
			},
		},
		{
			name: "skill project endpoint comparison ignores case",
			uses: []string{"summarize"},
			services: map[string]*azdext.ServiceConfig{
				"summarize": {Name: "summarize", Host: foundrySkillHost},
			},
			env: map[string]string{
				"FOUNDRY_PROJECT_ENDPOINT":               "https://account.test/projects/current",
				envkey.SkillVersion("summarize"):         "1",
				envkey.SkillProjectEndpoint("summarize"): "HTTPS://ACCOUNT.TEST/projects/current/",
			},
		},
		{
			name: "skill endpoint aliases resolve to the same project",
			uses: []string{"summarize"},
			services: map[string]*azdext.ServiceConfig{
				"summarize": {Name: "summarize", Host: foundrySkillHost},
			},
			env: map[string]string{
				"FOUNDRY_PROJECT_ENDPOINT":               "https://account.services.ai.azure.com/api/projects/current",
				envkey.SkillVersion("summarize"):         "1",
				envkey.SkillProjectEndpoint("summarize"): "https://account.services.ai.azure.com/projects/current",
			},
		},
		{
			name: "legacy skill without readiness markers is accepted",
			uses: []string{"summarize"},
			services: map[string]*azdext.ServiceConfig{
				"summarize": {Name: "summarize", Host: foundrySkillHost},
			},
		},
		{
			name: "partial skill marker fails",
			uses: []string{"summarize"},
			services: map[string]*azdext.ServiceConfig{
				"summarize": {Name: "summarize", Host: foundrySkillHost},
			},
			env: map[string]string{
				envkey.SkillProjectEndpoint("summarize"): "https://example.test/projects/current",
			},
			wantErr:    true,
			wantDetail: []string{"summarize (azure.ai.skill): SKILL_SUMMARIZE_VERSION is not set", `azd deploy "summarize"`},
		},
		{
			name: "legacy connection names without scope are accepted",
			uses: []string{"connection"},
			services: map[string]*azdext.ServiceConfig{
				"connection": {Name: "connection", Host: foundryConnectionHost},
			},
			env: map[string]string{
				"AZURE_AI_PROJECT_CONNECTION_NAMES": "connection",
			},
		},
		{
			name: "connection readiness from another project fails",
			uses: []string{"connection"},
			services: map[string]*azdext.ServiceConfig{
				"connection": {Name: "connection", Host: foundryConnectionHost},
			},
			env: map[string]string{
				"FOUNDRY_PROJECT_ENDPOINT":          "https://example.test/projects/current",
				envkey.ConnectionProjectEndpoint:    "https://example.test/projects/old",
				"AZURE_AI_PROJECT_CONNECTION_NAMES": "connection",
			},
			wantErr: true,
			wantDetail: []string{
				"AZURE_AI_PROJECT_CONNECTIONS_PROJECT_ENDPOINT does not match FOUNDRY_PROJECT_ENDPOINT",
			},
		},
		{
			name: "skill marker from another project fails",
			uses: []string{"summarize"},
			services: map[string]*azdext.ServiceConfig{
				"summarize": {Name: "summarize", Host: foundrySkillHost},
			},
			env: map[string]string{
				"FOUNDRY_PROJECT_ENDPOINT":               "https://example.test/projects/current",
				envkey.SkillVersion("summarize"):         "1",
				envkey.SkillProjectEndpoint("summarize"): "https://example.test/projects/old",
			},
			wantErr:    true,
			wantDetail: []string{"SKILL_SUMMARIZE_PROJECT_ENDPOINT does not match FOUNDRY_PROJECT_ENDPOINT"},
		},
		{
			name: "unknown Foundry host without readiness contract is ignored",
			uses: []string{"future"},
			services: map[string]*azdext.ServiceConfig{
				"future": {Name: "future", Host: "azure.ai.future"},
			},
		},
		{
			name: "non-Foundry dependency is ignored",
			uses: []string{"web"},
			services: map[string]*azdext.ServiceConfig{
				"web": {Name: "web", Host: "containerapp"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			agent := &azdext.ServiceConfig{Name: "agent", Host: foundryAgentHost, Uses: tt.uses}
			services := tt.services
			services[agent.Name] = agent

			err := validateFoundryDependencies(t.Context(), agent, nil, services, tt.env, nil)
			if !tt.wantErr {
				require.NoError(t, err)
				return
			}

			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			require.Equal(t, exterrors.CodeFoundryDependencyNotReady, localErr.Code)
			require.Equal(t, azdext.LocalErrorCategoryDependency, localErr.Category)
			for _, detail := range tt.wantDetail {
				require.Contains(t, localErr.Message+localErr.Suggestion, detail)
			}
		})
	}
}

func TestValidateFoundryDependenciesLegacyToolbox(t *testing.T) {
	t.Parallel()

	agent := &azdext.ServiceConfig{Name: "agent", Host: foundryAgentHost}
	config := &ServiceTargetAgentConfig{Toolboxes: []Toolbox{{Name: "legacy-tools"}}}
	err := validateFoundryDependencies(
		t.Context(),
		agent,
		config,
		map[string]*azdext.ServiceConfig{"agent": agent},
		nil,
		nil,
	)

	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	require.Contains(t, localErr.Message, "legacy bundled toolbox")
	require.Contains(t, localErr.Suggestion, "migrate bundled toolboxes")
}

func TestValidateFoundryDependenciesRejectsLegacyToolboxFromAnotherProject(t *testing.T) {
	t.Parallel()
	agent := &azdext.ServiceConfig{Name: "agent", Host: foundryAgentHost}
	config := &ServiceTargetAgentConfig{Toolboxes: []Toolbox{{Name: "legacy-tools"}}}
	env := map[string]string{
		"FOUNDRY_PROJECT_ENDPOINT":                "https://account.services.ai.azure.com/api/projects/current",
		envkey.ToolboxMCPEndpoint("legacy-tools"): "https://account.services.ai.azure.com/api/projects/old/toolboxes/legacy-tools/mcp",
	}
	err := validateFoundryDependencies(
		t.Context(), agent, config, map[string]*azdext.ServiceConfig{"agent": agent}, env, nil,
	)
	require.ErrorContains(t, err, "does not belong to FOUNDRY_PROJECT_ENDPOINT")
}

func TestValidateFoundryDependenciesSplitToolboxReference(t *testing.T) {
	t.Parallel()

	agent := &azdext.ServiceConfig{
		Name: "agent",
		Host: foundryAgentHost,
		Uses: []string{"tools"},
	}
	config := &ServiceTargetAgentConfig{Toolboxes: []Toolbox{{Name: "tools"}}}
	services := map[string]*azdext.ServiceConfig{
		"agent": agent,
		"tools": {Name: "tools", Host: foundryToolboxHost},
	}
	err := validateFoundryDependencies(t.Context(), agent, config, services, nil, nil)

	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	require.NotContains(t, localErr.Message, "legacy bundled toolbox")
	require.Contains(t, localErr.Suggestion, `azd deploy "tools"`)
}

func TestValidateFoundryDependenciesUnwiredSplitToolbox(t *testing.T) {
	t.Parallel()
	agent := &azdext.ServiceConfig{Name: "agent", Host: foundryAgentHost}
	config := &ServiceTargetAgentConfig{Toolboxes: []Toolbox{{Name: "tools"}}}
	services := map[string]*azdext.ServiceConfig{
		"agent": agent,
		"tools": {Name: "tools", Host: foundryToolboxHost},
	}
	err := validateFoundryDependencies(t.Context(), agent, config, services, nil, nil)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	require.Contains(t, localErr.Message, "toolbox service is not declared in agent uses")
	require.Contains(t, localErr.Suggestion, `add "tools" to the "agent" service uses list`)
}

func TestValidateFoundryDependenciesRejectsToolboxServiceWithWrongHost(t *testing.T) {
	t.Parallel()
	agent := &azdext.ServiceConfig{Name: "agent", Host: foundryAgentHost, Uses: []string{"tools"}}
	config := &ServiceTargetAgentConfig{Toolboxes: []Toolbox{{Name: "tools"}}}
	services := map[string]*azdext.ServiceConfig{
		"agent": agent,
		"tools": {Name: "tools", Host: foundryAgentHost},
	}
	env := map[string]string{
		"FOUNDRY_PROJECT_ENDPOINT":             "https://example.test/projects/current",
		envkey.ToolboxMCPEndpoint("tools"):     "https://example.test/projects/current/toolboxes/tools/mcp",
		envkey.ToolboxProjectEndpoint("tools"): "https://example.test/projects/current",
		envkey.AgentProjectEndpoint("tools"):   "https://example.test/projects/current",
		"AGENT_TOOLS_NAME":                     "tools",
		"AGENT_TOOLS_VERSION":                  "1",
	}

	err := validateFoundryDependencies(t.Context(), agent, config, services, env, nil)
	require.ErrorContains(t, err, "resolves to service host")
	require.ErrorContains(t, err, "instead of")
}

func TestValidateFoundryDependenciesSkipsDisabledDependency(t *testing.T) {
	t.Parallel()
	agent := &azdext.ServiceConfig{Name: "agent", Host: foundryAgentHost, Uses: []string{"tools"}}
	services := map[string]*azdext.ServiceConfig{
		"agent": agent,
		"tools": {Name: "tools", Host: foundryToolboxHost},
	}
	err := validateFoundryDependencies(
		t.Context(), agent, nil, services, nil,
		func(context.Context, string) (bool, error) { return false, nil },
	)
	require.NoError(t, err)
}

func TestValidateFoundryDependenciesReportsDisabledRuntimeToolbox(t *testing.T) {
	t.Parallel()
	agent := &azdext.ServiceConfig{Name: "agent", Host: foundryAgentHost, Uses: []string{"tools"}}
	config := &ServiceTargetAgentConfig{Toolboxes: []Toolbox{{Name: "tools"}}}
	services := map[string]*azdext.ServiceConfig{
		"agent": agent,
		"tools": {Name: "tools", Host: foundryToolboxHost},
	}
	err := validateFoundryDependencies(
		t.Context(), agent, config, services, nil,
		func(context.Context, string) (bool, error) { return false, nil },
	)
	require.ErrorContains(t, err, "toolbox dependency is disabled")
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	require.Contains(t, localErr.Suggestion, "enable the toolbox dependency or remove it from the agent definition")
}

func TestValidateFoundryDependenciesCombinesMigrationAndProvisionRemediation(t *testing.T) {
	t.Parallel()
	agent := &azdext.ServiceConfig{Name: "agent", Host: foundryAgentHost, Uses: []string{"project"}}
	config := &ServiceTargetAgentConfig{Toolboxes: []Toolbox{{Name: "legacy-tools"}}}
	services := map[string]*azdext.ServiceConfig{
		"agent": agent, "project": {Name: "project", Host: foundryProjectHost},
	}
	err := validateFoundryDependencies(t.Context(), agent, config, services, nil, nil)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	require.Contains(t, localErr.Suggestion, "migrate bundled toolboxes")
	require.Contains(t, localErr.Suggestion, "azd provision")
	require.Contains(t, localErr.Suggestion, "azd deploy --all")
}

func TestValidateFoundryDependenciesIgnoresResourceUses(t *testing.T) {
	t.Parallel()
	agent := &azdext.ServiceConfig{Name: "agent", Host: foundryAgentHost, Uses: []string{"storage"}}
	called := false
	err := validateFoundryDependencies(
		t.Context(), agent, nil, map[string]*azdext.ServiceConfig{"agent": agent}, nil,
		func(context.Context, string) (bool, error) { called = true; return true, nil },
	)
	require.NoError(t, err)
	require.False(t, called)
}

func TestValidateFoundryDependenciesRejectsCrossProjectMarkers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		host   string
		env    map[string]string
		detail string
	}{
		{
			name: "toolbox", host: foundryToolboxHost,
			env: map[string]string{
				"FOUNDRY_PROJECT_ENDPOINT":           "https://current",
				envkey.ToolboxMCPEndpoint("dep"):     "https://mcp",
				envkey.ToolboxProjectEndpoint("dep"): "https://old",
			},
			detail: "TOOLBOX_DEP_PROJECT_ENDPOINT",
		},
		{
			name: "agent", host: foundryAgentHost,
			env: map[string]string{
				"FOUNDRY_PROJECT_ENDPOINT": "https://current",
				"AGENT_DEP_NAME":           "dep", "AGENT_DEP_VERSION": "1",
				envkey.AgentProjectEndpoint("dep"): "https://old",
			},
			detail: "AGENT_DEP_PROJECT_ENDPOINT",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			agent := &azdext.ServiceConfig{Name: "root", Host: foundryAgentHost, Uses: []string{"dep"}}
			services := map[string]*azdext.ServiceConfig{
				"root": agent, "dep": {Name: "dep", Host: tt.host},
			}
			err := validateFoundryDependencies(t.Context(), agent, nil, services, tt.env, nil)
			require.ErrorContains(t, err, tt.detail)
		})
	}
}

func TestValidateFoundryDependenciesProvisionOnlyRemediation(t *testing.T) {
	t.Parallel()

	agent := &azdext.ServiceConfig{
		Name: "agent",
		Host: foundryAgentHost,
		Uses: []string{"project", "connection"},
	}
	services := map[string]*azdext.ServiceConfig{
		"agent":      agent,
		"project":    {Name: "project", Host: foundryProjectHost},
		"connection": {Name: "connection", Host: foundryConnectionHost},
	}
	err := validateFoundryDependencies(t.Context(), agent, nil, services, nil, nil)

	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	require.Contains(t, localErr.Suggestion, "azd provision")
	require.NotContains(t, localErr.Suggestion, "azd deploy --all")
}
