// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
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
		wantErr  string
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
			wantErr: "unsupported nested config block",
		},
		{
			name: "service without a definition is not reusable",
			services: map[string]*azdext.ServiceConfig{
				"chat": {Name: "chat", Host: AiAgentHost},
			},
			wantErr: "agent definition not found",
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

			got, err := projectAgentServicesFrom(tt.services, t.TempDir())
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				assert.Empty(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestProjectAgentServicesFrom_DiskDefinition(t *testing.T) {
	t.Parallel()

	for _, filename := range []string{"agent.yaml", "agent.yml"} {
		t.Run(filename, func(t *testing.T) {
			t.Parallel()

			projectRoot := t.TempDir()
			serviceDir := filepath.Join(projectRoot, "src", "chat")
			require.NoError(t, os.MkdirAll(serviceDir, 0o750))
			require.NoError(t, os.WriteFile(
				filepath.Join(serviceDir, filename),
				[]byte("kind: hosted\nname: disk-agent\nprotocols:\n"+
					"  - protocol: responses\n    version: \"1.0.0\"\n"),
				0o600,
			))

			services, err := projectAgentServicesFrom(map[string]*azdext.ServiceConfig{
				"chat": {
					Name:         "chat",
					Host:         AiAgentHost,
					RelativePath: "src/chat",
				},
			}, projectRoot)

			require.ErrorContains(t, err, "legacy file")
			assert.Empty(t, services)
		})
	}
}

func TestProjectAgentServicesFrom_RejectsUnsafeServicePaths(t *testing.T) {
	projectRoot := t.TempDir()
	outsideDir := t.TempDir()
	traversalPath, err := filepath.Rel(projectRoot, outsideDir)
	require.NoError(t, err)

	tests := []struct {
		name         string
		relativePath string
	}{
		{name: "absolute path", relativePath: outsideDir},
		{name: "traversal path", relativePath: traversalPath},
	}
	if runtime.GOOS != "windows" {
		linkPath := filepath.Join(projectRoot, "linked-agent")
		require.NoError(t, os.Symlink(outsideDir, linkPath))
		tests = append(tests, struct {
			name         string
			relativePath string
		}{name: "symlink escape", relativePath: "linked-agent"})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := inlineAgentService(t, "chat", "inline-agent")
			svc.RelativePath = tt.relativePath

			services, err := projectAgentServicesFrom(
				map[string]*azdext.ServiceConfig{"chat": svc},
				projectRoot,
			)
			assert.Empty(t, services)
			require.ErrorContains(t, err, "invalid project path")
		})
	}
}

// An implicit on-disk definition is not a reusable unified project service.
func TestDetectProjectAgentServices_ConfiguredDiskDefinitionFromServiceDir(t *testing.T) {
	projectRoot := t.TempDir()
	serviceDir := filepath.Join(projectRoot, "src", "chat")
	require.NoError(t, os.MkdirAll(serviceDir, 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(serviceDir, "agent.yaml"),
		[]byte("kind: hosted\nname: disk-agent\nprotocols:\n"+
			"  - protocol: responses\n    version: \"1.0.0\"\n"),
		0o600,
	))
	t.Chdir(serviceDir)

	client := newHelpersTestAzdClient(t,
		&helpersProjectServer{project: &azdext.ProjectConfig{
			Path: projectRoot,
			Services: map[string]*azdext.ServiceConfig{
				"chat": {
					Name:         "chat",
					Host:         AiAgentHost,
					RelativePath: "src/chat",
				},
			},
		}},
		&helpersPromptServer{},
	)

	detection, err := detectProjectAgentServices(t.Context(), client)
	require.Error(t, err)
	assert.Empty(t, detection.services)
}

// Ordering must not depend on Go's randomized map iteration, so the same input
// is evaluated repeatedly.
func TestProjectAgentServicesFrom_OrderingIsStable(t *testing.T) {
	t.Parallel()

	services := map[string]*azdext.ServiceConfig{
		"delta":   inlineAgentService(t, "delta", "delta"),
		"alpha":   inlineAgentService(t, "alpha", "alpha"),
		"charlie": inlineAgentService(t, "charlie", "c-agent"),
		"bravo":   inlineAgentService(t, "bravo", "bravo"),
	}
	want := []projectAgentService{
		{ServiceName: "alpha", AgentName: "alpha"},
		{ServiceName: "bravo", AgentName: "bravo"},
		{ServiceName: "charlie", AgentName: "c-agent"},
		{ServiceName: "delta", AgentName: "delta"},
	}
	projectRoot := t.TempDir()

	for range 20 {
		got, err := projectAgentServicesFrom(services, projectRoot)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}
}

func TestValidateExistingProjectAgentServices(t *testing.T) {
	t.Run("direct and root ref ignore stale unreferenced legacy files", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		require.NoError(t, os.WriteFile(
			filepath.Join(root, "definition.yaml"),
			[]byte("kind: hosted\nname: referenced-agent\n"),
			0o600,
		))
		require.NoError(t, os.WriteFile(
			filepath.Join(root, "agent.yaml"),
			[]byte("not: [valid"),
			0o600,
		))
		refProps, err := structpb.NewStruct(map[string]any{"$ref": "./definition.yaml"})
		require.NoError(t, err)

		project := &azdext.ProjectConfig{
			Path: root,
			Services: map[string]*azdext.ServiceConfig{
				"direct": inlineAgentService(t, "direct", "direct-agent"),
				"ref": {
					Name:                 "ref",
					Host:                 AiAgentHost,
					AdditionalProperties: refProps,
				},
			},
		}
		require.NoError(t, validateExistingProjectAgentServices(project))
	})

	for _, tt := range []struct {
		name    string
		setup   func(t *testing.T, root string) *azdext.ServiceConfig
		wantErr string
	}{
		{
			name: "implicit legacy file",
			setup: func(t *testing.T, root string) *azdext.ServiceConfig {
				require.NoError(t, os.WriteFile(
					filepath.Join(root, "agent.yaml"),
					[]byte("kind: hosted\nname: legacy\n"),
					0o600,
				))
				return &azdext.ServiceConfig{Name: "agent", Host: AiAgentHost}
			},
			wantErr: "found legacy file agent.yaml",
		},
		{
			name: "missing definition",
			setup: func(*testing.T, string) *azdext.ServiceConfig {
				return &azdext.ServiceConfig{Name: "agent", Host: AiAgentHost}
			},
			wantErr: "agent definition not found",
		},
		{
			name: "nested config",
			setup: func(*testing.T, string) *azdext.ServiceConfig {
				return legacyConfigAgentService("agent", "legacy")
			},
			wantErr: "unsupported nested config block",
		},
		{
			name: "malformed authoritative ref",
			setup: func(t *testing.T, _ string) *azdext.ServiceConfig {
				props, err := structpb.NewStruct(map[string]any{"$ref": "./missing.yaml"})
				require.NoError(t, err)
				return &azdext.ServiceConfig{
					Name:                 "agent",
					Host:                 AiAgentHost,
					AdditionalProperties: props,
				}
			},
			wantErr: "missing.yaml",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			err := validateExistingProjectAgentServices(&azdext.ProjectConfig{
				Path: root,
				Services: map[string]*azdext.ServiceConfig{
					"agent": tt.setup(t, root),
				},
			})
			require.ErrorContains(t, err, tt.wantErr)
			_, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok, "expected structured validation error, got %T", err)
		})
	}

	t.Run("environment override is rejected", func(t *testing.T) {
		root := t.TempDir()
		t.Setenv("AGENT_DEFINITION_PATH", filepath.Join(root, "agent.yaml"))
		err := validateExistingProjectAgentServices(&azdext.ProjectConfig{
			Path: root,
			Services: map[string]*azdext.ServiceConfig{
				"agent": inlineAgentService(t, "agent", "agent"),
			},
		})
		require.ErrorContains(t, err, "AGENT_DEFINITION_PATH")
	})

	t.Run("no agent services is allowed", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, validateExistingProjectAgentServices(&azdext.ProjectConfig{
			Path: t.TempDir(),
			Services: map[string]*azdext.ServiceConfig{
				"web": {Name: "web", Host: "containerapp"},
			},
		}))
	})

	t.Run("validation order is deterministic", func(t *testing.T) {
		t.Parallel()
		err := validateExistingProjectAgentServices(&azdext.ProjectConfig{
			Path: t.TempDir(),
			Services: map[string]*azdext.ServiceConfig{
				"zeta":  {Name: "zeta", Host: AiAgentHost},
				"alpha": {Name: "alpha", Host: AiAgentHost},
			},
		})
		require.ErrorContains(t, err, `service "alpha"`)
	})
}

// Reuse is only safe when the caller did not describe an agent to set up.
// Under --no-prompt reuse is unconditional, so any of these flags being ignored
// would silently no-op a scripted run.
func TestAgentDefiningFlagsSet(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		flags          *initFlags
		srcBlocksReuse bool
		want           bool
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
		{name: "acr-connection", flags: &initFlags{acrConnection: "my-acr"}, want: true},
		{name: "image", flags: &initFlags{image: "myacr.azurecr.io/agent:1"}, want: true},
		{name: "kind", flags: &initFlags{kind: "prompt-voice"}, want: true},
		{name: "voice", flags: &initFlags{voice: "en-US-AvaNeural"}, want: true},
		{name: "registry connection", flags: &initFlags{registryConnection: "private-registry"}, want: true},
		{name: "protocol", flags: &initFlags{protocols: []string{"responses"}}, want: true},

		// An explicit --src names where a new agent's source goes, so it opts
		// out. A positional path is classified separately against the project
		// root after detection.
		{
			name:           "explicit --src",
			flags:          &initFlags{src: "agents/chat"},
			srcBlocksReuse: true,
			want:           true,
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

			assert.Equal(t, tt.want, agentDefiningFlagsSet(tt.flags, tt.srcBlocksReuse))
		})
	}
}

func TestCanReuseExistingAgentConfiguration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		flags            *initFlags
		manifestDeclined bool
		srcBlocksReuse   bool
		want             bool
	}{
		{name: "no caller intent allows reuse", flags: &initFlags{}, want: true},
		{
			name:  "no-prompt alone allows reuse",
			flags: &initFlags{noPrompt: true},
			want:  true,
		},
		{
			name: "agent-defining flags block reuse under no-prompt",
			flags: &initFlags{
				noPrompt:   true,
				deployMode: "code",
				runtime:    "python_3_13",
				entryPoint: "app.py",
			},
		},
		{
			name:  "manifest pointer blocks reuse",
			flags: &initFlags{templatePointer: "agent.manifest.yaml"},
		},
		{
			name:  "force blocks reuse",
			flags: &initFlags{force: true},
		},
		{
			name:             "declined manifest blocks rediscovery",
			flags:            &initFlags{},
			manifestDeclined: true,
		},
		{
			name:           "explicit source blocks reuse",
			flags:          &initFlags{src: "src/new"},
			srcBlocksReuse: true,
		},
		{
			name: "explicit source is valid for bare-definition reuse",
			flags: &initFlags{
				src: "src/existing",
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, canReuseExistingAgentConfiguration(
				tt.flags,
				tt.manifestDeclined,
				tt.srcBlocksReuse,
			))
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
	otherDir := filepath.Join(projectRoot, "agents", "other")
	outsideDir := t.TempDir()
	require.NoError(t, os.MkdirAll(agentDir, 0o750))
	require.NoError(t, os.MkdirAll(otherDir, 0o750))
	traversalPath, err := filepath.Rel(projectRoot, outsideDir)
	require.NoError(t, err)

	tests := []struct {
		name                string
		src                 string
		projectRoot         string
		serviceRelativePath string
		want                bool
	}{
		{
			name:        "project root keeps reuse",
			src:         projectRoot,
			projectRoot: projectRoot,
			want:        false,
		},
		{
			name:                "configured service directory keeps reuse",
			src:                 agentDir,
			projectRoot:         projectRoot,
			serviceRelativePath: "agents/new",
			want:                false,
		},
		{
			name:                "unconfigured agent directory opts out",
			src:                 otherDir,
			projectRoot:         projectRoot,
			serviceRelativePath: "agents/new",
			want:                true,
		},
		{
			name:                "no positional source keeps reuse",
			src:                 "",
			projectRoot:         projectRoot,
			serviceRelativePath: "agents/new",
			want:                false,
		},
		{
			name:                "missing project root opts out conservatively",
			src:                 projectRoot,
			projectRoot:         "",
			serviceRelativePath: "agents/new",
			want:                true,
		},
		{
			name:                "absolute service path cannot authorize reuse",
			src:                 outsideDir,
			projectRoot:         projectRoot,
			serviceRelativePath: outsideDir,
			want:                true,
		},
		{
			name:                "traversal service path cannot authorize reuse",
			src:                 outsideDir,
			projectRoot:         projectRoot,
			serviceRelativePath: traversalPath,
			want:                true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			services := []projectAgentService{{
				ServiceName:  "new",
				RelativePath: tt.serviceRelativePath,
			}}
			assert.Equal(t, tt.want, positionalSourceOptsOutOfReuse(
				tt.src,
				tt.projectRoot,
				services,
			))
		})
	}
}

func TestPositionalSourceOptsOutOfReuse_RejectsSymlinkEscape(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks requires elevated privileges on some Windows hosts")
	}

	projectRoot := t.TempDir()
	outsideDir := t.TempDir()
	linkPath := filepath.Join(projectRoot, "linked-agent")
	require.NoError(t, os.Symlink(outsideDir, linkPath))

	services := []projectAgentService{{
		ServiceName:  "linked",
		RelativePath: "linked-agent",
	}}
	assert.True(t, positionalSourceOptsOutOfReuse(linkPath, projectRoot, services),
		"a service path that resolves outside the project must not authorize reuse")
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
