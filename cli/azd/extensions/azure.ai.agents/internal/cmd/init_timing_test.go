// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"azureaiagent/internal/inittelemetry"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
			assert.Equal(t, "agent_name_selection", event.Attributes["init.step.id"])
			assert.Equal(t, "1", event.Attributes["init.step.sequence"])
			duration, err := strconv.ParseFloat(event.Attributes["duration_ms"], 64)
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
	assert.Equal(t, "init_mode_selection", recorder.events[0].Attributes["init.step.id"])
	assert.Equal(t, "agent_name_selection", recorder.events[1].Attributes["init.step.id"])
}
