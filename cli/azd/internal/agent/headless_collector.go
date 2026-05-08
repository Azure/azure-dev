// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent

import (
	"context"
	"log"
	"sync"

	copilot "github.com/github/copilot-sdk/go"
)

// HeadlessCollector silently collects Copilot SDK session events without
// producing any console output. It tracks usage metrics and signals
// completion, making it suitable for gRPC/headless agent sessions.
type HeadlessCollector struct {
	mu sync.Mutex

	// Usage metrics — accumulated from assistant.usage events
	totalInputTokens  float64
	totalOutputTokens float64
	billingRate       float64
	totalDurationMS   float64
	premiumRequests   float64
	lastModel         string

	// Lifecycle
	messageReceived bool
	pendingIdle     bool
	idleCh          chan struct{}
}

// NewHeadlessCollector creates a new HeadlessCollector.
func NewHeadlessCollector() *HeadlessCollector {
	return &HeadlessCollector{
		idleCh: make(chan struct{}, 1),
	}
}

// HandleEvent processes a Copilot session event silently, collecting
// usage metrics and tracking completion state.
func (h *HeadlessCollector) HandleEvent(event copilot.SessionEvent) {
	switch event.Type {
	case copilot.SessionEventTypeAssistantTurnStart:
		h.mu.Lock()
		h.messageReceived = false
		h.pendingIdle = false
		h.mu.Unlock()

	case copilot.SessionEventTypeAssistantMessage:
		h.mu.Lock()
		h.messageReceived = true
		wasPendingIdle := h.pendingIdle
		h.pendingIdle = false
		h.mu.Unlock()

		if wasPendingIdle {
			h.signalIdle()
		}

	case copilot.SessionEventTypeAssistantUsage:
		if data, ok := event.Data.(*copilot.AssistantUsageData); ok {
			h.mu.Lock()
			if data.InputTokens != nil {
				h.totalInputTokens += *data.InputTokens
			}
			if data.OutputTokens != nil {
				h.totalOutputTokens += *data.OutputTokens
			}
			if data.Cost != nil {
				h.billingRate = *data.Cost
			}
			if data.Duration != nil {
				h.totalDurationMS += *data.Duration
			}
			if data.Model != "" {
				h.lastModel = data.Model
			}
			h.mu.Unlock()
		}

	case copilot.SessionEventTypeSessionShutdown:
		if data, ok := event.Data.(*copilot.SessionShutdownData); ok {
			h.mu.Lock()
			h.premiumRequests = data.TotalPremiumRequests
			h.mu.Unlock()
		}

	case copilot.SessionEventTypeSessionIdle:
		h.mu.Lock()
		hasMessage := h.messageReceived
		if !hasMessage {
			h.pendingIdle = true
		}
		h.mu.Unlock()

		log.Printf("[copilot-headless] session.idle (hasMessage=%v)", hasMessage)

		if hasMessage {
			h.signalIdle()
		}

	case copilot.SessionEventTypeSessionTaskComplete:
		log.Printf("[copilot-headless] %s received, signaling completion", event.Type)
		h.signalIdle()
	}
}

// WaitForIdle blocks until the session becomes idle or the context is cancelled.
func (h *HeadlessCollector) WaitForIdle(ctx context.Context) error {
	select {
	case <-h.idleCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// GetUsageMetrics returns the accumulated usage metrics.
func (h *HeadlessCollector) GetUsageMetrics() UsageMetrics {
	h.mu.Lock()
	defer h.mu.Unlock()

	return UsageMetrics{
		Model:           h.lastModel,
		InputTokens:     h.totalInputTokens,
		OutputTokens:    h.totalOutputTokens,
		BillingRate:     h.billingRate,
		PremiumRequests: h.premiumRequests,
		DurationMS:      h.totalDurationMS,
	}
}

func (h *HeadlessCollector) signalIdle() {
	select {
	case h.idleCh <- struct{}{}:
	default:
	}
}
