// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// poller.go provides a polling loop for optimization jobs that calls
// a progress callback on each tick until the job reaches a terminal state.
package optimize_api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
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
// is cancelled, or MaxAttempts is reached.
func (p *Poller) PollUntilDone(ctx context.Context) (*OptimizeJobStatus, error) {
	interval := p.Interval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	attempts := 0
	for {
		status, err := p.Client.GetOptimizeStatus(ctx, p.OperationID)
		if err != nil {
			if isTransientError(err) {
				log.Printf("[poller] transient error polling %s, will retry: %v", p.OperationID, err)
				goto wait
			}
			return nil, fmt.Errorf("failed to get optimization status: %w", err)
		}

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

// isTransientError checks whether an error represents a transient HTTP failure
// (429 Too Many Requests or 5xx Server Error) that is safe to retry.
func isTransientError(err error) bool {
	if respErr, ok := errors.AsType[*azcore.ResponseError](err); ok {
		return respErr.StatusCode == 429 || respErr.StatusCode >= 500
	}
	return false
}
