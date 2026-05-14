// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package optimize_api

import (
	"context"
	"fmt"
	"time"
)

// Poller polls an optimization job until it reaches a terminal state.
type Poller struct {
	Client      *OptimizeClient
	OperationID string
	Interval    time.Duration
	OnProgress  func(*OptimizeJobStatus)
}

// PollUntilDone polls GetOptimizeStatus at the configured interval until the
// job reaches a terminal state (completed, failed, cancelled) or the context
// is cancelled.
func (p *Poller) PollUntilDone(ctx context.Context) (*OptimizeJobStatus, error) {
	ticker := time.NewTicker(p.Interval)
	defer ticker.Stop()

	for {
		status, err := p.Client.GetOptimizeStatus(ctx, p.OperationID)
		if err != nil {
			return nil, fmt.Errorf("failed to get optimization status: %w", err)
		}

		if p.OnProgress != nil {
			p.OnProgress(status)
		}

		if IsTerminal(status.Status) {
			return status, nil
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			// continue polling
		}
	}
}
