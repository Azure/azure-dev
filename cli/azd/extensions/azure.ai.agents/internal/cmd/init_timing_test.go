// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"azureaiagent/internal/inittelemetry"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	contracts "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1"
	foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type initTimingRecorder struct {
	events []foundryTelemetry.Event
}

func (r *initTimingRecorder) Report(_ context.Context, event foundryTelemetry.Event) {
	r.events = append(r.events, event)
}

type initTimingPromptServer struct {
	testPromptServiceServer
	err error
}

func (s *initTimingPromptServer) Prompt(
	ctx context.Context, request *azdext.PromptRequest,
) (*azdext.PromptResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.testPromptServiceServer.Prompt(ctx, request)
}

func TestInitStepTimingSuccessRetryAndCancellation(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, test := range []struct {
		name    string
		answers []string
		err     error
	}{
		{"success", []string{"my-agent"}, nil},
		{"retry", []string{"invalid/name", "my-agent"}, nil},
		{"canceled", nil, status.Error(codes.Canceled, "private response")},
		{"error", nil, status.Error(codes.Internal, "private response")},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := &initTimingPromptServer{
				testPromptServiceServer: testPromptServiceServer{promptResponses: test.answers},
				err:                     test.err,
			}
			client := newTestAzdClient(t, &testEnvironmentServiceServer{}, nil, server)
			ctx, timing := inittelemetry.NewContext(t.Context())
			name, err := resolveInitAgentName(ctx, client, &initFlags{}, "default-agent")
			if test.err == nil {
				require.NoError(t, err)
				assert.Equal(t, "my-agent", name)
				assert.Len(t, server.promptRequests, len(test.answers))
			} else {
				require.Error(t, err)
			}
			recorder := &initTimingRecorder{}
			timing.Flush(ctx, recorder)
			require.Len(t, recorder.events, 1, "one business step includes all its loading and retries")
			event := recorder.events[0]
			assert.Equal(t, "agent.init.step", event.Name)
			require.Len(t, event.Attributes, 3)
			assert.Equal(t, "agent_name_selection", event.Attributes["agent.init.step.id"])
			assert.Equal(t, "1", event.Attributes["agent.init.step.sequence"])
			duration, err := strconv.ParseFloat(event.Attributes["agent.init.step.duration_ms"], 64)
			require.NoError(t, err)
			assert.GreaterOrEqual(t, duration, 0.0)
			for _, private := range []string{"my-agent", "invalid/name", "private response", "Enter a name"} {
				assert.NotContains(t, fmt.Sprint(event), private)
			}
		})
	}
}

func TestInitStepTimingNonInteractive(t *testing.T) {
	t.Chdir(t.TempDir())
	ctx, timing := inittelemetry.NewContext(t.Context())
	mode, err := promptInitMode(ctx, nil, true)
	require.NoError(t, err)
	assert.Equal(t, initModeTemplate, mode)
	name, err := resolveInitAgentName(ctx, nil, &initFlags{
		noPrompt: true, agentName: "scripted-agent",
	}, "default-agent")
	require.NoError(t, err)
	assert.Equal(t, "scripted-agent", name)
	recorder := &initTimingRecorder{}
	timing.Flush(ctx, recorder)
	require.Len(t, recorder.events, 2)
	assert.Equal(t, "init_mode_selection", recorder.events[0].Attributes["agent.init.step.id"])
	assert.Equal(t, "agent_name_selection", recorder.events[1].Attributes["agent.init.step.id"])
}

func recordedStepDurations(t *testing.T, recorder *initTimingRecorder, id inittelemetry.Step) []float64 {
	t.Helper()
	var durations []float64
	for _, event := range recorder.events {
		if event.Attributes["agent.init.step.id"] == string(id) {
			duration, err := strconv.ParseFloat(event.Attributes["agent.init.step.duration_ms"], 64)
			require.NoError(t, err)
			durations = append(durations, duration)
		}
	}
	return durations
}

func TestInitStepTimingAzureYamlScaffoldingFailure(t *testing.T) {
	for _, code := range []codes.Code{codes.Internal, codes.Canceled} {
		t.Run(code.String(), func(t *testing.T) {
			t.Chdir(t.TempDir())
			templateDir := t.TempDir()
			content := []byte("services:\n  agent:\n    host: azure.ai.agent\n    kind: hosted\n    name: agent\n")
			pointer := filepath.Join(templateDir, "azure.yaml")
			require.NoError(t, os.WriteFile(pointer, content, 0o600))
			workflow := &testWorkflowServiceServer{runErr: status.Error(code, "scaffolding stopped")}
			client := newTestAzdClient(t, &testEnvironmentServiceServer{}, workflow)
			ctx, timing := inittelemetry.NewContext(t.Context())
			err := runInitFromAzureYaml(ctx, &initFlags{
				templatePointer: pointer, src: "project",
			}, client, http.DefaultClient, content, "")
			require.Error(t, err)
			require.Equal(t, 1, workflow.runCalls)
			recorder := &initTimingRecorder{}
			timing.Flush(ctx, recorder)
			require.Len(t, recordedStepDurations(t, recorder, inittelemetry.ProjectScaffolding), 1)
		})
	}
}

func TestInitStepTimingGeneratedProjectOnlyRecordsScaffolding(t *testing.T) {
	for _, target := range []string{"", "image-agent"} {
		for _, existing := range []bool{false, true} {
			t.Run(fmt.Sprintf("target=%s/existing=%t", target, existing), func(t *testing.T) {
				root := t.TempDir()
				t.Chdir(root)
				var client *azdext.AzdClient
				if existing {
					client = newHelpersTestAzdClient(t, &helpersProjectServer{project: &azdext.ProjectConfig{
						Path: root, Infra: &azdext.InfraOptions{Provider: "microsoft.foundry"},
					}}, &helpersPromptServer{})
				} else {
					client = newTestAzdClient(t, &testEnvironmentServiceServer{}, &testWorkflowServiceServer{
						runErr: status.Error(codes.Internal, "scaffolding stopped"),
					})
				}
				ctx, timing := inittelemetry.NewContext(t.Context())
				action := &InitFromCodeAction{azdClient: client, flags: &initFlags{}, projectTargetDir: target}
				_, err := action.ensureProject(ctx)
				if existing {
					require.NoError(t, err)
				} else {
					require.ErrorContains(t, err, "scaffolding stopped")
				}
				recorder := &initTimingRecorder{}
				timing.Flush(ctx, recorder)
				durations := recordedStepDurations(t, recorder, inittelemetry.ProjectScaffolding)
				if existing {
					require.Empty(t, durations, "existing projects must not add no-op scaffolding samples")
				} else {
					require.Len(t, durations, 1)
				}
			})
		}
	}
}

const locationTimingDelay = 10 * time.Millisecond

type timingLocationEnvironmentServer struct {
	testEnvironmentServiceServer
	writes int
	fail   bool
}

func (s *timingLocationEnvironmentServer) SetValue(
	ctx context.Context, req *azdext.SetEnvRequest,
) (*azdext.EmptyResponse, error) {
	s.writes++
	time.Sleep(locationTimingDelay)
	if s.fail {
		return nil, status.Error(codes.Internal, "environment write failed")
	}
	return s.testEnvironmentServiceServer.SetValue(ctx, req)
}

func TestInitStepTimingLocationIncludesLoadingAndPersistence(t *testing.T) {
	for _, test := range []struct {
		name       string
		configured bool
		direct     bool
		fallback   bool
		failWrite  bool
	}{
		{name: "prompt"},
		{name: "configured", configured: true},
		{name: "region fallback", fallback: true},
		{name: "explicit allowlist", direct: true},
		{name: "explicit allowlist configured", direct: true, configured: true},
		{name: "write failure", failWrite: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			resetRegionsCache(t, nil)
			var hits atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hits.Add(1)
				time.Sleep(locationTimingDelay)
				if test.fallback {
					w.WriteHeader(http.StatusInternalServerError)
				} else {
					_, _ = w.Write([]byte(`{"regions":["japaneast"]}`))
				}

			}))
			t.Cleanup(server.Close)
			previous := hostedAgentRegionsURL
			hostedAgentRegionsURL = server.URL
			t.Cleanup(func() { hostedAgentRegionsURL = previous })
			environment := &timingLocationEnvironmentServer{fail: test.failWrite}
			prompts := &harnessLocationPromptServer{}
			client := newTestAzdClient(t, environment, nil, prompts)
			azureContext := &azdext.AzureContext{Scope: &azdext.AzureScope{}}
			if test.configured {
				azureContext.Scope.Location = "japaneast"
			}
			ctx, timing := inittelemetry.NewContext(t.Context())
			var err error
			if test.direct {
				err = ensureLocationWithAllowedLocations(ctx, client, azureContext, "dev", []string{"japaneast"})
			} else {
				err = ensureLocation(ctx, client, azureContext, "dev")
			}
			if test.failWrite {
				require.ErrorContains(t, err, "environment write failed")
			} else {
				require.NoError(t, err)
				require.Equal(t, "japaneast", environment.values["dev"]["AZURE_LOCATION"])
				require.Equal(t, "japaneast", environment.values["dev"]["AZURE_AI_DEPLOYMENTS_LOCATION"])
				require.Equal(t, 2, environment.writes)
			}
			if test.configured {
				require.Nil(t, prompts.request)
			} else {
				require.NotNil(t, prompts.request)
			}
			expectedLoads := int32(1)
			if test.direct {
				expectedLoads = 0
			}
			require.Equal(t, expectedLoads, hits.Load())
			recorder := &initTimingRecorder{}
			timing.Flush(ctx, recorder)
			durations := recordedStepDurations(t, recorder, inittelemetry.LocationSelection)
			require.Len(t, durations, 1, "loading, prompting and persistence must form a single sample")
			minimum := time.Duration(environment.writes+int(expectedLoads)) * locationTimingDelay
			require.GreaterOrEqual(t, durations[0], float64(minimum.Milliseconds()))
		})
	}
}

func setTimedInitAuth(t *testing.T) {
	t.Helper()
	t.Setenv("AZD_EXT_DEBUG", "")
	t.Setenv("AZD_EXEC_PROJECT_DIR", "")
	t.Setenv("NO_COLOR", "1")
	bin := t.TempDir()
	executable, script := "azd", "#!/bin/sh\nprintf '{\"status\":\"authenticated\"}\\n'\n"
	if runtime.GOOS == "windows" {
		executable, script = "azd.cmd", "@echo {\"status\":\"authenticated\"}\r\n"
	}
	path := filepath.Join(bin, executable)
	require.NoError(t, os.WriteFile(path, []byte(script), 0o600))
	//nolint:gosec // the fake auth-status executable must run on Unix.
	require.NoError(t, os.Chmod(path, 0o700))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func executeTimedInit(
	t *testing.T,
	args []string,
	noPrompt bool,
	project azdext.ProjectServiceServer,
	prompts azdext.PromptServiceServer,
	environment azdext.EnvironmentServiceServer,
) (error, *initTimingRecorder) {
	t.Helper()
	setTimedInitAuth(t)
	server := grpc.NewServer()
	capture := &operationTelemetryServer{}
	contracts.RegisterTelemetryServiceServer(server, capture)
	azdext.RegisterAiModelServiceServer(server, &initAuthOrderingAiServer{})
	if project != nil {
		azdext.RegisterProjectServiceServer(server, project)
	}
	if prompts != nil {
		azdext.RegisterPromptServiceServer(server, prompts)
	}
	if environment != nil {
		azdext.RegisterEnvironmentServiceServer(server, environment)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); require.NoError(t, <-done) })
	t.Setenv("AZD_SERVER", listener.Addr().String())

	command := NewRootCommand()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SilenceUsage, command.SilenceErrors = true, true
	commandArgs := []string{"init"}
	if noPrompt {
		commandArgs = append(commandArgs, "--no-prompt")
	}
	command.SetArgs(append(commandArgs, args...))
	runErr := command.Execute()
	recorder := &initTimingRecorder{}
	capture.mu.Lock()
	defer capture.mu.Unlock()
	for _, event := range capture.events {
		if event.EventName == "agent.init.step" {
			recorder.events = append(recorder.events, foundryTelemetry.Event{
				Name: event.EventName, Attributes: event.Attributes,
			})
		}
	}
	return runErr, recorder
}

func TestInitStepTimingLateValidationExcludesAuthentication(t *testing.T) {
	for _, test := range []struct {
		name           string
		args           []string
		wantErr        string
		postAuth       bool
		invalidProject bool
		template       bool
		standalone     bool
	}{
		{name: "infra parsing", args: []string{"--infra=invalid"}, wantErr: "unsupported --infra"},
		{name: "standalone infra conflict", args: []string{"--infra", "--kind", "hosted"},
			wantErr: "--kind", standalone: true},
		{name: "kind", args: []string{"--kind", "invalid"}, wantErr: "unknown --kind", postAuth: true},
		{name: "harness", args: []string{"--kind", "prompt", "--harness", "invalid"}, wantErr: "unknown --harness", postAuth: true},
		{name: "registry conflict", args: []string{"--registry-connection", "connection"},
			wantErr: "--registry-connection", postAuth: true},
		{name: "project service", wantErr: "agent definition not found", invalidProject: true},
		{name: "unified template conflict", args: []string{"--kind", "hosted"},
			wantErr: "project adoption cannot apply", template: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			config := &azdext.ProjectConfig{Path: root}
			if test.standalone {
				require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte(
					"name: test\nservices:\n  project:\n    host: azure.ai.project\n",
				), 0o600))
			}
			if test.invalidProject {
				config.Services = map[string]*azdext.ServiceConfig{"agent": {Name: "agent", Host: AiAgentHost}}
			}
			args := append([]string(nil), test.args...)
			if test.template {
				pointer := filepath.Join(root, "azure.yaml")
				require.NoError(t, os.WriteFile(pointer, []byte(
					"services:\n  agent:\n    host: azure.ai.agent\n    kind: prompt\n    name: agent\n",
				), 0o600))
				args = append(args, "-t", pointer)
			}
			err, recorder := executeTimedInit(t, args, true, &helpersProjectServer{project: config}, nil, nil)
			require.ErrorContains(t, err, test.wantErr)
			validationSequences := []int{}
			authSequence := 0
			for _, event := range recorder.events {
				sequence, parseErr := strconv.Atoi(event.Attributes["agent.init.step.sequence"])
				require.NoError(t, parseErr)
				switch event.Attributes["agent.init.step.id"] {
				case string(inittelemetry.InputValidation):
					validationSequences = append(validationSequences, sequence)
				case string(inittelemetry.Authentication):
					authSequence = sequence
				}
			}
			if test.postAuth {
				require.Len(t, validationSequences, 3, "the failing late group must have its own completed timing")
				require.Greater(t, authSequence, validationSequences[1])
				require.Less(t, authSequence, validationSequences[2])
			} else {
				require.Len(t, validationSequences, 2)
				require.Zero(t, authSequence)
			}
		})
	}
}

func TestInitStepTimingExistingProjectReuse(t *testing.T) {
	for _, noPrompt := range []bool{false, true} {
		t.Run(fmt.Sprintf("noPrompt=%t", noPrompt), func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			prompts := &testPromptServiceServer{confirmResponses: []bool{true}}
			project := &helpersProjectServer{project: &azdext.ProjectConfig{
				Path: root, Services: map[string]*azdext.ServiceConfig{"chat": inlineAgentService(t, "chat", "chat-agent")},
			}}
			environment := &testEnvironmentServiceServer{current: &azdext.Environment{Name: "dev"}}
			err, recorder := executeTimedInit(t, nil, noPrompt, project, prompts, environment)
			require.NoError(t, err)
			if noPrompt {
				require.Empty(t, prompts.confirmRequests)
			} else {
				require.Len(t, prompts.confirmRequests, 1)
			}
			require.Len(t, recordedStepDurations(t, recorder, inittelemetry.InitModeSelection), 1)
			require.Empty(t, recordedStepDurations(t, recorder, inittelemetry.ProjectScaffolding))
		})
	}
}

func TestInitStepTimingVoiceConfigurationBothEntryPoints(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprintf("explicit-kind=%t", explicit), func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			t.Setenv("AZD_ALPHA_ENABLE_AGENTS_VOICE", "true")
			project := &helpersProjectServer{project: &azdext.ProjectConfig{
				Path: root, Infra: &azdext.InfraOptions{Provider: "microsoft.foundry"},
			}}
			prompts := &helpersPromptServer{selectIndex: 1, promptValue: "voice-agent"}
			environment := &helpersFailingEnvironmentServer{
				testEnvironmentServiceServer: testEnvironmentServiceServer{current: &azdext.Environment{Name: "dev"}},
				getValuesErr:                 status.Error(codes.Internal, "voice setup stopped"),
			}
			var args []string
			if explicit {
				args = []string{"--kind", "prompt-voice", "--agent-name", "voice-agent"}
			}
			err, recorder := executeTimedInit(t, args, explicit, project, prompts, environment)
			require.ErrorContains(t, err, "voice setup stopped")
			require.Len(t, recordedStepDurations(t, recorder, inittelemetry.AgentConfiguration), 1)
			require.Empty(t, recordedStepDurations(t, recorder, inittelemetry.ProjectConfiguration),
				"agent setup must be timed even when it fails before updating the project")
			if explicit {
				require.Zero(t, prompts.selectCalls.Load())
			} else {
				require.EqualValues(t, 1, prompts.selectCalls.Load())
				require.Contains(t, prompts.lastSelect.Options.Choices[1].Label, "voice")
			}
		})
	}
}
