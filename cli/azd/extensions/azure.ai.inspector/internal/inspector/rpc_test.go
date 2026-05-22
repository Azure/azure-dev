// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package inspector

import (
	"context"
	"io"
	"log"
	"testing"
)

func TestRegisterStreamAfterCleanupCancelsWithoutPanic(t *testing.T) {
	rootCtx, rootCancel := context.WithCancel(t.Context())
	sess := &rpcSession{
		logger:     log.New(io.Discard, "", 0),
		streams:    make(map[string]context.CancelFunc),
		rootCtx:    rootCtx,
		rootCancel: rootCancel,
	}

	sess.cleanup()

	streamCtx, streamCancel := context.WithCancel(t.Context())
	if sess.registerStream("late", streamCancel) {
		t.Fatal("registerStream should reject streams after cleanup")
	}

	select {
	case <-streamCtx.Done():
	default:
		t.Fatal("registerStream should cancel rejected streams")
	}
}

func TestHandleMessageSafelyRecoversPanic(t *testing.T) {
	sess := &rpcSession{
		cfg:        Config{AgentPort: 8088},
		logger:     log.New(io.Discard, "", 0),
		streams:    make(map[string]context.CancelFunc),
		rootCtx:    t.Context(),
		rootCancel: func() {},
	}

	// setViewReady writes to the websocket. A nil conn would panic without the
	// recover wrapper around per-message goroutines.
	sess.handleMessageSafely(rpcMessage{Method: "setViewReady"})
}
