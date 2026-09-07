// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package inspector

import "sync"

const (
	inspectorFunnelStageEvent       = "inspector.funnel.stage"
	inspectorFunnelStageAttribute   = "stage"
	inspectorFunnelOutcomeAttribute = "outcome"

	inspectorFunnelStageUIReady = "ui_ready"
	inspectorFunnelSucceeded    = "succeeded"
)

// ReportUsageFunc records one extension-owned usage event.
type ReportUsageFunc func(eventName string, attributes map[string]string)

func newUIReadyReporter(reportUsage ReportUsageFunc) func() {
	return sync.OnceFunc(func() {
		if reportUsage == nil {
			return
		}

		reportUsage(inspectorFunnelStageEvent, map[string]string{
			inspectorFunnelStageAttribute:   inspectorFunnelStageUIReady,
			inspectorFunnelOutcomeAttribute: inspectorFunnelSucceeded,
		})
	})
}
