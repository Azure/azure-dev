// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package telemetry

import (
	"strconv"

	foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
)

const (
	agentInvokedEvent             = "agent.invoked"
	invokeProtocolAttribute       = "protocol"
	invokeLongRunningAttribute    = "long_running"
	invokeNoWaitAttribute         = "no_wait"
	localClientRouteSelectedEvent = "local_client.route.selected"
	localClientRouteAttribute     = "route"
)

// AgentInvoked creates the adoption event for a validated remote hosted-agent invoke.
// It records the selected request mode, not a service response or success.
func AgentInvoked(protocol string, longRunning, noWait bool) foundryTelemetry.Event {
	switch protocol {
	case "responses", "invocations", "a2a":
	default:
		protocol = "unknown"
	}

	return foundryTelemetry.Event{
		Name: agentInvokedEvent,
		Attributes: map[string]string{
			invokeProtocolAttribute:    protocol,
			invokeLongRunningAttribute: strconv.FormatBool(longRunning),
			invokeNoWaitAttribute:      strconv.FormatBool(noWait),
		},
	}
}

// LocalClientRoute identifies the client selected for a local agent run.
type LocalClientRoute string

const (
	LocalClientRouteInspector  LocalClientRoute = "inspector"
	LocalClientRoutePlayground LocalClientRoute = "playground"
	LocalClientRouteSuppressed LocalClientRoute = "suppressed"
)

// LocalClientRouteSelected creates the event emitted after resolving the local client route.
func LocalClientRouteSelected(route LocalClientRoute) foundryTelemetry.Event {
	return foundryTelemetry.Event{
		Name: localClientRouteSelectedEvent,
		Attributes: map[string]string{
			localClientRouteAttribute: string(route),
		},
	}
}
