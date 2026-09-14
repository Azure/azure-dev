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

	inputTokens := int64(100)
	outputTokens := int64(50)
	cost := float64(1.5)
	duration := int64(1000)

	collector.HandleEvent(copilot.SessionEvent{
		Data: &copilot.AssistantUsageData{
			InputTokens:  &inputTokens,
			OutputTokens: &outputTokens,
			Cost:         &cost,
			Duration:     &duration,
			Model:        "gpt-4o",
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

	tokens1 := int64(100)
	tokens2 := int64(200)

	collector.HandleEvent(copilot.SessionEvent{
		Data: &copilot.AssistantUsageData{InputTokens: &tokens1},
	})
	collector.HandleEvent(copilot.SessionEvent{
		Data: &copilot.AssistantUsageData{InputTokens: &tokens2},
	})

	usage := collector.GetUsageMetrics()
	require.Equal(t, float64(300), usage.InputTokens)
}

func TestHeadlessCollector_WaitForIdle_WithMessage(t *testing.T) {
	t.Parallel()
	collector := NewHeadlessCollector()

	// Simulate turn start → message → idle
	collector.HandleEvent(copilot.SessionEvent{Data: &copilot.AssistantTurnStartData{}})
	collector.HandleEvent(copilot.SessionEvent{
		Data: &copilot.AssistantMessageData{},
	})
	collector.HandleEvent(copilot.SessionEvent{Data: &copilot.SessionIdleData{}})

	ctx := t.Context()
	err := collector.WaitForIdle(ctx)
	require.NoError(t, err)
}

func TestHeadlessCollector_WaitForIdle_TaskComplete(t *testing.T) {
	t.Parallel()
	collector := NewHeadlessCollector()

	collector.HandleEvent(copilot.SessionEvent{Data: &copilot.SessionTaskCompleteData{}})

	ctx := t.Context()
	err := collector.WaitForIdle(ctx)
	require.NoError(t, err)
}

func TestHeadlessCollector_WaitForIdle_DeferredIdle(t *testing.T) {
	t.Parallel()
	collector := NewHeadlessCollector()

	// Idle arrives before message → should be deferred
	collector.HandleEvent(copilot.SessionEvent{Data: &copilot.AssistantTurnStartData{}})
	collector.HandleEvent(copilot.SessionEvent{Data: &copilot.SessionIdleData{}})

	// Message arrives → should flush deferred idle
	collector.HandleEvent(copilot.SessionEvent{
		Data: &copilot.AssistantMessageData{},
	})

	ctx := t.Context()
	err := collector.WaitForIdle(ctx)
	require.NoError(t, err)
}

func TestHeadlessCollector_PremiumRequests(t *testing.T) {
	t.Parallel()
	collector := NewHeadlessCollector()

	collector.HandleEvent(copilot.SessionEvent{
		Data: &copilot.SessionShutdownData{TotalPremiumRequests: new(5.0)},
	})

	usage := collector.GetUsageMetrics()
	require.Equal(t, float64(5), usage.PremiumRequests)
}
