// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/inittelemetry"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
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
	promptErr error
}

func (s *initTimingPromptServer) Prompt(
	ctx context.Context, req *azdext.PromptRequest,
) (*azdext.PromptResponse, error) {
	if s.promptErr != nil {
		return nil, s.promptErr
	}
	return s.testPromptServiceServer.Prompt(ctx, req)
}

func TestInitTimingInteractiveNameRetriesAndCancellation(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, test := range []struct {
		name        string
		answers     []string
		promptErr   error
		promptCount string
		outcome     string
	}{
		{"success", []string{"my-agent"}, nil, "1", "success"},
		{"retry", []string{"invalid/name", "my-agent"}, nil, "2", "success"},
		{"cancel", nil, status.Error(codes.Canceled, "customer answer"), "1", "canceled"},
		{"timeout", nil, status.Error(codes.DeadlineExceeded, "customer answer"), "1", "timeout"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := &initTimingPromptServer{
				testPromptServiceServer: testPromptServiceServer{promptResponses: test.answers},
				promptErr:               test.promptErr,
			}
			client := newTestAzdClient(t, &testEnvironmentServiceServer{}, nil, server)
			ctx, timing := inittelemetry.NewContext(t.Context(), inittelemetry.AgentInit, false)
			name, err := resolveInitAgentName(ctx, client, &initFlags{}, "default-agent")
			if test.promptErr == nil {
				require.NoError(t, err)
				assert.Equal(t, "my-agent", name)
			} else {
				require.Error(t, err)
				if test.outcome == "canceled" {
					localErr, ok := errors.AsType[*azdext.LocalError](err)
					require.True(t, ok)
					assert.Equal(t, exterrors.CodeCancelled, localErr.Code)
				}
			}
			recorder := &initTimingRecorder{}
			timing.Flush(ctx, recorder, err)
			require.NotEmpty(t, recorder.events)
			assert.Equal(t, test.promptCount, recorder.events[0].Attributes["init.prompt.count"])
			// The raw prompt error is classified before existing command converters run.
			assert.Equal(t, test.outcome, recorder.events[1].Attributes["init.step.outcome"])
			if test.name == "retry" {
				require.Len(t, recorder.events, 5)
				assert.Equal(t, "error", recorder.events[2].Attributes["init.step.outcome"])
				assert.Equal(t, "input_validation", recorder.events[2].Attributes["init.step.id"])
				assert.Equal(t, "1", recorder.events[1].Attributes["init.step.sequence"])
				assert.Equal(t, "3", recorder.events[3].Attributes["init.step.sequence"])
				assert.Equal(t, recorder.events[1].Attributes["init.step.id"], recorder.events[3].Attributes["init.step.id"])
			}
			assert.NotContains(t, fmtEvents(recorder.events), "my-agent")
			assert.NotContains(t, fmtEvents(recorder.events), "customer answer")
		})
	}
}

func fmtEvents(events []foundryTelemetry.Event) string {
	var buffer strings.Builder
	for _, event := range events {
		buffer.WriteString(event.Name)
		for key, value := range event.Attributes {
			buffer.WriteString(key)
			buffer.WriteString(value)
		}
	}
	return buffer.String()
}

func TestInitTimingNoPromptDoesNotRecordWaiting(t *testing.T) {
	t.Chdir(t.TempDir())
	ctx, timing := inittelemetry.NewContext(t.Context(), inittelemetry.AgentInit, true)
	mode, err := promptInitMode(ctx, nil, true)
	require.NoError(t, err)
	assert.Equal(t, initModeTemplate, mode)
	name, err := resolveInitAgentName(ctx, nil, &initFlags{
		noPrompt: true, agentName: "scripted-agent", agentNameExplicit: true,
	}, "default-agent")
	require.NoError(t, err)
	assert.Equal(t, "scripted-agent", name)
	recorder := &initTimingRecorder{}
	timing.Flush(ctx, recorder, nil)
	require.Len(t, recorder.events, 2)
	assert.Equal(t, "0", recorder.events[0].Attributes["init.prompt.count"])
	assert.Equal(t, "0.000", recorder.events[0].Attributes["init.prompt.total_duration_ms"])
	assert.Equal(t, "none", recorder.events[0].Attributes["init.prompt.longest.id"])
	assert.Equal(t, "execution", recorder.events[1].Attributes["init.step.category"])
	assert.Equal(t, "flag", recorder.events[1].Attributes["init.step.input_source"])
}

type initTimingTelemetryServer struct {
	v1beta.UnimplementedTelemetryServiceServer
	mu           sync.Mutex
	requests     []*v1beta.ReportUsageRequest
	traceparents []string
	reportErr    error
}

func (s *initTimingTelemetryServer) ReportUsage(
	ctx context.Context, req *v1beta.ReportUsageRequest,
) (*v1beta.ReportUsageResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, req)
	meta, _ := metadata.FromIncomingContext(ctx)
	s.traceparents = append(s.traceparents, strings.Join(meta.Get("traceparent"), ""))
	return &v1beta.ReportUsageResponse{Accepted: true}, s.reportErr
}

func TestInitTimingCommandValidationFailureReportsOnOriginalTrace(t *testing.T) {
	for _, reportErr := range []error{nil, status.Error(codes.Unavailable, "sensitive detail")} {
		t.Run(status.Code(reportErr).String(), func(t *testing.T) {
			telemetry := &initTimingTelemetryServer{reportErr: reportErr}
			server := grpc.NewServer()
			v1beta.RegisterTelemetryServiceServer(server, telemetry)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			serveDone := make(chan error, 1)
			go func() { serveDone <- server.Serve(listener) }()
			t.Cleanup(func() {
				server.Stop()
				require.NoError(t, <-serveDone)
			})
			t.Setenv("AZD_SERVER", listener.Addr().String())
			const parent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
			t.Setenv("TRACEPARENT", parent)
			command := newInitCommand(&azdext.ExtensionContext{NoPrompt: true})
			command.SetOut(&bytes.Buffer{})
			command.SetErr(&bytes.Buffer{})
			command.SilenceUsage = true
			command.SilenceErrors = true
			command.SetArgs([]string{"--template", "one.yaml", "--manifest", "two.yaml"})
			err = command.ExecuteContext(t.Context())
			require.Error(t, err)
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			assert.Equal(t, exterrors.CodeConflictingArguments, localErr.Code)
			telemetry.mu.Lock()
			defer telemetry.mu.Unlock()
			require.Len(t, telemetry.requests, 3)
			assert.True(t, strings.HasPrefix(telemetry.requests[0].EventName, "agent.operation.v1.init."))
			assert.Equal(t, "agent.init.summary", telemetry.requests[1].EventName)
			assert.Equal(t, "error", telemetry.requests[1].Attributes["init.flow.outcome"])
			assert.Equal(t, "input_validation", telemetry.requests[2].Attributes["init.step.id"])
			assert.Equal(t, []string{parent, parent, parent}, telemetry.traceparents)
		})
	}
}

func TestInitPromptTimingCoverage(t *testing.T) {
	files, err := filepath.Glob("init*.go")
	require.NoError(t, err)
	files = append(files, "helpers.go")
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		require.NoError(t, err)
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			method, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			clientCall, ok := method.X.(*ast.CallExpr)
			if !ok {
				return true
			}
			clientMethod, ok := clientCall.Fun.(*ast.SelectorExpr)
			if ok && clientMethod.Sel.Name == "Prompt" && len(clientCall.Args) == 0 {
				t.Errorf("%s: uninstrumented prompt RPC %s", path, method.Sel.Name)
			}
			return true
		})
	}
}

func TestInitTimingImplementationsStayAligned(t *testing.T) {
	for _, name := range []string{"tracker.go", "prompt.go", "tracker_test.go"} {
		agentPath := filepath.Join("..", "inittelemetry", name)
		projectPath := filepath.Join("..", "..", "..", "azure.ai.projects", "internal", "inittelemetry", name)
		agentSource, err := os.ReadFile(agentPath)
		require.NoError(t, err)
		projectSource, err := os.ReadFile(projectPath)
		require.NoError(t, err)
		assert.Equal(t, string(agentSource), string(projectSource), name)
	}
}
