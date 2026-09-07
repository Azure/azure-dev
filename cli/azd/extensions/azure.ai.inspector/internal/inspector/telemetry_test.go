// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package inspector

import (
	"sync"
	"testing"

	foundrytelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
	"github.com/stretchr/testify/require"
)

func TestUIReadyReporterReportsFunnelStageOnce(t *testing.T) {
	var events []foundrytelemetry.Event
	reportUIReady := newUIReadyReporter(func(event foundrytelemetry.Event) {
		events = append(events, event)
	})

	var wg sync.WaitGroup
	for range 10 {
		wg.Go(reportUIReady)
	}
	wg.Wait()

	require.Equal(t, []foundrytelemetry.Event{{
		Name: "inspector.funnel.stage",
		Attributes: map[string]string{
			"stage":   "ui_ready",
			"outcome": "succeeded",
		},
	}}, events)
}
