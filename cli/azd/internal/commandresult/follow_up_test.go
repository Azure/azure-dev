// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package commandresult

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func addFollowUp(
	collector *FollowUpCollector,
	extensionID, eventName, layer, text string,
) {
	collector.Add(FollowUp{
		ExtensionID: extensionID,
		EventName:   eventName,
		Layer:       layer,
		Text:        text,
	})
}

func TestFollowUpCollector_LaterEventReplacesEarlier(t *testing.T) {
	collector := NewFollowUpCollector()

	addFollowUp(collector, "test.extension", "postprovision", "", "first")
	addFollowUp(collector, "test.extension", "postdeploy", "", "second")

	require.Equal(t, "second", collector.Text())
}

func TestFollowUpCollector_EventOrderIgnoresCompletionOrder(t *testing.T) {
	collector := NewFollowUpCollector()

	addFollowUp(collector, "test.extension", "postdeploy", "", "deploy")
	addFollowUp(collector, "test.extension", "postprovision", "", "provision")

	require.Equal(t, "deploy", collector.Text())
}

func TestFollowUpCollector_SortsExtensions(t *testing.T) {
	collector := NewFollowUpCollector()

	addFollowUp(collector, "z.extension", "postdeploy", "", "z")
	addFollowUp(collector, "a.extension", "postdeploy", "", "a")

	require.Equal(t, "a\n\nz", collector.Text())
}

func TestFollowUpCollector_BlankTextProducesNoContribution(t *testing.T) {
	collector := NewFollowUpCollector()

	addFollowUp(collector, "test.extension", "postdeploy", "", " \n\t ")

	require.Empty(t, collector.Text())
}

func TestFollowUpCollector_LaterEventRetractsContribution(t *testing.T) {
	collector := NewFollowUpCollector()

	addFollowUp(collector, "test.extension", "postprovision", "", "first")
	addFollowUp(collector, "test.extension", "postdeploy", "", "")

	require.Empty(t, collector.Text())
}

func TestFollowUpCollector_UnsetLaterEventKeepsEarlier(t *testing.T) {
	collector := NewFollowUpCollector()

	addFollowUp(collector, "test.extension", "postprovision", "", "first")

	require.Equal(t, "first", collector.Text())
}

func TestFollowUpCollector_RetractKeepsOtherContributions(t *testing.T) {
	collector := NewFollowUpCollector()

	addFollowUp(collector, "a.extension", "postprovision", "", "first")
	addFollowUp(collector, "b.extension", "postdeploy", "", "other")
	addFollowUp(collector, "a.extension", "postdeploy", "", " \n\t ")

	require.Equal(t, "other", collector.Text())

	addFollowUp(collector, "a.extension", "postdeploy", "", "second")
	require.Equal(t, "second\n\nother", collector.Text())
}

func TestFollowUpCollector_LayerIdentityIgnoresCompletionOrder(t *testing.T) {
	collector := NewFollowUpCollector()

	addFollowUp(collector, "test.extension", "postprovision", "b", "from-b")
	addFollowUp(collector, "test.extension", "postprovision", "a", "from-a")

	require.Equal(t, "from-b", collector.Text())

	collector = NewFollowUpCollector()
	addFollowUp(collector, "test.extension", "postprovision", "a", "from-a")
	addFollowUp(collector, "test.extension", "postprovision", "b", "from-b")

	require.Equal(t, "from-b", collector.Text())
}

func TestFollowUpCollector_NoOpLayerDoesNotClear(t *testing.T) {
	collector := NewFollowUpCollector()

	addFollowUp(collector, "test.extension", "postprovision", "app", "keep")

	require.Equal(t, "keep", collector.Text())
}

func TestFollowUpCollector_ConcurrentWrites(t *testing.T) {
	collector := NewFollowUpCollector()
	var group sync.WaitGroup

	for i := range 100 {
		group.Go(func() {
			addFollowUp(
				collector,
				fmt.Sprintf("extension-%d", i),
				"postdeploy",
				"",
				fmt.Sprintf("follow-up-%d", i),
			)
		})
	}

	group.Wait()
	require.Len(t, strings.Split(collector.Text(), "\n\n"), 100)
}

func TestFollowUpCollector_ConcurrentLayersAreDeterministic(t *testing.T) {
	collector := NewFollowUpCollector()
	var group sync.WaitGroup

	for range 50 {
		group.Go(func() {
			addFollowUp(collector, "test.extension", "postprovision", "app", "app")
		})
		group.Go(func() {
			addFollowUp(collector, "test.extension", "postprovision", "data", "data")
		})
	}

	group.Wait()
	require.Equal(t, "data", collector.Text())
}

func TestFollowUpCollector_Context(t *testing.T) {
	collector := NewFollowUpCollector()
	ctx := WithFollowUpCollector(t.Context(), collector)

	require.Same(t, collector, FollowUpCollectorFromContext(ctx))
	require.Nil(t, FollowUpCollectorFromContext(t.Context()))
}
