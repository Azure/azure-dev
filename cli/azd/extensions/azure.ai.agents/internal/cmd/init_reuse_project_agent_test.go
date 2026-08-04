// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

// legacyConfigAgentService builds the deprecated shape, where the agent
// definition is nested under config: rather than inline on the service entry.
func legacyConfigAgentService(serviceName, agentName string) *azdext.ServiceConfig {
	return &azdext.ServiceConfig{
		Name: serviceName,
		Host: AiAgentHost,
		Config: &structpb.Struct{Fields: map[string]*structpb.Value{
			"kind": structpb.NewStringValue(string(agent_yaml.AgentKindHosted)),
			"name": structpb.NewStringValue(agentName),
		}},
	}
}

func TestProjectAgentServicesFrom(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		services map[string]*azdext.ServiceConfig
		want     []projectAgentService
	}{
		{
			name: "inline agent definition on the service entry",
			services: map[string]*azdext.ServiceConfig{
				"chat": inlineAgentService(t, "chat", "my-chat-agent"),
			},
			want: []projectAgentService{{ServiceName: "chat", AgentName: "my-chat-agent"}},
		},
		{
			name: "deprecated config-nested definition",
			services: map[string]*azdext.ServiceConfig{
				"chat": legacyConfigAgentService("chat", "legacy-agent"),
			},
			want: []projectAgentService{{ServiceName: "chat", AgentName: "legacy-agent"}},
		},
		{
			name: "falls back to the service key when the definition lives in agent.yaml",
			services: map[string]*azdext.ServiceConfig{
				"chat": {Name: "chat", Host: AiAgentHost},
			},
			want: []projectAgentService{{ServiceName: "chat", AgentName: "chat"}},
		},
		{
			name: "multiple agents are sorted by service name",
			services: map[string]*azdext.ServiceConfig{
				"zeta":  inlineAgentService(t, "zeta", "zeta-agent"),
				"alpha": inlineAgentService(t, "alpha", "alpha-agent"),
			},
			want: []projectAgentService{
				{ServiceName: "alpha", AgentName: "alpha-agent"},
				{ServiceName: "zeta", AgentName: "zeta-agent"},
			},
		},
		{
			name: "non-agent Foundry services are ignored",
			services: map[string]*azdext.ServiceConfig{
				"project": {Name: "project", Host: "azure.ai.project"},
				"api":     {Name: "api", Host: "containerapp"},
			},
			want: nil,
		},
		{
			name:     "project with no services",
			services: nil,
			want:     nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, projectAgentServicesFrom(tt.services))
		})
	}
}

// Ordering must not depend on Go's randomized map iteration, so the same input
// is evaluated repeatedly.
func TestProjectAgentServicesFrom_OrderingIsStable(t *testing.T) {
	t.Parallel()

	services := map[string]*azdext.ServiceConfig{
		"delta":   {Name: "delta", Host: AiAgentHost},
		"alpha":   {Name: "alpha", Host: AiAgentHost},
		"charlie": inlineAgentService(t, "charlie", "c-agent"),
		"bravo":   {Name: "bravo", Host: AiAgentHost},
	}
	want := []projectAgentService{
		{ServiceName: "alpha", AgentName: "alpha"},
		{ServiceName: "bravo", AgentName: "bravo"},
		{ServiceName: "charlie", AgentName: "c-agent"},
		{ServiceName: "delta", AgentName: "delta"},
	}

	for range 20 {
		assert.Equal(t, want, projectAgentServicesFrom(services))
	}
}

// Reuse is only safe when the caller did not describe an agent to set up.
// Under --no-prompt reuse is unconditional, so any of these flags being ignored
// would silently no-op a scripted run.
func TestAgentDefiningFlagsSet(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		flags       *initFlags
		srcExplicit bool
		want        bool
	}{
		{name: "no flags", flags: &initFlags{}, want: false},
		{name: "agent-name", flags: &initFlags{agentName: "my-agent"}, want: true},
		{name: "deploy-mode", flags: &initFlags{deployMode: "code"}, want: true},
		{name: "runtime", flags: &initFlags{runtime: "python_3_13"}, want: true},
		{name: "entry-point", flags: &initFlags{entryPoint: "app.py"}, want: true},
		{name: "dep-resolution", flags: &initFlags{depResolution: "bundled"}, want: true},
		{name: "model", flags: &initFlags{model: "gpt-5.4-mini"}, want: true},
		{name: "model-deployment", flags: &initFlags{modelDeployment: "my-deployment"}, want: true},
		{name: "project-id", flags: &initFlags{projectResourceId: "/subscriptions/x"}, want: true},
		{name: "image", flags: &initFlags{image: "myacr.azurecr.io/agent:1"}, want: true},
		{name: "protocol", flags: &initFlags{protocols: []string{"responses"}}, want: true},

		// An explicit --src names where a new agent's source goes, so it opts
		// out. A positional path is classified separately against the project
		// root after detection.
		{
			name:        "explicit --src",
			flags:       &initFlags{src: "agents/chat"},
			srcExplicit: true,
			want:        true,
		},
		{
			name:  "src folded from a positional arg is not an explicit flag",
			flags: &initFlags{src: "."},
			want:  false,
		},

		// Neither describes the agent, so both stay compatible with reuse.
		{name: "env alone does not opt out", flags: &initFlags{env: "dev"}, want: false},
		{name: "infra alone does not opt out", flags: &initFlags{infra: "bicep"}, want: false},
		{name: "no-prompt alone does not opt out", flags: &initFlags{noPrompt: true}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, agentDefiningFlagsSet(tt.flags, tt.srcExplicit))
		})
	}
}

// Regression guard for the positional form documented in the command's Use
// string: `azd ai agent init .` resolves to flags.src via applyPositionalArg,
// which must not be mistaken for an explicit --src before it is compared with
// the active project root (#9154).
func TestAgentDefiningFlagsSet_PositionalPathKeepsReuse(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	flags := &initFlags{}
	cmd := newInitCommand(&azdext.ExtensionContext{})

	require.NoError(t, applyPositionalArg(dir, flags, cmd))
	require.Equal(t, dir, flags.src, "a positional directory is folded into flags.src")

	assert.False(t, agentDefiningFlagsSet(flags, cmd.Flags().Changed("src")),
		"a positional path must not be treated as an explicit --src flag")
}

func TestPositionalSourceOptsOutOfReuse(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	agentDir := filepath.Join(projectRoot, "agents", "new")
	require.NoError(t, os.MkdirAll(agentDir, 0o750))

	tests := []struct {
		name        string
		src         string
		projectRoot string
		want        bool
	}{
		{
			name:        "project root keeps reuse",
			src:         projectRoot,
			projectRoot: projectRoot,
			want:        false,
		},
		{
			name:        "selected agent directory opts out",
			src:         agentDir,
			projectRoot: projectRoot,
			want:        true,
		},
		{
			name:        "no positional source keeps reuse",
			src:         "",
			projectRoot: projectRoot,
			want:        false,
		},
		{
			name:        "missing project root opts out conservatively",
			src:         projectRoot,
			projectRoot: "",
			want:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, positionalSourceOptsOutOfReuse(tt.src, tt.projectRoot))
		})
	}
}

func TestDescribeProjectAgentServices(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		services []projectAgentService
		want     string
	}{
		{
			name:     "agent name differs from the service key",
			services: []projectAgentService{{ServiceName: "chat", AgentName: "my-chat-agent"}},
			want:     `"chat" (agent: my-chat-agent)`,
		},
		{
			name:     "agent name matching the service key is not repeated",
			services: []projectAgentService{{ServiceName: "chat", AgentName: "chat"}},
			want:     `"chat"`,
		},
		{
			name: "multiple services are comma separated",
			services: []projectAgentService{
				{ServiceName: "alpha", AgentName: "alpha"},
				{ServiceName: "zeta", AgentName: "zeta-agent"},
			},
			want: `"alpha", "zeta" (agent: zeta-agent)`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, describeProjectAgentServices(tt.services))
		})
	}
}
