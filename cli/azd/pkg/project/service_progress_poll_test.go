// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/stretchr/testify/require"
)

func TestStartPollingProgress_EmitsMessages(t *testing.T) {
	t.Parallel()
	p := async.NewProgress[ServiceProgress]()

	stop := startPollingProgress(p, "Waiting", 50*time.Millisecond)

	// Collect at least 2 messages.
	var msgs []string
	for range 2 {
		sp := <-p.Progress()
		msgs = append(msgs, sp.Message)
	}

	stop()
	p.Done()

	require.Len(t, msgs, 2)
	// First tick should include elapsed time.
	require.Contains(t, msgs[0], "Waiting")
	require.Contains(t, msgs[0], "s)")
}

func TestStartPollingProgress_StopEndsGoroutine(t *testing.T) {
	t.Parallel()
	p := async.NewProgress[ServiceProgress]()

	stop := startPollingProgress(p, "Deploy", 10*time.Millisecond)

	// Stop immediately.
	stop()
	p.Done()

	// Drain any already-sent messages.
	for range p.Progress() {
	}
	// If goroutine leaked, the test would hang. Reaching here = pass.
}
