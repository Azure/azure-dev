// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package telemetry

import (
	"strconv"

	foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
)

const (
	agentInvokeSelectedEvent      = "agent.invoke.selected"
	invokeProtocolAttribute       = "agent.invoke.protocol"
	invokeLongRunningAttribute    = "agent.invoke.long_running"
	invokeNoWaitAttribute         = "agent.invoke.no_wait"
	localClientRouteSelectedEvent = "local_client.route.selected"
	localClientRouteAttribute     = "route"
)

// AgentInvokeSelected creates the adoption event for a resolved remote hosted-agent invoke.
// It records the selected request mode, not a service response or success.
// The caller supplies the protocol resolved and validated by the invoke command.
func AgentInvokeSelected(protocol string, longRunning, noWait bool) foundryTelemetry.Event {
	return foundryTelemetry.Event{
		Name: agentInvokeSelectedEvent,
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
