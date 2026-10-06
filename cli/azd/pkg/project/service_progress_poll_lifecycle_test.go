// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/stretchr/testify/require"
)

func TestStartPollingProgress_PreservesResultAndFinalProgress(t *testing.T) {
	for _, operationErr := range []error{nil, errors.New("operation failed")} {
		t.Run(fmt.Sprintf("error=%v", operationErr), func(t *testing.T) {
			var messages []ServiceProgress
			result, err := async.RunWithProgress(
				func(update ServiceProgress) { messages = append(messages, update) },
				func(progress *async.Progress[ServiceProgress]) (string, error) {
					stop := startPollingProgress(progress, "Waiting", time.Hour)
					stop()
					stop()
					progress.SetProgress(NewServiceProgress("Final"))
					return "result", operationErr
				},
			)
			require.Equal(t, "result", result)
			require.Equal(t, operationErr, err)
			require.Equal(t, []ServiceProgress{NewServiceProgress("Final")}, messages)
		})
	}
}

func TestStartPollingProgress_StopJoinsBlockedSender(t *testing.T) {
	progress := async.NewProgress[ServiceProgress]()
	stop := startPollingProgress(progress, "Waiting", time.Nanosecond)

	// Synchronize with an actual blocked send, rather than relying on a sleep.
	deadline := time.Now().Add(5 * time.Second)
	stack := make([]byte, 1<<20)
	blocked := false
	for time.Now().Before(deadline) {
		n := runtime.Stack(stack, true)
		for goroutine := range strings.SplitSeq(string(stack[:n]), "\n\n") {
			if strings.Contains(goroutine, "startPollingProgress.func1") &&
				(strings.Contains(goroutine, "[chan send]") ||
					strings.Contains(goroutine, "SetProgressWithContext")) {
				blocked = true
				break
			}
		}
		if blocked {
			break
		}
		runtime.Gosched()
	}
	require.True(t, blocked, "poller did not reach a blocked progress send")

	stop()
	progress.Done()
	for time.Now().Before(deadline) {
		n := runtime.Stack(stack, true)
		if !strings.Contains(string(stack[:n]), "startPollingProgress.func1") {
			break
		}
		runtime.Gosched()
	}
	n := runtime.Stack(stack, true)
	require.NotContains(t, string(stack[:n]), "startPollingProgress.func1")
	for range progress.Progress() {
		t.Fatal("stopped poller emitted an update after its owner closed progress")
	}
}
