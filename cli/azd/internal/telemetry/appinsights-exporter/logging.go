// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package appinsightsexporter

import "fmt"

// Provides simple local logging functionality for the telemetry library.

type logger struct {
	listen func(s string)
}

// Process-wide logger
var diagLog logger

func SetListener(listener func(s string)) {
	diagLog.listen = listener
}

func (log *logger) Printf(format string, a ...interface{}) {
	log.listen(fmt.Sprintf(format, a...))
}
