// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore phasetelemetry BYOAKS

package phasetelemetry

import (
	"context"
	"errors"
	"testing"
	"time"

	foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
	"github.com/stretchr/testify/require"
)

type recordingReporter struct {
	events []foundryTelemetry.Event
}

func (r *recordingReporter) Report(ctx context.Context, event foundryTelemetry.Event) {
	if ctx.Err() != nil {
		panic("business cancellation must not cancel reporting")
	}
	r.events = append(r.events, event)
}

func TestBYOAKSMarksOnlyActiveProviderProvisionCompletion(t *testing.T) {
	t.Run("marked on provider provision", func(t *testing.T) {
		reporter := &recordingReporter{}
		ctx, phase := Begin(t.Context(), Provision, Provider, reporter)
		now := phase.started
		phase.now = func() time.Time { return now }

		endStep := Start(ctx, TemplateResolution)
		BYOAKS(ctx)
		now = now.Add(5 * time.Millisecond)
		endStep()
		phase.End(ctx, nil)

		require.Len(t, reporter.events, 2)
		completed := reporter.events[0]
		require.Equal(t, "agent.phase.completed", completed.Name)
		require.Equal(t, "provision", completed.Attributes["agent.phase.name"])
		require.Equal(t, "provider", completed.Attributes["agent.phase.scope"])
		require.Equal(t, "success", completed.Attributes["agent.phase.outcome"])
		require.Equal(t, "true", completed.Attributes["agent.hosting.byo_aks"])

		stepEvent := reporter.events[1]
		require.Equal(t, "agent.phase.step", stepEvent.Name)
		_, stepMarked := stepEvent.Attributes["agent.hosting.byo_aks"]
		require.False(t, stepMarked)
	})

	t.Run("omitted when unmarked", func(t *testing.T) {
		reporter := &recordingReporter{}
		ctx, phase := Begin(t.Context(), Provision, Provider, reporter)
		phase.End(ctx, nil)

		require.Len(t, reporter.events, 1)
		_, marked := reporter.events[0].Attributes["agent.hosting.byo_aks"]
		require.False(t, marked)
	})

	t.Run("omitted on other phases and scopes", func(t *testing.T) {
		for _, test := range []struct {
			label string
			name  Name
			scope Scope
		}{
			{"project_authoring command", ProjectAuthoring, Command},
			{"model_deployment_authoring command", ModelDeploymentAuthoring, Command},
			{"provision command", Provision, Command},
			{"deploy provider", Deploy, Provider},
			{"package provider", Package, Provider},
			{"publish provider", Publish, Provider},
		} {
			t.Run(test.label, func(t *testing.T) {
				reporter := &recordingReporter{}
				ctx, phase := Begin(t.Context(), test.name, test.scope, reporter)
				BYOAKS(ctx)
				phase.BYOAKS()
				phase.End(ctx, errors.New("boom"))

				require.Len(t, reporter.events, 1)
				_, marked := reporter.events[0].Attributes["agent.hosting.byo_aks"]
				require.False(t, marked)
			})
		}
	})

	t.Run("safe without timing or after end", func(t *testing.T) {
		BYOAKS(t.Context())
		var nilTiming *Timing
		nilTiming.BYOAKS()

		reporter := &recordingReporter{}
		ctx, phase := Begin(t.Context(), Provision, Provider, reporter)
		phase.End(ctx, nil)
		BYOAKS(ctx)
		phase.End(ctx, nil)

		require.Len(t, reporter.events, 1)
		_, marked := reporter.events[0].Attributes["agent.hosting.byo_aks"]
		require.False(t, marked)
	})
}
