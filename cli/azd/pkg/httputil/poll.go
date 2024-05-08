// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !record

package httputil

import (
	"time"
)

// By default, PollDelay is a no-op function that returns the delay as-is.
// This function is overridden in the record mode to return a suitable delay for testing.
func PollDelay(delay time.Duration) time.Duration {
	return delay
}
