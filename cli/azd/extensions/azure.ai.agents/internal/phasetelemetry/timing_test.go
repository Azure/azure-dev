// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package phasetelemetry

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type recordingReporter struct {
	events []foundryTelemetry.Event
	md     metadata.MD
	block  bool
}

func (r *recordingReporter) Report(ctx context.Context, event foundryTelemetry.Event) {
	if ctx.Err() != nil {
		panic("business cancellation must not cancel reporting")
	}
	r.md, _ = metadata.FromOutgoingContext(ctx)
	r.events = append(r.events, event)
	if r.block {
		<-ctx.Done()
	}
}

func TestPhaseAndStepsUseActualTimesAndOneAttempt(t *testing.T) {
	reporter := &recordingReporter{}
	ctx, phase := Begin(t.Context(), Deploy, Provider, reporter)
	now := phase.started
	phase.now = func() time.Time { return now }
	now = now.Add(2 * time.Millisecond)
	first := Start(ctx, AgentCreateOrUpdate)
	now = now.Add(3 * time.Millisecond)
	second := Start(ctx, AgentCreateOrUpdate)
	now = now.Add(4 * time.Millisecond)
	second()
	first()
	first()
	now = now.Add(1 * time.Millisecond)
	phase.End(ctx, nil)
	phase.End(ctx, errors.New("ignored duplicate end"))
	require.Len(t, reporter.events, 3)
	complete := reporter.events[0]
	require.Equal(t, "agent.phase.completed", complete.Name)
	require.Equal(t, "deploy", complete.Attributes["agent.phase.name"])
	require.Equal(t, "provider", complete.Attributes["agent.phase.scope"])
	require.Equal(t, "success", complete.Attributes["agent.phase.outcome"])
	require.Equal(t, "10.000", complete.Attributes["agent.phase.duration_ms"])
	started, err := time.Parse(time.RFC3339Nano, complete.Attributes["agent.phase.start_time"])
	require.NoError(t, err)
	ended, err := time.Parse(time.RFC3339Nano, complete.Attributes["agent.phase.end_time"])
	require.NoError(t, err)
	require.Equal(t, 10*time.Millisecond, ended.Sub(started))
	for i, event := range reporter.events[1:] {
		require.Equal(t, "agent.phase.step", event.Name)
		require.Equal(t, phase.attempt, event.Attributes["agent.phase.attempt_id"])
		require.Equal(t, "agent_create_or_update", event.Attributes["agent.phase.step.id"])
		require.Equal(t, strconv.Itoa(i+1), event.Attributes["agent.phase.step.sequence"])
	}
	require.Equal(t, "2.000", reporter.events[1].Attributes["agent.phase.step.start_offset_ms"])
	require.Equal(t, "7.000", reporter.events[1].Attributes["agent.phase.step.duration_ms"])
	require.Equal(t, "5.000", reporter.events[2].Attributes["agent.phase.step.start_offset_ms"])
	require.Equal(t, "4.000", reporter.events[2].Attributes["agent.phase.step.duration_ms"])
}

func TestOutcomesAndCancellationPreserveBusinessErrorsAndTrace(t *testing.T) {
	for _, test := range []struct {
		err     error
		outcome string
	}{
		{nil, "success"}, {errors.New("secret contents"), "error"},
		{fmt.Errorf("private URL: %w", context.Canceled), "canceled"},
		{context.DeadlineExceeded, "timeout"},
		{status.Error(codes.Canceled, "private name"), "canceled"},
		{status.Error(codes.DeadlineExceeded, "private name"), "timeout"},
		{&azdext.LocalError{Code: "cancelled", Category: azdext.LocalErrorCategoryUser}, "canceled"},
	} {
		t.Run(fmt.Sprint(test.err), func(t *testing.T) {
			const trace = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
			ctx := metadata.NewOutgoingContext(t.Context(), metadata.Pairs("traceparent", trace))
			ctx, cancel := context.WithCancel(ctx)
			reporter := &recordingReporter{}
			ctx, phase := Begin(ctx, Provision, Provider, reporter)
			run := func() error { defer Start(ctx, ResourceDeploymentWait)(); cancel(); return test.err }
			err := run()
			phase.End(ctx, err)
			require.Equal(t, test.err, err)
			require.Len(t, reporter.events, 2)
			require.Equal(t, test.outcome, reporter.events[0].Attributes["agent.phase.outcome"])
			require.Equal(t, []string{trace}, reporter.md.Get("traceparent"))
			require.NotContains(t, fmt.Sprint(reporter.events), "secret contents")
			require.NotContains(t, fmt.Sprint(reporter.events), "private name")
			require.NotContains(t, fmt.Sprint(reporter.events), "private URL")
		})
	}
}

func TestConcurrentAttemptsAreIsolatedAndBounded(t *testing.T) {
	reporter := &recordingReporter{}
	ctx, phase := Begin(t.Context(), ProjectAuthoring, Command, reporter)
	_, other := Begin(t.Context(), ProjectAuthoring, Command, nil)
	require.NotEqual(t, phase.attempt, other.attempt)
	var group sync.WaitGroup
	for range maxSteps + 4 {
		group.Go(func() { Start(ctx, ConfigurationSave)() })
	}
	group.Wait()
	phase.End(ctx, nil)
	require.Len(t, reporter.events, maxSteps+1)
	require.Equal(t, "4", reporter.events[0].Attributes["agent.phase.step.dropped_count"])
	Start(t.Context(), ConfigurationSave)()
	other.End(ctx, nil)
}

func TestReportingHasOneBudgetAndCompletionPriority(t *testing.T) {
	reporter := &recordingReporter{block: true}
	ctx, phase := Begin(t.Context(), Deploy, Provider, reporter)
	for range 3 {
		Start(ctx, AgentCreateOrUpdate)()
	}
	started := time.Now()
	phase.End(ctx, nil)
	require.Less(t, time.Since(started), 2*time.Second)
	require.Len(t, reporter.events, 1)
	require.Equal(t, "agent.phase.completed", reporter.events[0].Name)
}

func TestObservedPollingTimeoutDoesNotReplaceTheBusinessError(t *testing.T) {
	for _, test := range []struct {
		err     error
		outcome string
	}{
		{errors.New("untyped polling deadline"), "timeout"},
		{nil, "success"},
		{context.Canceled, "canceled"},
	} {
		reporter := &recordingReporter{}
		ctx, phase := Begin(t.Context(), Deploy, Provider, reporter)
		Timeout(ctx)
		phase.End(ctx, test.err)
		require.Len(t, reporter.events, 1)
		require.Equal(t, test.outcome, reporter.events[0].Attributes["agent.phase.outcome"])
	}
}
