// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
)

// startPollingProgress starts a background goroutine that emits periodic progress messages
// during long-running operations like ARM polling or kubectl roll-outs.
// It returns a stop function that cancels pending updates and waits for the goroutine to exit.
// Call stop when the operation completes, before closing progress or reporting the next operation's progress.
func startPollingProgress(
	progress *async.Progress[ServiceProgress],
	message string,
	interval time.Duration,
) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		elapsed := 0
		for {
			select {
			case <-ticker.C:
				elapsed += int(interval.Seconds())
				if err := progress.SetProgressWithContext(
					ctx, NewServiceProgress(fmt.Sprintf("%s (%ds)", message, elapsed)),
				); err != nil {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	return func() {
		cancel()
		<-stopped
	}
}
