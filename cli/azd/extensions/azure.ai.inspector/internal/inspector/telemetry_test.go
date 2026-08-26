// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package inspector

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUIReadyReporterReportsFunnelStageOnce(t *testing.T) {
	type usageEvent struct {
		name       string
		attributes map[string]string
	}

	var events []usageEvent
	reportUIReady := newUIReadyReporter(func(eventName string, attributes map[string]string) {
		events = append(events, usageEvent{
			name:       eventName,
			attributes: attributes,
		})
	})

	var wg sync.WaitGroup
	for range 10 {
		wg.Go(reportUIReady)
	}
	wg.Wait()

	require.Equal(t, []usageEvent{{
		name: inspectorFunnelStageEvent,
		attributes: map[string]string{
			inspectorFunnelStageAttribute:   inspectorFunnelStageUIReady,
			inspectorFunnelOutcomeAttribute: inspectorFunnelSucceeded,
		},
	}}, events)
}
