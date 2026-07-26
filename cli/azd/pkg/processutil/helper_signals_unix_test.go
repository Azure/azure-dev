// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !windows

package processutil

import (
	"os"
	"syscall"
)

// ignorableSignals lists the signals a helper process must swallow so the escalation
// path can be observed. SIGKILL is deliberately absent because it cannot be caught,
// which is exactly what makes the forceful stop reliable.
func ignorableSignals() []os.Signal {
	return []os.Signal{syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP}
}
