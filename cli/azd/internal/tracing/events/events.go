// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package events provides definitions and functions related to the definition of telemetry events.
package events

// Command event names follow the convention cmd.<command invocation path with spaces replaced by .>.
//
// Examples:
//   - cmd.auth.login
//   - cmd.init
//   - cmd.up
const CommandEventPrefix = "cmd."

// Prefix for vsrpc events.
const VsRpcEventPrefix = "vsrpc."

// Prefix for MCP related events.
const McpEventPrefix = "mcp."

// PackBuildEvent is the name of the event which tracks the overall pack build operation.
const PackBuildEvent = "tools.pack.build"

// AgentTroubleshootEvent is the name of the event which tracks agent troubleshoot operations.
const AgentTroubleshootEvent = "agent.troubleshoot"

// Extension related events.
const (
	ExtensionRunEvent     = "ext.run"
	ExtensionInstallEvent = "ext.install"
)

// Copilot agent related events.
const (
	// CopilotInitializeEvent tracks the agent initialization flow (model/reasoning config).
	CopilotInitializeEvent = "copilot.initialize"

	// CopilotSessionEvent tracks session creation or resumption.
	CopilotSessionEvent = "copilot.session"
)

// Preflight validation events.
const (
	// PreflightValidationEvent tracks the local preflight validation operation
	// and its outcome (passed, warnings accepted, aborted).
	PreflightValidationEvent = "validation.preflight"
)

// AKS service target events.
const (
	// AksPostprovisionSkipEvent tracks when the AKS postprovision hook
	// skips Kubernetes context setup because the cluster isn't available yet.
	AksPostprovisionSkipEvent = "aks.postprovision.skip"
)

// ARM deployment events track provisioning, validation, and preview operations.
const (
	ArmDeploySubscriptionEvent       = "arm.deploy.subscription"
	ArmDeployResourceGroupEvent      = "arm.deploy.resourcegroup"
	ArmStackDeploySubscriptionEvent  = "arm.stack.deploy.subscription"
	ArmStackDeployResourceGroupEvent = "arm.stack.deploy.resourcegroup"
	ArmWhatIfSubscriptionEvent       = "arm.whatif.subscription"
	ArmWhatIfResourceGroupEvent      = "arm.whatif.resourcegroup"
	ArmValidateSubscriptionEvent     = "arm.validate.subscription"
	ArmValidateResourceGroupEvent    = "arm.validate.resourcegroup"
)

// App Service deployment events.
const (
	DeployAppServiceZipEvent = "deploy.appservice.zip"
)

// Container lifecycle events.
const (
	ContainerCredentialsEvent = "container.credentials"
	ContainerPublishEvent     = "container.publish"
	ContainerRemoteBuildEvent = "container.remotebuild"
)
