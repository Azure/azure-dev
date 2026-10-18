// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package commandresult

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServiceEventMessageCollector_SnapshotOrdering(t *testing.T) {
	collector := NewServiceEventMessageCollector()
	collector.Add([]ServiceEventMessage{
		{
			ServiceName: "worker",
			EventName:   "predeploy",
			ExtensionID: "extension-b",
			Message:     "worker message",
		},
		{
			ServiceName: "api",
			EventName:   "postdeploy",
			ExtensionID: "extension-b",
			Message:     "api post",
		},
	})
	collector.Add([]ServiceEventMessage{
		{
			ServiceName: "api",
			EventName:   "predeploy",
			ExtensionID: "extension-b",
			Message:     "api pre 1",
		},
		{
			ServiceName: "api",
			EventName:   "predeploy",
			ExtensionID: "extension-b",
			Message:     "api pre 2",
		},
	})
	collector.Add([]ServiceEventMessage{
		{
			ServiceName: "api",
			EventName:   "predeploy",
			ExtensionID: "extension-a",
			Message:     "api pre extension-a",
		},
		{
			ServiceName: "api",
			EventName:   "postdeploy",
			ExtensionID: "extension-a",
			Message:     "api post extension-a",
		},
	})

	messages := collector.Snapshot([]string{"api", "worker"})
	require.Equal(t, []string{
		"api pre extension-a",
		"api pre 1",
		"api pre 2",
		"api post extension-a",
		"api post",
		"worker message",
	}, serviceEventMessageTexts(messages))
}

func TestServiceEventMessageCollector_SnapshotsAreIndependent(t *testing.T) {
	collector := NewServiceEventMessageCollector()
	links := []ServiceEventMessageLink{{Title: "Guide", URL: "https://example.com"}}
	collector.Add([]ServiceEventMessage{{
		ServiceName: "api",
		EventName:   "predeploy",
		Message:     "Check the service",
		Links:       links,
	}})
	links[0].Title = "changed"

	first := collector.Snapshot([]string{"api"})
	require.Equal(t, "Guide", first[0].Links[0].Title)
	first[0].Links[0].Title = "changed again"

	second := collector.Snapshot([]string{"api"})
	require.Equal(t, "Guide", second[0].Links[0].Title)
}

func TestServiceEventMessageCollector_ConcurrentAdd(t *testing.T) {
	collector := NewServiceEventMessageCollector()
	var waitGroup sync.WaitGroup
	for range 20 {
		waitGroup.Go(func() {
			collector.Add([]ServiceEventMessage{{
				ServiceName: "api",
				EventName:   "predeploy",
				Message:     "message",
			}})
		})
	}
	waitGroup.Wait()

	require.Len(t, collector.Snapshot([]string{"api"}), 20)
}

func serviceEventMessageTexts(messages []ServiceEventMessage) []string {
	texts := make([]string, len(messages))
	for index, message := range messages {
		texts[index] = message.Message
	}
	return texts
}
