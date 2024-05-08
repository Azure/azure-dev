// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build record

package httputil

import (
	"os"
	"time"
)

// By default, PollDelay is a no-op function that returns the delay as-is.
// This function is overridden in the record mode to return the delay as 0.
func PollDelay(delay time.Duration) time.Duration {
	d := os.Getenv("AZD_TEST_POLL_DELAY")
	if d != "" {
		testDelay, err := time.ParseDuration(d)
		if err != nil {
			panic(err)
		}
		delay = testDelay
	}
	return delay
}
