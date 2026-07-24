// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package telemetry defines the extension telemetry contract values shared by
// the azd host and extensions. Keeping the key and the allowed enum values in
// one dependency-free package prevents core and extension code from duplicating
// telemetry string literals.
package telemetry

// AgentDeploymentModeAttribute is the command-level usage attribute that
// records the set of hosted agent deployment modes selected during a command.
// Its value is a de-duplicated string slice.
const AgentDeploymentModeAttribute = "agent.deploy.mode"

// AgentDeploymentMode identifies the selected hosted agent deployment source.
type AgentDeploymentMode string

const (
	// AgentDeploymentModeCode deploys a source archive.
	AgentDeploymentModeCode AgentDeploymentMode = "code"

	// AgentDeploymentModeContainer deploys a container image that azd builds
	// and publishes from the project source.
	AgentDeploymentModeContainer AgentDeploymentMode = "container"

	// AgentDeploymentModeByoImage deploys a caller-supplied image.
	AgentDeploymentModeByoImage AgentDeploymentMode = "byo_image"
)
