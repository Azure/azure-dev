// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package telemetry

const (
	inspectorFunnelStageEvent       = "inspector.funnel.stage"
	inspectorFunnelStageAttribute   = "stage"
	inspectorFunnelOutcomeAttribute = "outcome"

	inspectorFunnelStageUIReady = "ui_ready"
	inspectorFunnelSucceeded    = "succeeded"
)

// InspectorUIReady creates the event emitted after the Inspector UI mounts.
func InspectorUIReady() Event {
	return Event{
		Name: inspectorFunnelStageEvent,
		Attributes: map[string]string{
			inspectorFunnelStageAttribute:   inspectorFunnelStageUIReady,
			inspectorFunnelOutcomeAttribute: inspectorFunnelSucceeded,
		},
	}
}
