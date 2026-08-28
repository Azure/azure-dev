// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

const (
	// Periodic cursor persistence bounds duplicate replay after abrupt termination without a
	// UserConfig read-modify-write for every SSE event. Identity, lifecycle, and terminal events
	// persist immediately, and normal exits flush pending state.
	backgroundCursorPersistInterval   = 3 * time.Second
	backgroundCursorPersistTimeout    = 30 * time.Second
	backgroundCursorPersistEventCount = 64
)

var errBackgroundProgressPersisterClosed = errors.New("background progress persister is closed")

type backgroundPersistTimer interface {
	Stop() bool
}

type backgroundPersistTimerFactory func(time.Duration, func()) backgroundPersistTimer

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
	mu                 sync.Mutex
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
	timerFactory       backgroundPersistTimerFactory
	timerContext       func(context.Context) (context.Context, context.CancelFunc)
	timer              backgroundPersistTimer
	timerGeneration    uint64
	pendingTimerErr    error
	closed             bool
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
		timerFactory: func(delay time.Duration, callback func()) backgroundPersistTimer {
			return time.AfterFunc(delay, callback)
		},
		timerContext: func(ctx context.Context) (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.WithoutCancel(ctx), backgroundCursorPersistTimeout)
		},
	}
}

func (p *backgroundProgressPersister) Apply(ctx context.Context, progress responsesStreamProgress) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return errBackgroundProgressPersisterClosed
	}
	if err := p.takeTimerErrorLocked(); err != nil {
		return err
	}
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
		p.scheduleTimerLocked(ctx, now)
		return nil
	}
	return p.persistLocked(ctx, now)
}

func (p *backgroundProgressPersister) Flush(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return errBackgroundProgressPersisterClosed
	}
	p.stopTimerLocked()
	if err := p.takeTimerErrorLocked(); err != nil {
		return err
	}
	if !p.dirty {
		return nil
	}
	return p.persistLocked(ctx, p.now())
}

// Close stops asynchronous persistence and reports any timer error not already
// observed by Apply or Flush. It intentionally does not flush dirty state when
// command cancellation caused the stream to exit.
func (p *backgroundProgressPersister) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.stopTimerLocked()
	p.closed = true
	return p.takeTimerErrorLocked()
}

func (p *backgroundProgressPersister) scheduleTimerLocked(ctx context.Context, now time.Time) {
	if p.timer != nil || !p.dirty {
		return
	}

	delay := backgroundCursorPersistInterval
	if !p.lastPersistedAt.IsZero() {
		delay = max(backgroundCursorPersistInterval-now.Sub(p.lastPersistedAt), 0)
	}
	p.timerGeneration++
	generation := p.timerGeneration
	p.timer = p.timerFactory(delay, func() {
		p.persistFromTimer(ctx, generation)
	})
}

func (p *backgroundProgressPersister) persistFromTimer(ctx context.Context, generation uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed || generation != p.timerGeneration {
		return
	}
	p.timer = nil
	if !p.dirty || p.pendingTimerErr != nil {
		return
	}
	persistCtx, cancel := p.timerContext(ctx)
	defer cancel()
	if err := p.persistLocked(persistCtx, p.now()); err != nil {
		p.pendingTimerErr = err
	}
}

func (p *backgroundProgressPersister) stopTimerLocked() {
	if p.timer == nil {
		return
	}
	p.timerGeneration++
	p.timer.Stop()
	p.timer = nil
}

func (p *backgroundProgressPersister) takeTimerErrorLocked() error {
	err := p.pendingTimerErr
	p.pendingTimerErr = nil
	return err
}

func (p *backgroundProgressPersister) persistLocked(ctx context.Context, now time.Time) error {
	p.stopTimerLocked()
	if err := p.store.Save(ctx, p.agentKey, p.latest); err != nil {
		if !p.printedResponseID {
			_, _ = fmt.Fprintf(
				p.writer,
				"Response:     %s\nWARNING: This background Response was accepted, but its state was not saved. "+
					"Save the Response ID before retrying.\n",
				p.latest.ResponseID,
			)
			p.printedResponseID = true
		}
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
