// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"
	projectpkg "azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	goyaml "go.yaml.in/yaml/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestDetectStartupCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		files    []string // files to create in a temp directory
		expected string
	}{
		{
			name:     "python with pyproject.toml and main.py",
			files:    []string{"pyproject.toml", "main.py"},
			expected: "python main.py",
		},
		{
			name:     "python with pyproject.toml but no main.py",
			files:    []string{"pyproject.toml"},
			expected: "",
		},
		{
			name:     "python with requirements.txt and main.py",
			files:    []string{"requirements.txt", "main.py"},
			expected: "python main.py",
		},
		{
			name:     "python with requirements.txt but no main.py",
			files:    []string{"requirements.txt"},
			expected: "",
		},
		{
			name:     "python with main.py only",
			files:    []string{"main.py"},
			expected: "python main.py",
		},
		{
			name:     "python with main.py and helper file",
			files:    []string{"main.py", "tools.py"},
			expected: "python main.py",
		},
		{
			name:     "case-sensitive Python entry point",
			files:    []string{"Main.py"},
			expected: "",
		},
		{
			name:     "dotnet with csproj",
			files:    []string{"MyAgent.csproj"},
			expected: "dotnet run",
		},
		{
			name:     "node with package.json",
			files:    []string{"package.json"},
			expected: "npm start",
		},
		{
			name:     "node with Python helper file",
			files:    []string{"package.json", "tools.py"},
			expected: "npm start",
		},
		{
			name:     "unknown project type",
			files:    []string{"README.md"},
			expected: "",
		},
		{
			name:     "empty directory",
			files:    nil,
			expected: "",
		},
		{
			name:     "pyproject.toml takes precedence over package.json",
			files:    []string{"pyproject.toml", "main.py", "package.json"},
			expected: "python main.py",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			for _, f := range tt.files {
				if err := os.WriteFile(filepath.Join(dir, f), []byte(""), 0600); err != nil {
					t.Fatalf("failed to create test file %s: %v", f, err)
				}
			}

			got := detectStartupCommand(dir)
			if got != tt.expected {
				t.Errorf("detectStartupCommand() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestDetectProjectType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		files        []string
		wantLanguage string
		wantStartCmd string
	}{
		{
			name:         "python detected from pyproject.toml with main.py",
			files:        []string{"pyproject.toml", "main.py"},
			wantLanguage: "python",
			wantStartCmd: "python main.py",
		},
		{
			name:         "python detected but no start cmd without entry point",
			files:        []string{"pyproject.toml"},
			wantLanguage: "python",
			wantStartCmd: "",
		},
		{
			name:         "dotnet detected from csproj",
			files:        []string{"Agent.csproj"},
			wantLanguage: "dotnet",
			wantStartCmd: "dotnet run",
		},
		{
			name:         "node detected from package.json",
			files:        []string{"package.json"},
			wantLanguage: "node",
			wantStartCmd: "npm start",
		},
		{
			name:         "node takes precedence over Python helper file",
			files:        []string{"package.json", "tools.py"},
			wantLanguage: "node",
			wantStartCmd: "npm start",
		},
		{
			name:         "unknown when no markers",
			files:        []string{"Dockerfile"},
			wantLanguage: "unknown",
			wantStartCmd: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			for _, f := range tt.files {
				if err := os.WriteFile(filepath.Join(dir, f), []byte(""), 0600); err != nil {
					t.Fatalf("failed to create test file %s: %v", f, err)
				}
			}

			pt := detectProjectType(dir)
			if pt.Language != tt.wantLanguage {
				t.Errorf("Language = %q, want %q", pt.Language, tt.wantLanguage)
			}
			if pt.StartCmd != tt.wantStartCmd {
				t.Errorf("StartCmd = %q, want %q", pt.StartCmd, tt.wantStartCmd)
			}
		})
	}
}

func TestWarnProjectInspectionFailure(t *testing.T) {
	var stderr bytes.Buffer
	warnProjectInspectionFailure(&stderr, "missing-service", errors.New("access denied"))

	warning := stderr.String()
	require.Contains(t, warning, `cannot read project directory "missing-service": access denied`)
	require.Contains(t, warning, "code deploy will not be offered")
	require.Contains(t, warning, "no local start command can be detected")
	require.Contains(t, warning, "Check the service path in azure.yaml and directory permissions")
}

func TestCodeDeployProjectDetection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		files      []string
		wantPython bool
		wantDotnet bool
		wantCode   bool
	}{
		{
			name:       "modern Python project",
			files:      []string{"pyproject.toml"},
			wantPython: true,
			wantCode:   true,
		},
		{
			name:       "requirements Python project",
			files:      []string{"requirements.txt"},
			wantPython: true,
			wantCode:   true,
		},
		{
			name:       "standalone Python file",
			files:      []string{"agent.py"},
			wantPython: true,
			wantCode:   true,
		},
		{
			name:       "Node project with Python helper",
			files:      []string{"package.json", "tools.py"},
			wantPython: true,
		},
		{
			name:  "case-sensitive Python marker",
			files: []string{"PYPROJECT.TOML"},
		},
		{
			name:       "dotnet project",
			files:      []string{"Agent.csproj"},
			wantDotnet: true,
			wantCode:   true,
		},
		{
			name:       "mixed supported project",
			files:      []string{"pyproject.toml", "Agent.csproj"},
			wantPython: true,
			wantDotnet: true,
			wantCode:   true,
		},
		{
			name:  "unsupported Node project",
			files: []string{"package.json"},
		},
		{
			name:  "unknown project",
			files: []string{"README.md"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			for _, file := range tt.files {
				if err := os.WriteFile(filepath.Join(dir, file), nil, 0600); err != nil {
					t.Fatalf("failed to create test file %s: %v", file, err)
				}
			}

			if got := isPythonProject(dir); got != tt.wantPython {
				t.Errorf("isPythonProject() = %t, want %t", got, tt.wantPython)
			}
			if got := isDotnetProject(dir); got != tt.wantDotnet {
				t.Errorf("isDotnetProject() = %t, want %t", got, tt.wantDotnet)
			}
			if got := supportsCodeDeploy(dir); got != tt.wantCode {
				t.Errorf("supportsCodeDeploy() = %t, want %t", got, tt.wantCode)
			}
		})
	}
}

func TestToServiceKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "simple name", input: "myagent", want: "MYAGENT"},
		{name: "with dashes", input: "my-agent-svc", want: "MY_AGENT_SVC"},
		{name: "with spaces", input: "my agent svc", want: "MY_AGENT_SVC"},
		{name: "mixed dashes and spaces", input: "my-agent svc", want: "MY_AGENT_SVC"},
		{name: "already uppercase", input: "MY_AGENT", want: "MY_AGENT"},
		{name: "empty string", input: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := toServiceKey(tt.input)
			if got != tt.want {
				t.Errorf("toServiceKey(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestProtocolFromAgentYaml(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		yaml       string // file contents; empty string means no file
		noFile     bool   // when true, don't create agent.yaml
		wantProto  string
		wantErr    bool
		errContain string // substring expected in the error message
	}{
		{
			name:      "single protocol responses",
			yaml:      "protocols:\n  - protocol: responses\n    version: \"1.0\"\n",
			wantProto: "responses",
		},
		{
			name:      "single protocol invocations",
			yaml:      "protocols:\n  - protocol: invocations\n    version: \"1.0\"\n",
			wantProto: "invocations",
		},
		{
			name:      "single protocol a2a",
			yaml:      "protocols:\n  - protocol: a2a\n    version: \"1.0\"\n",
			wantProto: "a2a",
		},
		{
			name:       "no protocols field",
			yaml:       "name: my-agent\n",
			wantErr:    true,
			errContain: "does not declare any protocols",
		},
		{
			name:       "empty protocols list",
			yaml:       "protocols: []\n",
			wantErr:    true,
			errContain: "does not declare any protocols",
		},
		{
			name:       "single protocol with empty value",
			yaml:       "protocols:\n  - protocol: \"\"\n    version: \"1.0\"\n",
			wantErr:    true,
			errContain: "protocol field is empty",
		},
		{
			name:       "single protocol whitespace only",
			yaml:       "protocols:\n  - protocol: \"  \"\n    version: \"1.0\"\n",
			wantErr:    true,
			errContain: "protocol field is empty",
		},
		{
			name: "multiple protocols",
			yaml: "protocols:\n  - protocol: responses\n" +
				"    version: \"1.0\"\n  - protocol: invocations\n" +
				"    version: \"1.0\"\n",
			wantErr:    true,
			errContain: "declares multiple protocols",
		},
		{
			name: "responses plus a2a requires --protocol",
			yaml: "protocols:\n  - protocol: responses\n" +
				"    version: \"1.0\"\n  - protocol: a2a\n" +
				"    version: \"1.0\"\n",
			wantErr:    true,
			errContain: "declares multiple protocols",
		},
		{
			name:       "activity_protocol only is not invocable",
			yaml:       "protocols:\n  - protocol: activity_protocol\n    version: \"1.0\"\n",
			wantErr:    true,
			errContain: "non-invocable protocols",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var agentDef agent_yaml.ContainerAgent
			require.NoError(t, goyaml.Unmarshal([]byte(tt.yaml), &agentDef))

			got, err := protocolFromContainerAgent(agentDef)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil",
						tt.errContain)
				}
				if !strings.Contains(err.Error(), tt.errContain) {
					t.Errorf("error = %q, want substring %q",
						err.Error(), tt.errContain)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(got) != tt.wantProto {
				t.Errorf("protocol = %q, want %q", got, tt.wantProto)
			}
		})
	}
}

func TestSetACREnvVar(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		skipACR   bool
		wantValue string
	}{
		{
			name:      "skip ACR sets true",
			skipACR:   true,
			wantValue: "true",
		},
		{
			name:      "container deploy sets false",
			skipACR:   false,
			wantValue: "false",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			envServer := &testEnvironmentServiceServer{
				environments: map[string]*azdext.Environment{
					"test-env": {Name: "test-env"},
				},
			}
			workflowServer := &testWorkflowServiceServer{}
			azdClient := newTestAzdClient(t, envServer, workflowServer)

			err := setACREnvVar(t.Context(), azdClient, "test-env", tt.skipACR)
			require.NoError(t, err)
			require.Equal(t, tt.wantValue, envServer.values["test-env"]["AZD_AGENT_SKIP_ACR"])
		})
	}
}

func TestIsTerminal_NonTTY(t *testing.T) {
	t.Parallel()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	t.Cleanup(func() {
		_ = r.Close()
		_ = w.Close()
	})

	if isTerminal(r.Fd()) {
		t.Errorf("isTerminal(pipe read end) = true, want false")
	}
}

// helpersProjectServer is a fake ProjectServiceServer that returns a fixed project config.
type helpersProjectServer struct {
	azdext.UnimplementedProjectServiceServer
	project *azdext.ProjectConfig
}

func (s *helpersProjectServer) Get(
	_ context.Context, _ *azdext.EmptyRequest,
) (*azdext.GetProjectResponse, error) {
	return &azdext.GetProjectResponse{Project: s.project}, nil
}

// helpersPromptServer is a fake PromptServiceServer that records Select calls
// and returns a canned choice index.
type helpersPromptServer struct {
	azdext.UnimplementedPromptServiceServer
	selectIndex int32
	selectCalls atomic.Int32
}

type helpersFailingEnvironmentServer struct {
	testEnvironmentServiceServer
	getValueErr error
}

func (s *helpersFailingEnvironmentServer) GetValue(
	context.Context, *azdext.GetEnvRequest,
) (*azdext.KeyValueResponse, error) {
	return nil, s.getValueErr
}

func (s *helpersPromptServer) Select(
	_ context.Context, req *azdext.SelectRequest,
) (*azdext.SelectResponse, error) {
	s.selectCalls.Add(1)
	idx := s.selectIndex
	return &azdext.SelectResponse{Value: &idx}, nil
}

// newHelpersTestAzdClient spins up a gRPC server with the supplied Project,
// Prompt, and optional Environment stubs and returns a client wired to its
// address.
func newHelpersTestAzdClient(
	t *testing.T,
	projectServer *helpersProjectServer,
	promptServer *helpersPromptServer,
	environmentServers ...azdext.EnvironmentServiceServer,
) *azdext.AzdClient {
	t.Helper()

	grpcServer := grpc.NewServer()
	azdext.RegisterProjectServiceServer(grpcServer, projectServer)
	azdext.RegisterPromptServiceServer(grpcServer, promptServer)
	if len(environmentServers) > 0 {
		azdext.RegisterEnvironmentServiceServer(grpcServer, environmentServers[0])
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(func() {
		grpcServer.Stop()
		_ = listener.Close()
	})

	azdClient, err := azdext.NewAzdClient(azdext.WithAddress(listener.Addr().String()))
	require.NoError(t, err)
	t.Cleanup(func() { azdClient.Close() })

	return azdClient
}

// TestResolveAgentServiceFromProject_UsesVerifiedInlineNameForBrownfieldProject
// is a regression test for #9109. Brownfield init writes the hosted agent
// definition inline and points the used azure.ai.project service at an existing
// project, but AGENT_<SERVICE>_NAME is not populated. Remote invoke may use the
// inline name only after confirming the agent exists in that adopted project.
func TestResolveAgentServiceFromProject_UsesVerifiedInlineNameForBrownfieldProject(t *testing.T) {
	t.Parallel()

	projectEndpoint := "https://account.services.ai.azure.com/api/projects/existing"
	agentProps, err := projectpkg.AgentDefinitionToServiceProperties(agent_yaml.ContainerAgent{
		AgentDefinition: agent_yaml.AgentDefinition{
			Kind: agent_yaml.AgentKindHosted,
			Name: "inline-agent",
		},
		Protocols: []agent_yaml.ProtocolVersionRecord{{
			Protocol: "invocations",
			Version:  "2.0.0",
		}},
	}, nil)
	require.NoError(t, err)
	projectProps, err := projectpkg.MarshalStruct(&projectpkg.ServiceTargetAgentConfig{
		Endpoint: projectEndpoint,
	})
	require.NoError(t, err)

	projectServer := &helpersProjectServer{project: &azdext.ProjectConfig{
		Path: t.TempDir(),
		Services: map[string]*azdext.ServiceConfig{
			"ai-project": {
				Name:                 "ai-project",
				Host:                 AiProjectHost,
				AdditionalProperties: projectProps,
			},
			"service-key": {
				Name:                 "service-key",
				Host:                 AiAgentHost,
				AdditionalProperties: agentProps,
				Uses:                 []string{"ai-project"},
			},
		},
	}}
	envServer := &testEnvironmentServiceServer{
		current: &azdext.Environment{Name: "test"},
		values:  map[string]map[string]string{"test": {}},
	}
	azdClient := newHelpersTestAzdClient(
		t, projectServer, &helpersPromptServer{}, envServer,
	)

	defaultInfo, err := resolveAgentServiceFromProject(t.Context(), azdClient, "", true)
	require.NoError(t, err)
	require.Empty(t, defaultInfo.AgentName,
		"shared commands must not implicitly target a brownfield agent")

	info, err := resolveAgentServiceFromProject(
		t.Context(),
		azdClient,
		"",
		true,
		withBrownfieldAgentExistenceResolver(func(
			_ context.Context,
			endpoint string,
			agentName string,
		) (bool, error) {
			require.Equal(t, projectEndpoint, endpoint)
			require.Equal(t, "inline-agent", agentName)
			return true, nil
		}),
	)
	require.NoError(t, err)
	require.Equal(t, "service-key", info.ServiceName)
	require.Equal(t, "inline-agent", info.AgentName,
		"inline agent name should resolve for an explicitly adopted existing project")
	require.Equal(t, projectEndpoint, info.ProjectEndpoint,
		"verified inline name must remain bound to its adopted project")

	missingInfo, err := resolveAgentServiceFromProject(
		t.Context(),
		azdClient,
		"",
		true,
		withBrownfieldAgentExistenceResolver(func(
			context.Context,
			string,
			string,
		) (bool, error) {
			return false, nil
		}),
	)
	require.NoError(t, err)
	require.Empty(t, missingInfo.AgentName,
		"inline name must not resolve when the agent does not exist in the adopted project")

	_, err = resolveAgentServiceFromProject(
		t.Context(),
		azdClient,
		"",
		true,
		withBrownfieldAgentExistenceResolver(func(
			context.Context,
			string,
			string,
		) (bool, error) {
			return false, status.Error(codes.Unavailable, "agent lookup unavailable")
		}),
	)
	require.ErrorContains(t, err, "checking whether agent")
}

// TestResolveAgentServiceFromProject_GreenfieldRequiresDeploy verifies that an
// undeployed greenfield service does not auto-resolve its inline name. This
// prevents invoke from silently targeting an older live agent whose name
// happens to match the local definition.
func TestResolveAgentServiceFromProject_GreenfieldRequiresDeploy(t *testing.T) {
	t.Parallel()

	agentProps, err := projectpkg.AgentDefinitionToServiceProperties(agent_yaml.ContainerAgent{
		AgentDefinition: agent_yaml.AgentDefinition{
			Kind: agent_yaml.AgentKindHosted,
			Name: "inline-agent",
		},
	}, nil)
	require.NoError(t, err)
	projectProps, err := projectpkg.MarshalStruct(&projectpkg.ServiceTargetAgentConfig{})
	require.NoError(t, err)

	projectServer := &helpersProjectServer{project: &azdext.ProjectConfig{
		Path: t.TempDir(),
		Services: map[string]*azdext.ServiceConfig{
			"ai-project": {
				Name:                 "ai-project",
				Host:                 AiProjectHost,
				AdditionalProperties: projectProps,
			},
			"service-key": {
				Name:                 "service-key",
				Host:                 AiAgentHost,
				AdditionalProperties: agentProps,
				Uses:                 []string{"ai-project"},
			},
		},
	}}
	envServer := &testEnvironmentServiceServer{
		current: &azdext.Environment{Name: "test"},
		values:  map[string]map[string]string{"test": {}},
	}
	azdClient := newHelpersTestAzdClient(
		t, projectServer, &helpersPromptServer{}, envServer,
	)

	info, err := resolveAgentServiceFromProject(
		t.Context(),
		azdClient,
		"",
		true,
		withBrownfieldAgentExistenceResolver(func(
			context.Context,
			string,
			string,
		) (bool, error) {
			t.Fatal("greenfield service must not check for an existing agent")
			return false, nil
		}),
	)
	require.NoError(t, err)
	require.Empty(t, info.AgentName,
		"greenfield service must require deploy output rather than using the inline name")
}

// TestResolveAgentServiceFromProject_EnvironmentNameWins verifies a deployed
// agent name remains authoritative when it differs from the current inline
// definition.
func TestResolveAgentServiceFromProject_EnvironmentNameWins(t *testing.T) {
	t.Parallel()

	agentProps, err := projectpkg.AgentDefinitionToServiceProperties(agent_yaml.ContainerAgent{
		AgentDefinition: agent_yaml.AgentDefinition{
			Kind: agent_yaml.AgentKindHosted,
			Name: "inline-agent",
		},
	}, nil)
	require.NoError(t, err)
	projectProps, err := projectpkg.MarshalStruct(&projectpkg.ServiceTargetAgentConfig{
		Endpoint: "https://account.services.ai.azure.com/api/projects/existing",
	})
	require.NoError(t, err)

	projectServer := &helpersProjectServer{project: &azdext.ProjectConfig{
		Path: t.TempDir(),
		Services: map[string]*azdext.ServiceConfig{
			"ai-project": {
				Name:                 "ai-project",
				Host:                 AiProjectHost,
				AdditionalProperties: projectProps,
			},
			"service-key": {
				Name:                 "service-key",
				Host:                 AiAgentHost,
				AdditionalProperties: agentProps,
				Uses:                 []string{"ai-project"},
			},
		},
	}}
	envServer := &testEnvironmentServiceServer{
		current: &azdext.Environment{Name: "test"},
		values: map[string]map[string]string{"test": {
			"AGENT_SERVICE_KEY_NAME": "deployed-agent",
		}},
	}
	azdClient := newHelpersTestAzdClient(
		t, projectServer, &helpersPromptServer{}, envServer,
	)

	info, err := resolveAgentServiceFromProject(
		t.Context(),
		azdClient,
		"",
		true,
		withBrownfieldAgentExistenceResolver(func(
			context.Context,
			string,
			string,
		) (bool, error) {
			t.Fatal("deployed environment name must bypass the inline agent check")
			return false, nil
		}),
	)
	require.NoError(t, err)
	require.Equal(t, "deployed-agent", info.AgentName,
		"deployed environment output should override the inline definition")
}

// TestResolveAgentServiceFromProject_EnvLookupFailureDoesNotFallback verifies a
// transient env read failure is not treated as proof that the deploy output is
// absent. Falling back in that case could target a different existing agent.
func TestResolveAgentServiceFromProject_EnvLookupFailureIsReturned(t *testing.T) {
	agentProps, err := projectpkg.AgentDefinitionToServiceProperties(agent_yaml.ContainerAgent{
		AgentDefinition: agent_yaml.AgentDefinition{
			Kind: agent_yaml.AgentKindHosted,
			Name: "inline-agent",
		},
	}, nil)
	require.NoError(t, err)
	projectProps, err := projectpkg.MarshalStruct(&projectpkg.ServiceTargetAgentConfig{
		Endpoint: "https://account.services.ai.azure.com/api/projects/existing",
	})
	require.NoError(t, err)

	projectServer := &helpersProjectServer{project: &azdext.ProjectConfig{
		Path: t.TempDir(),
		Services: map[string]*azdext.ServiceConfig{
			"ai-project": {
				Name:                 "ai-project",
				Host:                 AiProjectHost,
				AdditionalProperties: projectProps,
			},
			"service-key": {
				Name:                 "service-key",
				Host:                 AiAgentHost,
				AdditionalProperties: agentProps,
				Uses:                 []string{"ai-project"},
			},
		},
	}}
	tests := []struct {
		name string
		err  error
	}{
		{
			name: "environment unavailable",
			err:  status.Error(codes.Unavailable, "environment service unavailable"),
		},
		{
			name: "environment not found",
			err:  status.Error(codes.NotFound, "environment not found"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			envServer := &helpersFailingEnvironmentServer{
				testEnvironmentServiceServer: testEnvironmentServiceServer{
					current: &azdext.Environment{Name: "test"},
				},
				getValueErr: tt.err,
			}
			azdClient := newHelpersTestAzdClient(
				t, projectServer, &helpersPromptServer{}, envServer,
			)

			info, err := resolveAgentServiceFromProject(
				t.Context(), azdClient, "", true, withBrownfieldInlineAgentName(),
			)
			require.Error(t, err)
			require.Empty(t, info.AgentName)
			require.ErrorContains(t, err, "reading AGENT_SERVICE_KEY_NAME")
		})
	}
}

// TestResolveAgentProtocol_ReturnsServiceName verifies that resolveAgentProtocol
// returns the resolved service name alongside the protocol, so callers can cache
// it and avoid a redundant prompt.
func TestResolveAgentProtocol_ReturnsServiceName(t *testing.T) {
	t.Parallel()

	// Create a temp dir with a hosted agent.yaml declaring the "responses" protocol.
	svcDir := t.TempDir()
	agentYaml := "kind: hosted\nname: my-agent\nprotocols:\n  - protocol: responses\n    version: \"1.0\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(svcDir, "agent.yaml"), []byte(agentYaml), 0600))

	tests := []struct {
		name        string
		inputName   string // name passed to resolveAgentProtocol
		services    map[string]*azdext.ServiceConfig
		selectIndex int32 // which service the prompt returns (0-based)
		wantName    string
	}{
		{
			name:      "single service auto-resolved",
			inputName: "",
			services: map[string]*azdext.ServiceConfig{
				"my-agent": {Name: "my-agent", Host: AiAgentHost, RelativePath: "."},
			},
			wantName: "my-agent",
		},
		{
			name:      "explicit name returns that service",
			inputName: "agent-b",
			services: map[string]*azdext.ServiceConfig{
				"agent-a": {Name: "agent-a", Host: AiAgentHost, RelativePath: "."},
				"agent-b": {Name: "agent-b", Host: AiAgentHost, RelativePath: "."},
			},
			wantName: "agent-b",
		},
		{
			name:      "multiple services prompt selects first",
			inputName: "",
			services: map[string]*azdext.ServiceConfig{
				"alpha": {Name: "alpha", Host: AiAgentHost, RelativePath: "."},
				"beta":  {Name: "beta", Host: AiAgentHost, RelativePath: "."},
			},
			selectIndex: 0,
			wantName:    "alpha",
		},
		{
			name:      "multiple services prompt selects second",
			inputName: "",
			services: map[string]*azdext.ServiceConfig{
				"alpha": {Name: "alpha", Host: AiAgentHost, RelativePath: "."},
				"beta":  {Name: "beta", Host: AiAgentHost, RelativePath: "."},
			},
			selectIndex: 1,
			wantName:    "beta",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			projectServer := &helpersProjectServer{
				project: &azdext.ProjectConfig{
					Path:     svcDir,
					Services: tt.services,
				},
			}
			promptServer := &helpersPromptServer{selectIndex: tt.selectIndex}
			azdClient := newHelpersTestAzdClient(t, projectServer, promptServer)

			protocol, serviceName, err := resolveAgentProtocol(
				t.Context(), azdClient, tt.inputName, false,
			)
			require.NoError(t, err)
			require.Equal(t, "responses", string(protocol))
			require.Equal(t, tt.wantName, serviceName,
				"resolveAgentProtocol should return the resolved service name")
		})
	}
}

// TestResolveAgentProtocol_MultipleServicesPromptsOnce verifies that a single
// call to resolveAgentProtocol triggers exactly one prompt when there are
// multiple agent services and no name is provided.
func TestResolveAgentProtocol_MultipleServicesPromptsOnce(t *testing.T) {
	t.Parallel()

	svcDir := t.TempDir()
	agentYaml := "kind: hosted\nname: my-agent\nprotocols:\n  - protocol: responses\n    version: \"1.0\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(svcDir, "agent.yaml"), []byte(agentYaml), 0600))

	projectServer := &helpersProjectServer{
		project: &azdext.ProjectConfig{
			Path: svcDir,
			Services: map[string]*azdext.ServiceConfig{
				"svc-a": {Name: "svc-a", Host: AiAgentHost, RelativePath: "."},
				"svc-b": {Name: "svc-b", Host: AiAgentHost, RelativePath: "."},
			},
		},
	}
	promptServer := &helpersPromptServer{selectIndex: 0}
	azdClient := newHelpersTestAzdClient(t, projectServer, promptServer)

	_, serviceName, err := resolveAgentProtocol(t.Context(), azdClient, "", false)
	require.NoError(t, err)
	require.Equal(t, "svc-a", serviceName)
	require.Equal(t, int32(1), promptServer.selectCalls.Load(),
		"resolveAgentProtocol should trigger exactly one prompt")
}
