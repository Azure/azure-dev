// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package inittelemetry

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
)

type recordingReporter struct {
	events []foundryTelemetry.Event
	ctxErr error
	parent string
	block  bool
}

func (r *recordingReporter) Report(ctx context.Context, event foundryTelemetry.Event) {
	r.ctxErr = ctx.Err()
	meta, _ := metadata.FromOutgoingContext(ctx)
	r.parent = fmt.Sprint(meta.Get("traceparent"))
	r.events = append(r.events, event)
	if r.block {
		<-ctx.Done()
	}
}

func TestStepTimingsIncludeAllWorkAndRepeatedAttempts(t *testing.T) {
	ctx, tracker := NewContext(t.Context())
	now := time.Now()
	tracker.now = func() time.Time { return now }
	first := Start(ctx, ModelSelection)
	now = now.Add(2 * time.Second) // Loading and interaction belong to the same step.
	second := Start(ctx, ModelSelection)
	now = now.Add(1500 * time.Microsecond)
	second()
	now = now.Add(3 * time.Millisecond)
	first()
	first() // Completion is idempotent.
	recorder := &recordingReporter{}
	tracker.Flush(ctx, recorder)
	require.Len(t, recorder.events, 2)
	assert.Equal(t, foundryTelemetry.Event{
		Name: "agent.init.step",
		Attributes: map[string]string{
			"agent.init.step.id": "model_selection", "agent.init.step.sequence": "1", "agent.init.step.duration_ms": "2004.500",
		},
	}, recorder.events[0])
	assert.Equal(t, foundryTelemetry.Event{
		Name: "agent.init.step",
		Attributes: map[string]string{
			"agent.init.step.id": "model_selection", "agent.init.step.sequence": "2", "agent.init.step.duration_ms": "1.500",
		},
	}, recorder.events[1])
}

func TestCanceledInitStillReportsWithTraceMetadata(t *testing.T) {
	const parent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	ctx := metadata.NewOutgoingContext(t.Context(), metadata.Pairs("traceparent", parent))
	ctx, cancel := context.WithCancel(ctx)
	ctx, tracker := NewContext(ctx)
	run := func() error {
		defer Start(ctx, ModelSelection)()
		cancel()
		return ctx.Err()
	}
	err := run()
	require.ErrorIs(t, err, context.Canceled)
	recorder := &recordingReporter{}
	tracker.Flush(ctx, recorder)
	require.Len(t, recorder.events, 1)
	assert.NoError(t, recorder.ctxErr)
	assert.Equal(t, "["+parent+"]", recorder.parent)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestReportingIsBufferedAndHasOneBatchDeadline(t *testing.T) {
	ctx, tracker := NewContext(t.Context())
	for range maxSteps + 1 {
		Start(ctx, TemplateStaging)()
	}
	require.Len(t, tracker.steps, maxSteps)
	assert.True(t, tracker.limited)
	recorder := &recordingReporter{block: true}
	started := time.Now()
	tracker.Flush(ctx, recorder)
	assert.Less(t, time.Since(started), 2*time.Second)
	require.Len(t, recorder.events, 1)
}

func TestUnfinishedStepsAreNotReported(t *testing.T) {
	ctx, tracker := NewContext(t.Context())
	Start(ctx, TemplateLoading)
	Start(ctx, AgentNameSelection)()
	recorder := &recordingReporter{}
	tracker.Flush(ctx, recorder)
	require.Len(t, recorder.events, 1)
	assert.Equal(t, "2", recorder.events[0].Attributes["agent.init.step.sequence"])
	Start(t.Context(), ModelSelection)() // Other commands do not enable timing.
}

func TestConcurrentCompletionKeepsStartOrder(t *testing.T) {
	ctx, tracker := NewContext(t.Context())
	first := Start(ctx, TemplateLoading)
	second := Start(ctx, TemplateStaging)
	var group sync.WaitGroup
	group.Go(second)
	group.Go(first)
	group.Wait()
	recorder := &recordingReporter{}
	tracker.Flush(ctx, recorder)
	require.Len(t, recorder.events, 2)
	assert.Equal(t, "template_loading", recorder.events[0].Attributes["agent.init.step.id"])
	assert.Equal(t, "template_staging", recorder.events[1].Attributes["agent.init.step.id"])
}
