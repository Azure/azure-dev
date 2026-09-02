// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package inspector

import (
	"sync"
	"testing"

	"azureaiinspector/internal/telemetry"

	"github.com/stretchr/testify/require"
)

func TestUIReadyReporterReportsFunnelStageOnce(t *testing.T) {
	var events []telemetry.Event
	reportUIReady := newUIReadyReporter(func(event telemetry.Event) {
		events = append(events, event)
	})

	var wg sync.WaitGroup
	for range 10 {
		wg.Go(reportUIReady)
	}
	wg.Wait()

	require.Equal(t, []telemetry.Event{{
		Name: "inspector.funnel.stage",
		Attributes: map[string]string{
			"stage":   "ui_ready",
			"outcome": "succeeded",
		},
	}}, events)
}
