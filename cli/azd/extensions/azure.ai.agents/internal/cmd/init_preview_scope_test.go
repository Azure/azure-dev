// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"azureaiagent/internal/featureflags"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const initScopeNoProject = "no project exists; to create a new project, run `azd init`"

type initScopeEnvironmentServer struct {
	previewTestEnvironment
}

func (s *initScopeEnvironmentServer) Get(
	_ context.Context, request *azdext.GetEnvironmentRequest,
) (*azdext.EnvironmentResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.selected = request.Name
	if s.err != nil {
		return nil, s.err
	}
	return &azdext.EnvironmentResponse{Environment: &azdext.Environment{Name: request.Name}}, nil
}

type initScopePromptServer struct {
	azdext.UnimplementedPromptServiceServer
	selectMode func(*azdext.SelectRequest) (*azdext.SelectResponse, error)
	protocols  func(*azdext.MultiSelectRequest) (*azdext.MultiSelectResponse, error)
}

func (s *initScopePromptServer) Select(
	_ context.Context, request *azdext.SelectRequest,
) (*azdext.SelectResponse, error) {
	return s.selectMode(request)
}

func (s *initScopePromptServer) MultiSelect(
	_ context.Context, request *azdext.MultiSelectRequest,
) (*azdext.MultiSelectResponse, error) {
	return s.protocols(request)
}

type initScopeHost struct {
	project  helpersProjectServer
	env      initScopeEnvironmentServer
	config   previewTestConfig
	prompts  *initScopePromptServer
	workflow testWorkflowServiceServer
	mu       sync.Mutex
	methods  []string
}

func (s *initScopeHost) start(t *testing.T) {
	t.Helper()
	s.workflow.runErr = status.Error(codes.FailedPrecondition, "fixture stopped before init writes")
	server := grpc.NewServer(grpc.UnaryInterceptor(func(
		ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler,
	) (any, error) {
		s.mu.Lock()
		s.methods = append(s.methods, info.FullMethod)
		s.mu.Unlock()
		return handler(ctx, request)
	}))
	azdext.RegisterProjectServiceServer(server, &s.project)
	azdext.RegisterEnvironmentServiceServer(server, &s.env)
	azdext.RegisterUserConfigServiceServer(server, &s.config)
	azdext.RegisterWorkflowServiceServer(server, &s.workflow)
	azdext.RegisterAiModelServiceServer(server, &initAuthOrderingAiServer{})
	if s.prompts != nil {
		azdext.RegisterPromptServiceServer(server, s.prompts)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	t.Setenv("AZD_SERVER", listener.Addr().String())
	t.Setenv("AZD_ACCESS_TOKEN", "test-token")
	t.Setenv("AZD_EXT_DEBUG", "")
}

func (s *initScopeHost) called(serviceMethod string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.ContainsFunc(s.methods, func(method string) bool {
		return strings.HasSuffix(method, serviceMethod)
	})
}

func initScopeAuthFixture(t *testing.T, authenticated bool) string {
	t.Helper()
	binDir := t.TempDir()
	marker := filepath.Join(binDir, "auth-called")
	authStatus := "unauthenticated"
	if authenticated {
		authStatus = "authenticated"
	}
	name := "azd"
	body := "#!/bin/sh\nprintf checked > '" + marker + "'\nprintf '{\"status\":\"" + authStatus + "\"}\\n'\n"
	if runtime.GOOS == "windows" {
		name = "azd.cmd"
		body = "@echo off\r\necho checked>\"" + marker + "\"\r\necho {\"status\":\"" + authStatus + "\"}\r\n"
	}
	// The executable is an offline test fixture, not the real azd.
	executable := filepath.Join(binDir, name)
	require.NoError(t, os.WriteFile(executable, []byte(body), 0o600))
	//nolint:gosec // the fixture must be executable on Unix
	require.NoError(t, os.Chmod(executable, 0o700))
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return marker
}

func runInitScopeRoot(t *testing.T, args ...string) error {
	t.Helper()
	_, err := captureStdout(t, func() error {
		_, err := executeRoot(t, append([]string{"init"}, args...)...)
		return err
	})
	return err
}

func TestInitPreviewNamedCreationScope(t *testing.T) {
	for _, route := range []struct {
		name    string
		feature featureflags.Feature
		args    []string
	}{
		{"voice", featureflags.Voice,
			[]string{"--kind", "prompt-voice", "--agent-name", "voice-agent", "--model", "gpt-realtime"}},
		{"harness", featureflags.ManagedHarness,
			[]string{"--kind", "prompt", "--harness", "microsoft_copilot_preview",
				"--agent-name", "harness-agent", "--model", "gpt-4.1-mini"}},
		{"hosted voice", featureflags.Voice,
			[]string{"--image", "registry.example.com/agent:v1", "--agent-name", "hosted-agent",
				"--protocol", "invocations_ws"}},
	} {
		for _, scope := range []string{"fresh", "existing project", "existing environment"} {
			for _, source := range []string{"off", "process", "config", "dotenv"} {
				if source == "dotenv" && scope != "existing environment" {
					continue
				}
				for _, selection := range []string{"flag", "SDK environment"} {
					t.Run(strings.Join([]string{route.name, scope, source, selection}, "/"), func(t *testing.T) {
						clearPreviewProcess(t)
						root := t.TempDir()
						t.Chdir(root)
						host := &initScopeHost{}
						if scope == "fresh" {
							host.project.err = status.Error(codes.Unknown, initScopeNoProject)
						} else {
							host.project.project = &azdext.ProjectConfig{Path: root}
							if scope == "existing project" {
								host.env.err = status.Error(codes.Unknown, "environment not found")
							}
						}
						if source == "process" {
							t.Setenv(route.feature.EnvVar, "true")
						} else if source == "config" {
							host.config.alpha = map[string]any{"agents": map[string]any{
								strings.TrimPrefix(route.feature.ID, "agents."): "on",
							}}
						} else if source == "dotenv" {
							host.env.values = []*azdext.KeyValue{{Key: route.feature.EnvVar, Value: "true"}}
						}
						host.start(t)
						auth := initScopeAuthFixture(t, false)
						args := append(slices.Clone(route.args), "--no-prompt")
						if selection == "flag" {
							t.Setenv("AZD_ENVIRONMENT", "ambient")
							args = append(args, "-e", "new-env")
						} else {
							t.Setenv("AZD_ENVIRONMENT", "new-env")
						}
						err := runInitScopeRoot(t, args...)
						if source != "process" {
							require.ErrorContains(t, err, "preview feature")
							require.ErrorContains(t, err, route.feature.ID)
							_, statErr := os.Stat(auth)
							require.ErrorIs(t, statErr, os.ErrNotExist)
							require.False(t, host.called("AiModelService/ListModels"))
							require.Empty(t, host.methods)
						} else {
							require.ErrorContains(t, err, "not logged in")
							require.FileExists(t, auth)
						}
						require.False(t, host.called("UserConfigService/Get"))
						require.False(t, host.called("EnvironmentService/GetValues"))
						require.Zero(t, host.workflow.runCalls)
						entries, readErr := os.ReadDir(root)
						require.NoError(t, readErr)
						require.Empty(t, entries)
					})
				}
			}
		}
	}
}

func TestInitInteractiveNamedCreationScope(t *testing.T) {
	for _, scope := range []string{"fresh", "existing project"} {
		for _, withCode := range []bool{false, true} {
			for _, voiceEnabled := range []bool{false, true} {
				t.Run(strings.Join([]string{scope, strconv.FormatBool(withCode), strconv.FormatBool(voiceEnabled)}, "/"), func(t *testing.T) {
					clearPreviewProcess(t)
					root := t.TempDir()
					t.Chdir(root)
					if withCode {
						require.NoError(t, os.WriteFile("main.py", []byte("print('hello')\n"), 0o600))
					}
					modeCalls, protocolCalls := 0, 0
					host := &initScopeHost{prompts: &initScopePromptServer{
						selectMode: func(request *azdext.SelectRequest) (*azdext.SelectResponse, error) {
							modeCalls++
							var choices []string
							for _, choice := range request.Options.Choices {
								choices = append(choices, choice.Value)
							}
							expected := []string{initModeTemplate}
							if withCode {
								expected = append([]string{initModeFromCode}, expected...)
							}
							if voiceEnabled {
								expected = append(expected, initModeVoice)
							}
							require.Equal(t, expected, choices)
							if !withCode {
								return nil, status.Error(codes.Canceled, "fixture stopped at template choice")
							}
							return &azdext.SelectResponse{Value: new(int32(0))}, nil
						},
						protocols: func(request *azdext.MultiSelectRequest) (*azdext.MultiSelectResponse, error) {
							protocolCalls++
							var choices []string
							for _, choice := range request.Options.Choices {
								choices = append(choices, choice.Value)
								require.Equal(t, choice.Value == "responses", choice.Selected)
							}
							require.Equal(t, voiceEnabled, slices.Contains(choices, "invocations_ws"))
							return nil, status.Error(codes.Canceled, "fixture stopped before init writes")
						},
					}}
					if scope == "fresh" {
						host.project.err = status.Error(codes.Unknown, initScopeNoProject)
					} else {
						host.project.project = &azdext.ProjectConfig{Path: root}
						host.env.err = status.Error(codes.Unknown, "environment not found")
					}
					if voiceEnabled {
						t.Setenv(featureflags.Voice.EnvVar, "true")
					}
					host.start(t)
					initScopeAuthFixture(t, true)
					err := runInitScopeRoot(t, "-e", "new-env")
					require.ErrorContains(t, err, "cancelled")
					require.Equal(t, 1, modeCalls)
					if withCode {
						require.Equal(t, 1, protocolCalls)
					} else {
						require.Zero(t, protocolCalls)
					}
					require.Zero(t, host.workflow.runCalls)
					entries, readErr := os.ReadDir(root)
					require.NoError(t, readErr)
					require.Len(t, entries, map[bool]int{false: 0, true: 1}[withCode])
				})
			}
		}
	}
}

func TestInitOrdinaryProtocolsPreserveCreationSelection(t *testing.T) {
	for _, scope := range []string{"fresh", "existing project"} {
		for _, protocol := range []string{"", "responses", "invocations", "activity"} {
			t.Run(scope+"/"+protocol, func(t *testing.T) {
				clearPreviewProcess(t)
				root := t.TempDir()
				t.Chdir(root)
				require.NoError(t, os.WriteFile("main.py", []byte("print('hello')\n"), 0o600))
				host := &initScopeHost{}
				if scope == "fresh" {
					host.project.err = status.Error(codes.Unknown, initScopeNoProject)
				} else {
					host.project.project = &azdext.ProjectConfig{Path: root}
					host.env.err = status.Error(codes.Unknown, "environment not found")
				}
				host.start(t)
				initScopeAuthFixture(t, true)
				args := []string{"--no-prompt", "--agent-name", "agent", "-e", "new-env", "--deploy-mode", "container"}
				if protocol != "" {
					args = append(args, "--protocol", protocol)
				}
				err := runInitScopeRoot(t, args...)
				require.ErrorContains(t, err, "fixture stopped before init writes")
				require.False(t, host.called("UserConfigService/Get"))
				require.Equal(t, 1, host.workflow.runCalls)
				steps := host.workflow.request.Workflow.Steps
				require.Len(t, steps, 1)
				workflowArgs := steps[0].Command.Args
				if scope == "fresh" {
					require.Equal(t, "init", workflowArgs[0])
					require.Equal(t, []string{"--environment", "new-env"}, workflowArgs[len(workflowArgs)-2:])
				} else {
					require.Equal(t, []string{"env", "new", "new-env"}, workflowArgs)
				}
				entries, readErr := os.ReadDir(root)
				require.NoError(t, readErr)
				require.Len(t, entries, 1)
				require.Equal(t, "main.py", entries[0].Name())
			})
		}
	}
}
