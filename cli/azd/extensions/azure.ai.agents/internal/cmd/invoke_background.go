// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"
	"time"
)

const (
	// Periodic cursor persistence bounds duplicate replay after abrupt termination without a
	// UserConfig read-modify-write for every SSE event. Identity, lifecycle, and terminal events
	// persist immediately, and normal exits flush pending state.
	backgroundCursorPersistInterval   = 3 * time.Second
	backgroundCursorPersistEventCount = 64
)

func isTerminalResponseStatus(status string) bool {
	switch status {
	case "completed", "failed", "incomplete", "cancelled":
		return true
	default:
		return false
	}
}

func isResponseLifecycleEvent(eventType string) bool {
	switch eventType {
	case "response.created", "response.queued", "response.in_progress",
		"response.completed", "response.failed", "response.incomplete", "response.cancelled":
		return true
	default:
		return false
	}
}

type backgroundProgressPersister struct {
	store              responseStateStore
	agentKey           string
	sessionID          string
	conversationID     string
	writer             io.Writer
	now                func() time.Time
	latest             savedBackgroundResponse
	persistedResponse  string
	persistedStatus    string
	lastPersistedAt    time.Time
	eventsSincePersist int
	printedResponseID  bool
	dirty              bool
}

func newBackgroundProgressPersister(
	store responseStateStore,
	agentKey string,
	sessionID string,
	conversationID string,
	writer io.Writer,
) *backgroundProgressPersister {
	return &backgroundProgressPersister{
		store:          store,
		agentKey:       agentKey,
		sessionID:      sessionID,
		conversationID: conversationID,
		writer:         writer,
		now:            time.Now,
	}
}

func (p *backgroundProgressPersister) Apply(ctx context.Context, progress responsesStreamProgress) error {
	if progress.ResponseID == "" {
		return nil
	}

	p.latest = savedBackgroundResponse{
		ResponseID:         progress.ResponseID,
		LastSequenceNumber: progress.Cursor,
		Status:             progress.Status,
		SessionID:          p.sessionID,
		ConversationID:     p.conversationID,
	}
	p.dirty = true
	p.eventsSincePersist++
	now := p.now()
	shouldPersist := p.persistedResponse == "" ||
		isResponseLifecycleEvent(progress.EventType) ||
		progress.Status != p.persistedStatus ||
		progress.Terminal ||
		p.eventsSincePersist >= backgroundCursorPersistEventCount ||
		(!p.lastPersistedAt.IsZero() && now.Sub(p.lastPersistedAt) >= backgroundCursorPersistInterval)
	if !shouldPersist {
		return nil
	}
	return p.persist(ctx, now)
}

func (p *backgroundProgressPersister) Flush(ctx context.Context) error {
	if !p.dirty {
		return nil
	}
	return p.persist(ctx, p.now())
}

func (p *backgroundProgressPersister) persist(ctx context.Context, now time.Time) error {
	if err := p.store.Save(ctx, p.agentKey, p.latest); err != nil {
		return err
	}
	if !p.printedResponseID {
		if _, err := fmt.Fprintf(p.writer, "Response:     %s\n", p.latest.ResponseID); err != nil {
			return err
		}
		p.printedResponseID = true
	}
	p.persistedResponse = p.latest.ResponseID
	p.persistedStatus = p.latest.Status
	p.lastPersistedAt = now
	p.eventsSincePersist = 0
	p.dirty = false
	return nil
}
