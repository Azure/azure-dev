// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore BYOAKS

package telemetry

import (
	foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
)

const (
	agentHostingSelectedEvent = "agent.hosting.selected"
	agentHostingModeAttribute = "agent.hosting.mode"
)

// AgentHostingMode identifies the resolved agent hosting compute.
type AgentHostingMode string

const (
	AgentHostingModeBYOAKS AgentHostingMode = "byo_aks"
)

// AgentHostingSelected builds the hosting mode adoption event.
func AgentHostingSelected(mode AgentHostingMode) foundryTelemetry.Event {
	return foundryTelemetry.Event{
		Name: agentHostingSelectedEvent,
		Attributes: map[string]string{
			agentHostingModeAttribute: string(mode),
		},
	}
}
