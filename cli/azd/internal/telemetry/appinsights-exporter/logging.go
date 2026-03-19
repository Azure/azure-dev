// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package appinsightsexporter

import (
	"fmt"
	"sync"
)

// Provides simple local logging functionality for the telemetry library.

type logger struct {
	mu     sync.RWMutex
	listen func(s string)
}

// Process-wide logger
var diagLog logger = logger{listen: func(string) {}}

// SetListener sets the diagnostics logging listener for telemetry related warnings.
// This is safe to call concurrently.
func SetListener(listener func(s string)) {
	diagLog.mu.Lock()
	defer diagLog.mu.Unlock()
	diagLog.listen = listener
}

func (log *logger) Printf(format string, a ...any) {
	log.mu.RLock()
	fn := log.listen
	log.mu.RUnlock()
	// Safe: fn is an immutable function reference; calling outside lock avoids potential deadlocks
	fn(fmt.Sprintf(format, a...))
}
