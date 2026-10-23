// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"net"
	"strings"
	"sync"
	"testing"

	"azureaiagent/internal/inittelemetry"
	projectpkg "azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

type initBudgetTelemetryServer struct {
	azdext.UnimplementedTelemetryServiceServer
	helpersProjectServer
	fillBuffer func()
	fillOnce   sync.Once
	mu         sync.Mutex
	requests   []*azdext.ReportUsageRequest
	accepted   []bool
}

func (s *initBudgetTelemetryServer) Get(
	ctx context.Context, req *azdext.EmptyRequest,
) (*azdext.GetProjectResponse, error) {
	s.fillOnce.Do(s.fillBuffer)
	return s.helpersProjectServer.Get(ctx, req)
}

func (s *initBudgetTelemetryServer) ReportUsage(
	_ context.Context, req *azdext.ReportUsageRequest,
) (*azdext.ReportUsageResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	accepted := len(s.requests) < 100
	s.requests = append(s.requests, req)
	s.accepted = append(s.accepted, accepted)
	return &azdext.ReportUsageResponse{Accepted: accepted}, nil
}

type initBudgetPromptServer struct {
	azdext.UnimplementedPromptServiceServer
	code codes.Code
}

func (s *initBudgetPromptServer) Confirm(
	context.Context, *azdext.ConfirmRequest,
) (*azdext.ConfirmResponse, error) {
	return nil, status.Error(s.code, "reuse stopped")
}

func TestRootInitReportsExistingTelemetryBeforeFullTimingBuffer(t *testing.T) {
	for _, code := range []codes.Code{codes.OK, codes.Internal, codes.Canceled} {
		t.Run(code.String(), func(t *testing.T) {
			t.Chdir(t.TempDir())
			setTimedInitAuth(t)
			t.Setenv("AZD_ALPHA_ENABLE_AGENTS_VOICE", "true")
			promptProps, err := structpb.NewStruct(map[string]any{
				"kind": "prompt", "name": "prompt-agent", "model": "gpt-4.1-mini", "instructions": "Help.",
			})
			require.NoError(t, err)
			voiceProps, err := projectpkg.VoiceAgentDefinitionToServiceProperties(
				*voiceDefinitionForInit(&initFlags{}, "voice-agent"), nil,
			)
			require.NoError(t, err)
			project := &azdext.ProjectConfig{
				Path: t.TempDir(),
				Services: map[string]*azdext.ServiceConfig{
					"hosted": inlineAgentService(t, "hosted", "hosted-agent"),
					"prompt": {Name: "prompt", Host: AiAgentHost, AdditionalProperties: promptProps},
					"voice":  {Name: "voice", Host: AiAgentHost, AdditionalProperties: voiceProps},
				},
			}
			root := NewRootCommand()
			initCmd, _, err := root.Find([]string{"init"})
			require.NoError(t, err)
			capture := &initBudgetTelemetryServer{
				helpersProjectServer: helpersProjectServer{project: project},
				fillBuffer: func() {
					for range 96 {
						inittelemetry.Start(initCmd.Context(), inittelemetry.ModelSelection)()
					}
				},
			}
			server := grpc.NewServer()
			azdext.RegisterTelemetryServiceServer(server, capture)
			azdext.RegisterProjectServiceServer(server, capture)
			azdext.RegisterAiModelServiceServer(server, &initAuthOrderingAiServer{})
			azdext.RegisterEnvironmentServiceServer(server, &testEnvironmentServiceServer{
				current: &azdext.Environment{Name: "dev"},
			})
			azdext.RegisterPromptServiceServer(server, &initBudgetPromptServer{code: code})
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			done := make(chan error, 1)
			go func() { done <- server.Serve(listener) }()
			t.Cleanup(func() { server.Stop(); require.NoError(t, <-done) })
			t.Setenv("AZD_SERVER", listener.Addr().String())
			var output bytes.Buffer
			root.SetOut(&output)
			root.SetErr(&output)
			args := []string{"init"}
			if code == codes.OK {
				args = append(args, "--no-prompt")
			}
			root.SetArgs(args)
			err = root.ExecuteContext(t.Context())
			if code == codes.OK {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}

			capture.mu.Lock()
			defer capture.mu.Unlock()
			contexts, markers, timings, dropped := 0, 0, 0, 0
			for i, req := range capture.requests {
				switch {
				case req.EventName == "agent.init.step":
					timings++
					if !capture.accepted[i] {
						dropped++
					}
				case req.EventName == agentContextResolvedEvent:
					require.Zero(t, timings, "timing reports must not precede existing agent contexts")
					require.True(t, capture.accepted[i])
					contexts++
				case strings.HasPrefix(req.EventName, "agent.operation.v1.init."):
					require.Zero(t, timings, "timing reports must not precede existing operation markers")
					require.True(t, capture.accepted[i])
					markers++
				default:
					t.Fatalf("unexpected usage event %q", req.EventName)
				}
			}
			require.Equal(t, 96, timings, "the saturated buffer must be flushed exactly once")
			if code == codes.OK {
				require.Equal(t, 3, contexts)
				require.Equal(t, 3, markers)
				require.Equal(t, 2, dropped, "only timings may lose the last two host budget slots")
			} else {
				require.Zero(t, contexts, "errors and cancellation skip the success post-run hook")
				require.Equal(t, 1, markers)
				require.Zero(t, dropped)
			}
		})
	}
}
