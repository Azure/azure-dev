// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package inittelemetry

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type recordingReporter struct {
	events []foundryTelemetry.Event
	ctx    context.Context
	ctxErr error
}

func (r *recordingReporter) Report(ctx context.Context, event foundryTelemetry.Event) {
	r.events = append(r.events, event)
	r.ctx = ctx
	r.ctxErr = ctx.Err()
}

func TestTrackerMeasuresStepsAndSummary(t *testing.T) {
	ctx, tracker := NewContext(t.Context(), AgentInit, false)
	now := time.Now()
	tracker.now = func() time.Time { return now }
	external := Start(ctx, ModelCatalogLoading, External, "")
	now = now.Add(20 * time.Millisecond)
	external(nil)
	first := Start(ctx, ModelSelection, Prompt, User)
	now = now.Add(1500 * time.Microsecond)
	first(nil)
	second := Start(ctx, DeployModeSelection, Prompt, User)
	now = now.Add(3 * time.Millisecond)
	second(nil)
	second(errors.New("duplicate completion must not overwrite the result"))

	reporter := &recordingReporter{}
	tracker.Flush(ctx, reporter, nil)

	require.Len(t, reporter.events, 4)
	assert.Equal(t, foundryTelemetry.Event{
		Name: "agent.init.summary",
		Attributes: map[string]string{
			"init.flow.version":               "1",
			"init.flow.scope":                 "agent.init",
			"init.flow.outcome":               "success",
			"init.prompt.count":               "2",
			"init.prompt.total_duration_ms":   "4.500",
			"init.prompt.longest.id":          "deploy_mode_selection",
			"init.prompt.longest_duration_ms": "3.000",
			"init.step.count":                 "3",
			"init.step.dropped_count":         "0",
		},
	}, reporter.events[0])
	assert.Equal(t, foundryTelemetry.Event{
		Name: "agent.init.step",
		Attributes: map[string]string{
			"init.flow.version":      "1",
			"init.flow.scope":        "agent.init",
			"init.step.id":           "model_selection",
			"init.step.category":     "prompt",
			"init.step.sequence":     "2",
			"init.step.outcome":      "success",
			"init.step.timing_scope": "operation",
			"init.step.input_source": "user",
			"duration_ms":            "1.500",
		},
	}, reporter.events[2])
	assert.Equal(t, "20.000", reporter.events[1].Attributes["duration_ms"])
	assert.NotContains(t, reporter.events[1].Attributes, "init.step.input_source")
}

func TestTrackerRetriesAndOutcomes(t *testing.T) {
	for _, test := range []struct {
		name    string
		err     error
		outcome string
	}{
		{"error", errors.New("sensitive error detail"), "error"},
		{"canceled", fmt.Errorf("wrapped: %w", context.Canceled), "canceled"},
		{"grpc canceled", status.Error(codes.Canceled, "sensitive answer"), "canceled"},
		{"timeout", context.DeadlineExceeded, "timeout"},
		{"grpc timeout", status.Error(codes.DeadlineExceeded, "sensitive URL"), "timeout"},
		{"network timeout", &net.DNSError{IsTimeout: true}, "timeout"},
		{"structured canceled", &azdext.LocalError{
			Category: azdext.LocalErrorCategoryUser, Code: "cancelled", Message: "sensitive name",
		}, "canceled"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, tracker := NewContext(t.Context(), AgentInit, false)
			for _, err := range []error{test.err, nil} {
				got, gotErr := Measure(ctx, ModelSelection, External, "", func() (int, error) {
					return 42, err
				})
				assert.Equal(t, 42, got)
				assert.Equal(t, err, gotErr)
			}
			reporter := &recordingReporter{}
			tracker.Flush(ctx, reporter, test.err)
			require.Len(t, reporter.events, 3)
			assert.Equal(t, test.outcome, reporter.events[0].Attributes["init.flow.outcome"])
			assert.Equal(t, test.outcome, reporter.events[1].Attributes["init.step.outcome"])
			assert.Equal(t, "success", reporter.events[2].Attributes["init.step.outcome"])
			assert.Equal(t, "1", reporter.events[1].Attributes["init.step.sequence"])
			assert.Equal(t, "2", reporter.events[2].Attributes["init.step.sequence"])
			assert.Equal(t, reporter.events[1].Attributes["init.step.id"], reporter.events[2].Attributes["init.step.id"])
			assert.NotContains(t, fmt.Sprint(reporter.events), "sensitive")
		})
	}
}

func TestTrackerCanceledContextPreservesTrace(t *testing.T) {
	const traceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	ctx := propagation.TraceContext{}.Extract(t.Context(), propagation.MapCarrier{"traceparent": traceparent})
	ctx = azdext.WithAccessToken(ctx, "test-token")
	ctx, cancel := context.WithCancel(ctx)
	ctx, tracker := NewContext(ctx, AgentInit, false)
	end := Start(ctx, AgentNameInput, Prompt, User)
	cancel()
	end(context.Canceled)
	reporter := &recordingReporter{}
	tracker.Flush(ctx, reporter, context.Canceled)

	require.Len(t, reporter.events, 2)
	assert.NoError(t, reporter.ctxErr)
	assert.Equal(t, trace.SpanContextFromContext(ctx), trace.SpanContextFromContext(reporter.ctx))
	meta, ok := metadata.FromOutgoingContext(reporter.ctx)
	require.True(t, ok)
	assert.Equal(t, []string{traceparent}, meta.Get("traceparent"))
	assert.Equal(t, "canceled", reporter.events[0].Attributes["init.flow.outcome"])
	assert.Equal(t, "1", reporter.events[0].Attributes["init.prompt.count"])
	assert.Equal(t, "agent_name_input", reporter.events[0].Attributes["init.prompt.longest.id"])
}

func TestTrackerBoundsVolumeButKeepsCompletePromptSummary(t *testing.T) {
	ctx, tracker := NewContext(t.Context(), AgentInit, false)
	for range maxBufferedSteps + 5 {
		Start(ctx, AgentNameInput, Prompt, User)(nil)
	}
	reporter := &recordingReporter{}
	tracker.Flush(ctx, reporter, nil)
	require.Len(t, reporter.events, maxBufferedSteps+1)
	assert.Equal(t, "5", reporter.events[0].Attributes["init.step.dropped_count"])
	assert.Equal(t, strconv.Itoa(maxBufferedSteps+5), reporter.events[0].Attributes["init.prompt.count"])
	assert.Equal(t, strconv.Itoa(maxBufferedSteps+5), reporter.events[0].Attributes["init.step.count"])
}

func TestTrackerNestedActionsReuseContext(t *testing.T) {
	ctx, tracker := NewContext(t.Context(), ProjectDeploymentAdd, true)
	nestedCtx, nestedTracker := NewContext(ctx, ProjectAdd, false)
	assert.Same(t, tracker, nestedTracker)
	assert.Equal(t, ctx, nestedCtx)
	assert.Equal(t, ProjectDeploymentAdd, tracker.flow)
	assert.True(t, tracker.noPrompt)
}

func TestTrackerConcurrentCompletionUsesStartSequence(t *testing.T) {
	ctx, tracker := NewContext(t.Context(), AgentInit, false)
	first := Start(ctx, ManifestLoading, External, "")
	second := Start(ctx, FileGeneration, Execution, "")
	var wg sync.WaitGroup
	wg.Go(func() { second(nil) })
	wg.Go(func() { first(nil) })
	wg.Wait()
	reporter := &recordingReporter{}
	tracker.Flush(ctx, reporter, nil)
	require.Len(t, reporter.events, 3)
	assert.Equal(t, "manifest_loading", reporter.events[1].Attributes["init.step.id"])
	assert.Equal(t, "file_generation", reporter.events[2].Attributes["init.step.id"])
}

type blockingReporter struct {
	calls int
}

func (r *blockingReporter) Report(ctx context.Context, _ foundryTelemetry.Event) {
	r.calls++
	<-ctx.Done()
}

func TestTrackerReportingSharesOneDeadline(t *testing.T) {
	ctx, tracker := NewContext(t.Context(), AgentInit, false)
	for range 5 {
		Start(ctx, FileGeneration, Execution, "")(nil)
	}
	reporter := &blockingReporter{}
	started := time.Now()
	tracker.Flush(ctx, reporter, nil)
	assert.Equal(t, 1, reporter.calls)
	assert.Less(t, time.Since(started), 2*reportTimeout)
}

type failingClient struct{}

func (failingClient) ReportUsage(
	context.Context, *v1beta.ReportUsageRequest, ...grpc.CallOption,
) (*v1beta.ReportUsageResponse, error) {
	return nil, status.Error(codes.Unavailable, "https://user:secret@example.invalid?sig=credential")
}

func TestTrackerReportingFailureDoesNotChangeBusinessResult(t *testing.T) {
	ctx, tracker := NewContext(t.Context(), AgentInit, false)
	businessErr := errors.New("business error")
	err := MeasureError(ctx, ManifestValidation, Execution, Config, func() error { return businessErr })
	reporter := foundryTelemetry.NewReporter(failingClient{}, nil)
	tracker.Flush(ctx, reporter, err)
	assert.Same(t, businessErr, err)
	tracker.Flush(ctx, nil, err)
}

type fakePromptClient struct {
	azdext.PromptServiceClient
	response *azdext.PromptResponse
	err      error
	request  *azdext.PromptRequest
}

func (p *fakePromptClient) Prompt(
	_ context.Context, req *azdext.PromptRequest, _ ...grpc.CallOption,
) (*azdext.PromptResponse, error) {
	p.request = req
	return p.response, p.err
}

func (p *fakePromptClient) PromptAiModel(
	context.Context, *azdext.PromptAiModelRequest, ...grpc.CallOption,
) (*azdext.PromptAiModelResponse, error) {
	return &azdext.PromptAiModelResponse{}, p.err
}

func TestPromptTimingBoundariesAndPrivacy(t *testing.T) {
	for _, noPrompt := range []bool{false, true} {
		t.Run(fmt.Sprintf("noPrompt=%t", noPrompt), func(t *testing.T) {
			ctx, tracker := NewContext(t.Context(), AgentInit, noPrompt)
			request := &azdext.PromptRequest{Options: &azdext.PromptOptions{
				Message: "Enter secret prompt at C:\\private\\file",
			}}
			response := &azdext.PromptResponse{Value: "https://user:password@example.invalid?sig=secret"}
			client := &fakePromptClient{response: response}
			result, err := Prompts(client, EnvironmentValueInput).Prompt(ctx, request)
			require.NoError(t, err)
			assert.Same(t, response, result)
			assert.Same(t, request, client.request)
			_, err = Prompts(client, ModelSelection).PromptAiModel(ctx, &azdext.PromptAiModelRequest{})
			require.NoError(t, err)
			reporter := &recordingReporter{}
			tracker.Flush(ctx, reporter, nil)
			require.Len(t, reporter.events, 3)
			assert.Equal(t, "external", reporter.events[2].Attributes["init.step.category"])
			assert.Equal(t, "composite_prompt_rpc", reporter.events[2].Attributes["init.step.timing_scope"])
			if noPrompt {
				assert.Equal(t, "0", reporter.events[0].Attributes["init.prompt.count"])
				assert.Equal(t, "execution", reporter.events[1].Attributes["init.step.category"])
				assert.Equal(t, "prompt_resolution_rpc", reporter.events[1].Attributes["init.step.timing_scope"])
				assert.NotContains(t, reporter.events[1].Attributes, "init.step.input_source")
			} else {
				assert.Equal(t, "1", reporter.events[0].Attributes["init.prompt.count"])
				assert.Equal(t, "prompt", reporter.events[1].Attributes["init.step.category"])
				assert.Equal(t, "prompt_rpc", reporter.events[1].Attributes["init.step.timing_scope"])
			}
			for _, sensitive := range []string{"Enter secret", "private", "password", "sig=", "example.invalid"} {
				assert.NotContains(t, fmt.Sprint(reporter.events), sensitive)
			}
		})
	}
}

func TestMeasureWithoutTrackerPreservesBehavior(t *testing.T) {
	value, err := Measure(t.Context(), FileGeneration, Execution, "", func() (int, error) { return 7, nil })
	assert.Equal(t, 7, value)
	assert.NoError(t, err)
}
