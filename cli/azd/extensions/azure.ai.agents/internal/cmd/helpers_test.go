// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	goyaml "go.yaml.in/yaml/v3"
	"google.golang.org/grpc"
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

func (s *helpersPromptServer) Select(
	_ context.Context, req *azdext.SelectRequest,
) (*azdext.SelectResponse, error) {
	s.selectCalls.Add(1)
	idx := s.selectIndex
	return &azdext.SelectResponse{Value: &idx}, nil
}

// newHelpersTestAzdClient spins up a gRPC server with the supplied Project and
// Prompt stubs and returns a client wired to its address.
func newHelpersTestAzdClient(
	t *testing.T,
	projectServer *helpersProjectServer,
	promptServer *helpersPromptServer,
) *azdext.AzdClient {
	t.Helper()

	grpcServer := grpc.NewServer()
	azdext.RegisterProjectServiceServer(grpcServer, projectServer)
	azdext.RegisterPromptServiceServer(grpcServer, promptServer)

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

// withInteractiveAgentSelection overrides the agent-service picker's TTY check for the
// duration of the test. The seam is process-global, so tests that use it must not run in
// parallel (nor use parallel subtests) to avoid racing on the shared variable.
func withInteractiveAgentSelection(t *testing.T, interactive bool) {
	t.Helper()
	prev := agentSelectionInteractive
	agentSelectionInteractive = func() bool { return interactive }
	t.Cleanup(func() { agentSelectionInteractive = prev })
}

// TestResolveAgentProtocol_ReturnsServiceName verifies that resolveAgentProtocol
// returns the resolved service name alongside the protocol, so callers can cache
// it and avoid a redundant prompt.
func TestResolveAgentProtocol_ReturnsServiceName(t *testing.T) {
	withInteractiveAgentSelection(t, true)

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
	withInteractiveAgentSelection(t, true)

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

func twoAgentServices() []*azdext.ServiceConfig {
	return []*azdext.ServiceConfig{
		{Name: "svc-b", Host: AiAgentHost, RelativePath: "."},
		{Name: "svc-a", Host: AiAgentHost, RelativePath: "."},
	}
}

// TestPromptForAgentService_NonInteractiveFailsFast verifies that, without a TTY, the picker
// fails fast with a structured error instead of blocking on stdin, and never calls the host prompt.
func TestPromptForAgentService_NonInteractiveFailsFast(t *testing.T) {
	withInteractiveAgentSelection(t, false)

	promptServer := &helpersPromptServer{}
	azdClient := newHelpersTestAzdClient(t, &helpersProjectServer{}, promptServer)

	svc, err := promptForAgentService(t.Context(), azdClient, twoAgentServices(), false)
	require.Nil(t, svc)
	require.Error(t, err)

	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok, "expected a *azdext.LocalError")
	require.Equal(t, exterrors.CodeNonInteractiveAgentSelection, localErr.Code)
	// The available services are listed to help the user pick one.
	require.Contains(t, localErr.Error(), "svc-a")
	require.Contains(t, localErr.Error(), "svc-b")

	require.Equal(t, int32(0), promptServer.selectCalls.Load(),
		"the host prompt must not be invoked in a non-interactive context")
}

// TestPromptForAgentService_NoPromptFailsFast verifies that --no-prompt fails fast without
// invoking the host prompt, independent of TTY state.
func TestPromptForAgentService_NoPromptFailsFast(t *testing.T) {
	withInteractiveAgentSelection(t, true)

	promptServer := &helpersPromptServer{}
	azdClient := newHelpersTestAzdClient(t, &helpersProjectServer{}, promptServer)

	svc, err := promptForAgentService(t.Context(), azdClient, twoAgentServices(), true)
	require.Nil(t, svc)
	require.Error(t, err)
	require.Contains(t, err.Error(), "svc-a")
	require.Equal(t, int32(0), promptServer.selectCalls.Load())
}

// TestPromptForAgentService_InteractivePrintsBanner verifies that, with a TTY, a one-line banner
// is written to stderr before the picker is rendered.
func TestPromptForAgentService_InteractivePrintsBanner(t *testing.T) {
	withInteractiveAgentSelection(t, true)

	// Capture stderr for the duration of the call.
	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = oldStderr })

	promptServer := &helpersPromptServer{selectIndex: 0}
	azdClient := newHelpersTestAzdClient(t, &helpersProjectServer{}, promptServer)

	svc, err := promptForAgentService(t.Context(), azdClient, twoAgentServices(), false)
	require.NoError(t, w.Close())
	os.Stderr = oldStderr

	require.NoError(t, err)
	require.NotNil(t, svc)
	require.Equal(t, int32(1), promptServer.selectCalls.Load())

	out, readErr := io.ReadAll(r)
	require.NoError(t, readErr)
	require.Contains(t, string(out), "azd is waiting for your agent service selection")
}
