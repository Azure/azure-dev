// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package appinsightsexporter

import "fmt"

// Provides simple local logging functionality for the telemetry library.

type logger struct {
	listen func(s string)
}

// Process-wide logger
var diagLog logger = logger{listen: func(string) {}}

// Sets the diagnostics logging listener for telemetry related warnings.
// This is NOT thread-safe, and thus should be set once, early in application lifecycle.
func SetListener(listener func(s string)) {
	diagLog.listen = listener
}

func (log *logger) Printf(format string, a ...interface{}) {
	log.listen(fmt.Sprintf(format, a...))
}
