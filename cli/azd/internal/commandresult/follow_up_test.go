// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package commandresult

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFollowUpCollector_ReplacesByExtension(t *testing.T) {
	collector := NewFollowUpCollector()

	collector.Add("test.extension", "first")
	collector.Add("test.extension", "second")

	require.Equal(t, "second", collector.Text())
}

func TestFollowUpCollector_SortsExtensions(t *testing.T) {
	collector := NewFollowUpCollector()

	collector.Add("z.extension", "z")
	collector.Add("a.extension", "a")

	require.Equal(t, "a\n\nz", collector.Text())
}

func TestFollowUpCollector_BlankTextProducesNoContribution(t *testing.T) {
	collector := NewFollowUpCollector()

	collector.Add("test.extension", " \n\t ")

	require.Empty(t, collector.Text())
}

func TestFollowUpCollector_ClearsExistingContribution(t *testing.T) {
	collector := NewFollowUpCollector()

	collector.Add("test.extension", "first")
	collector.Add("test.extension", "")

	require.Empty(t, collector.Text())
}

func TestFollowUpCollector_ClearKeepsOtherContributions(t *testing.T) {
	collector := NewFollowUpCollector()

	collector.Add("a.extension", "first")
	collector.Add("b.extension", "other")
	collector.Add("a.extension", " \n\t ")

	require.Equal(t, "other", collector.Text())

	collector.Add("a.extension", "second")
	require.Equal(t, "second\n\nother", collector.Text())
}

func TestFollowUpCollector_ConcurrentWrites(t *testing.T) {
	collector := NewFollowUpCollector()
	var group sync.WaitGroup

	for i := range 100 {
		group.Go(func() {
			collector.Add(
				fmt.Sprintf("extension-%d", i),
				fmt.Sprintf("follow-up-%d", i),
			)
		})
	}

	group.Wait()
	require.Len(t, collector.contributions, 100)
}

func TestFollowUpCollector_Context(t *testing.T) {
	collector := NewFollowUpCollector()
	ctx := WithFollowUpCollector(t.Context(), collector)

	require.Same(t, collector, FollowUpCollectorFromContext(ctx))
	require.Nil(t, FollowUpCollectorFromContext(t.Context()))
}
