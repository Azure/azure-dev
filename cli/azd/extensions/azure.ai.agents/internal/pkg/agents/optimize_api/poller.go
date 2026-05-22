// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// poller.go provides a polling loop for optimization jobs that calls
// a progress callback on each tick until the job reaches a terminal state.
package optimize_api

import (
	"context"
	"fmt"
	"log"
	"time"

	"azureaiagent/internal/pkg/agents"
)

// Poller polls an optimization job until it reaches a terminal state.
type Poller struct {
	Client      *OptimizeClient
	OperationID string
	Interval    time.Duration
	MaxAttempts int // 0 means no limit
	OnProgress  func(*OptimizeJobStatus)
}

// PollUntilDone polls GetOptimizeStatus at the configured interval until the
// job reaches a terminal state (completed, failed, cancelled), the context
// is cancelled, or MaxAttempts is reached. Transient errors (5xx, 429,
// connection reset) are retried up to maxConsecutiveTransient times before
// the poller gives up.
func (p *Poller) PollUntilDone(ctx context.Context) (*OptimizeJobStatus, error) {
	const maxConsecutiveTransient = 5

	interval := p.Interval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	attempts := 0
	consecutiveTransient := 0
	for {
		status, err := p.Client.GetOptimizeStatus(ctx, p.OperationID)
		if err != nil {
			if agents.IsTransientError(err) {
				consecutiveTransient++
				if consecutiveTransient > maxConsecutiveTransient {
					return nil, fmt.Errorf(
						"polling aborted after %d consecutive transient errors, last: %w",
						consecutiveTransient, err)
				}
				log.Printf("[poller] transient error polling %s (%d/%d), will retry: %v",
					p.OperationID, consecutiveTransient, maxConsecutiveTransient, err)
				goto wait
			}
			return nil, fmt.Errorf("failed to get optimization status: %w", err)
		}

		consecutiveTransient = 0 // reset on success

		if p.OnProgress != nil {
			p.OnProgress(status)
		}

		if IsTerminal(status.Status) {
			return status, nil
		}

	wait:
		attempts++
		if p.MaxAttempts > 0 && attempts >= p.MaxAttempts {
			return nil, fmt.Errorf("polling timed out after %d attempts", attempts)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			// continue polling
		}
	}
}
