// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package inspector

import (
	"sync"

	"azureaiinspector/internal/telemetry"

	foundrytelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
)

// ReportUsageFunc records one extension-owned usage event.
type ReportUsageFunc func(event foundrytelemetry.Event)

func newUIReadyReporter(reportUsage ReportUsageFunc) func() {
	return sync.OnceFunc(func() {
		if reportUsage == nil {
			return
		}

		reportUsage(telemetry.InspectorUIReady())
	})
}
