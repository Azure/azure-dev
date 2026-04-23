// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent

import (
	"testing"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/stretchr/testify/require"
)

func TestHeadlessCollector_HandleEvent_Usage(t *testing.T) {
	t.Parallel()
	collector := NewHeadlessCollector()

	inputTokens := float64(100)
	outputTokens := float64(50)
	cost := float64(1.5)
	duration := float64(1000)
	model := "gpt-4o"

	collector.HandleEvent(copilot.SessionEvent{
		Type: copilot.AssistantUsage,
		Data: copilot.Data{
			InputTokens:  &inputTokens,
			OutputTokens: &outputTokens,
			Cost:         &cost,
			Duration:     &duration,
			Model:        &model,
		},
	})

	usage := collector.GetUsageMetrics()
	require.Equal(t, float64(100), usage.InputTokens)
	require.Equal(t, float64(50), usage.OutputTokens)
	require.Equal(t, float64(1.5), usage.BillingRate)
	require.Equal(t, float64(1000), usage.DurationMS)
	require.Equal(t, "gpt-4o", usage.Model)
}

func TestHeadlessCollector_HandleEvent_AccumulatesUsage(t *testing.T) {
	t.Parallel()
	collector := NewHeadlessCollector()

	tokens1 := float64(100)
	tokens2 := float64(200)

	collector.HandleEvent(copilot.SessionEvent{
		Type: copilot.AssistantUsage,
		Data: copilot.Data{InputTokens: &tokens1},
	})
	collector.HandleEvent(copilot.SessionEvent{
		Type: copilot.AssistantUsage,
		Data: copilot.Data{InputTokens: &tokens2},
	})

	usage := collector.GetUsageMetrics()
	require.Equal(t, float64(300), usage.InputTokens)
}

func TestHeadlessCollector_WaitForIdle_WithMessage(t *testing.T) {
	t.Parallel()
	collector := NewHeadlessCollector()

	// Simulate turn start → message → idle
	collector.HandleEvent(copilot.SessionEvent{Type: copilot.AssistantTurnStart})
	collector.HandleEvent(copilot.SessionEvent{Type: copilot.AssistantMessage, Data: copilot.Data{}})
	collector.HandleEvent(copilot.SessionEvent{Type: copilot.SessionIdle})

	ctx := t.Context()
	err := collector.WaitForIdle(ctx)
	require.NoError(t, err)
}

func TestHeadlessCollector_WaitForIdle_TaskComplete(t *testing.T) {
	t.Parallel()
	collector := NewHeadlessCollector()

	collector.HandleEvent(copilot.SessionEvent{Type: copilot.SessionTaskComplete})

	ctx := t.Context()
	err := collector.WaitForIdle(ctx)
	require.NoError(t, err)
}

func TestHeadlessCollector_WaitForIdle_DeferredIdle(t *testing.T) {
	t.Parallel()
	collector := NewHeadlessCollector()

	// Idle arrives before message → should be deferred
	collector.HandleEvent(copilot.SessionEvent{Type: copilot.AssistantTurnStart})
	collector.HandleEvent(copilot.SessionEvent{Type: copilot.SessionIdle})

	// Message arrives → should flush deferred idle
	collector.HandleEvent(copilot.SessionEvent{Type: copilot.AssistantMessage, Data: copilot.Data{}})

	ctx := t.Context()
	err := collector.WaitForIdle(ctx)
	require.NoError(t, err)
}

func TestHeadlessCollector_PremiumRequests(t *testing.T) {
	t.Parallel()
	collector := NewHeadlessCollector()

	premium := float64(5)
	collector.HandleEvent(copilot.SessionEvent{
		Type: copilot.SessionUsageInfo,
		Data: copilot.Data{TotalPremiumRequests: &premium},
	})

	usage := collector.GetUsageMetrics()
	require.Equal(t, float64(5), usage.PremiumRequests)
}
