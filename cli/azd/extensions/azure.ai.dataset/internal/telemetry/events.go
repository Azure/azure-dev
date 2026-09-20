// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package telemetry holds the usage events this extension is approved to report.
//
// Values are closed sets declared here rather than strings passed in at the
// call site, because the host does not review what an attribute means and a
// value that can be anything is both unusable for aggregation and the way
// customer content escapes.
package telemetry

import foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"

const (
	datasetPublishedEvent = "dataset.published"
	operationAttribute    = "operation"
)

// Operation identifies the write that published a dataset version.
type Operation string

const (
	OperationCreate Operation = "create"
	OperationUpdate Operation = "update"
	// OperationUnknown keeps an unrecognized verb from widening the
	// attribute into an open set.
	OperationUnknown Operation = "unknown"
)

// NewOperation maps a command verb onto the reportable set.
func NewOperation(verb string) Operation {
	switch Operation(verb) {
	case OperationCreate:
		return OperationCreate
	case OperationUpdate:
		return OperationUpdate
	default:
		return OperationUnknown
	}
}

// DatasetPublished creates the event emitted once a dataset version is written.
func DatasetPublished(operation Operation) foundryTelemetry.Event {
	return foundryTelemetry.Event{
		Name: datasetPublishedEvent,
		Attributes: map[string]string{
			operationAttribute: string(operation),
		},
	}
}
