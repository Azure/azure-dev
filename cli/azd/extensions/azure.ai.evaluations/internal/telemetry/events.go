// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package telemetry holds the usage events this extension is approved to report.
//
// Values are closed sets, normalized by the builders here rather than trusted
// from the call site, because the host does not review what an attribute means
// and a value that can be anything is both unusable for aggregation and the way
// customer content escapes.
package telemetry

import foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"

const (
	initCompletedEvent  = "init.completed"
	initSourceAttribute = "source"
)

// InitSource identifies the rows a scaffolded eval will grade.
type InitSource string

const (
	InitSourceTraces  InitSource = "traces"
	InitSourceDataset InitSource = "dataset"
	// InitSourceUnknown keeps an unrecognized source from widening the
	// attribute into an open set.
	InitSourceUnknown InitSource = "unknown"
)

// NewInitSource maps a settled source onto the reportable set.
func NewInitSource(source string) InitSource {
	switch InitSource(source) {
	case InitSourceTraces:
		return InitSourceTraces
	case InitSourceDataset:
		return InitSourceDataset
	default:
		return InitSourceUnknown
	}
}

// InitCompleted creates the event emitted once init has written a scaffold.
//
// The source is normalized here rather than taken on trust. InitSource is a
// string type, so InitCompleted("anything") compiles; a closed set that holds
// only while every call site remembers NewInitSource is not closed, and this is
// the boundary the privacy review is about.
func InitCompleted(source string) foundryTelemetry.Event {
	return foundryTelemetry.Event{
		Name: initCompletedEvent,
		Attributes: map[string]string{
			initSourceAttribute: string(NewInitSource(source)),
		},
	}
}
