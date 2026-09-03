// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package commandresult

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFollowUpCollector_ResolvesByEventOrder(t *testing.T) {
	collector := NewFollowUpCollector()

	recordFollowUp(collector, "test.extension", "postdeploy", "", "deploy")
	recordFollowUp(collector, "test.extension", "postbuild", "", "build")

	require.Equal(t, "deploy", collector.Text())
}

func TestFollowUpCollector_SortsExtensions(t *testing.T) {
	collector := NewFollowUpCollector()

	recordFollowUp(collector, "z.extension", "postdeploy", "", "z")
	recordFollowUp(collector, "a.extension", "postdeploy", "", "a")

	require.Equal(t, "a\n\nz", collector.Text())
}

func TestFollowUpCollector_ExplicitBlankProducesNoContribution(t *testing.T) {
	collector := NewFollowUpCollector()

	recordFollowUp(collector, "test.extension", "postdeploy", "", " \n\t ")

	require.Empty(t, collector.Text())
}

func TestFollowUpCollector_ClearsExistingContribution(t *testing.T) {
	collector := NewFollowUpCollector()

	recordFollowUp(collector, "test.extension", "postbuild", "", "first")
	recordFollowUp(collector, "test.extension", "postdeploy", "", "")

	require.Empty(t, collector.Text())
}

func TestFollowUpCollector_ClearKeepsOtherContributions(t *testing.T) {
	collector := NewFollowUpCollector()

	recordFollowUp(collector, "a.extension", "postdeploy", "", "first")
	recordFollowUp(collector, "b.extension", "postdeploy", "", "other")
	recordFollowUp(collector, "a.extension", "postdeploy", "", " \n\t ")

	require.Equal(t, "other", collector.Text())

	recordFollowUp(collector, "a.extension", "postdeploy", "", "second")
	require.Equal(t, "second\n\nother", collector.Text())
}

func TestFollowUpCollector_NilLeavesExistingContribution(t *testing.T) {
	collector := NewFollowUpCollector()
	recordFollowUp(collector, "test.extension", "postdeploy", "", "first")

	collector.Record("test.extension", "postdeploy", "", nil)

	require.Equal(t, "first", collector.Text())
}

func TestFollowUpCollector_SortsLayerInstances(t *testing.T) {
	collector := NewFollowUpCollector()

	recordFollowUp(collector, "test.extension", "postprovision", "layer-b", "b")
	recordFollowUp(collector, "test.extension", "postprovision", "layer-a", "a")

	require.Equal(t, "b", collector.Text())
}

func TestFollowUpCollector_ConcurrentWrites(t *testing.T) {
	collector := NewFollowUpCollector()
	var group sync.WaitGroup

	for i := range 100 {
		group.Go(func() {
			recordFollowUp(
				collector,
				fmt.Sprintf("extension-%d", i),
				"postdeploy",
				"",
				fmt.Sprintf("follow-up-%d", i),
			)
		})
	}

	group.Wait()
	require.Len(t, collector.contributions, 100)
}

func recordFollowUp(
	collector *FollowUpCollector,
	extensionID string,
	eventName string,
	instanceID string,
	text string,
) {
	collector.Record(extensionID, eventName, instanceID, &text)
}

func TestFollowUpCollector_Context(t *testing.T) {
	collector := NewFollowUpCollector()
	ctx := WithFollowUpCollector(t.Context(), collector)

	require.Same(t, collector, FollowUpCollectorFromContext(ctx))
	require.Nil(t, FollowUpCollectorFromContext(t.Context()))
}
