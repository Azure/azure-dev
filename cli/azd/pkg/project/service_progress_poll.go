// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
)

// startPollingProgress starts a background goroutine that emits periodic progress messages
// during long-running operations like ARM polling or kubectl rollouts.
// It returns a stop function that must be called when the operation completes.
func startPollingProgress(
	progress *async.Progress[ServiceProgress],
	message string,
	interval time.Duration,
) (stop func()) {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		elapsed := 0
		for {
			select {
			case <-ticker.C:
				elapsed += int(interval.Seconds())
				progress.SetProgress(NewServiceProgress(fmt.Sprintf("%s (%ds)", message, elapsed)))
			case <-done:
				return
			}
		}
	}()
	return func() { close(done) }
}
