// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package telemetry

const (
	localClientRouteSelectedEvent = "local_client.route.selected"
	localClientRouteAttribute     = "route"
)

// LocalClientRoute identifies the client selected for a local agent run.
type LocalClientRoute string

const (
	LocalClientRouteInspector  LocalClientRoute = "inspector"
	LocalClientRoutePlayground LocalClientRoute = "playground"
	LocalClientRouteSuppressed LocalClientRoute = "suppressed"
)

// LocalClientRouteSelected creates the event emitted after resolving the local client route.
func LocalClientRouteSelected(route LocalClientRoute) Event {
	return Event{
		Name: localClientRouteSelectedEvent,
		Attributes: map[string]string{
			localClientRouteAttribute: string(route),
		},
	}
}
