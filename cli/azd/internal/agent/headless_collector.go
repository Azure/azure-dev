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
	case copilot.AssistantTurnStart:
		h.mu.Lock()
		h.messageReceived = false
		h.pendingIdle = false
		h.mu.Unlock()

	case copilot.AssistantMessage:
		h.mu.Lock()
		h.messageReceived = true
		wasPendingIdle := h.pendingIdle
		h.pendingIdle = false
		h.mu.Unlock()

		if wasPendingIdle {
			h.signalIdle()
		}

	case copilot.AssistantUsage:
		h.mu.Lock()
		if event.Data.InputTokens != nil {
			h.totalInputTokens += *event.Data.InputTokens
		}
		if event.Data.OutputTokens != nil {
			h.totalOutputTokens += *event.Data.OutputTokens
		}
		if event.Data.Cost != nil {
			h.billingRate = *event.Data.Cost
		}
		if event.Data.Duration != nil {
			h.totalDurationMS += *event.Data.Duration
		}
		if event.Data.Model != nil {
			h.lastModel = *event.Data.Model
		}
		h.mu.Unlock()

	case copilot.SessionUsageInfo, copilot.SessionShutdown:
		h.mu.Lock()
		if event.Data.TotalPremiumRequests != nil {
			h.premiumRequests = *event.Data.TotalPremiumRequests
		}
		h.mu.Unlock()

	case copilot.SessionIdle:
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

	case copilot.SessionTaskComplete:
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
