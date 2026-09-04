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

// AspireUnsupportedAppHostEvent tracks when azd detects an Aspire polyglot (non-C#) AppHost
// (e.g. a TypeScript or Python AppHost) which azd does not yet support. See
// https://github.com/Azure/azure-dev/issues/7138.
const AspireUnsupportedAppHostEvent = "aspire.apphost.unsupported"

// Extension related events.
const (
	ExtensionRunEvent     = "ext.run"
	ExtensionInstallEvent = "ext.install"
	// ExtensionUpdateEvent tracks a single extension update attempt.
	ExtensionUpdateEvent = "ext.update"
	// ExtensionUninstallEvent tracks the removal of a single extension by
	// `azd extension uninstall`, including dependencies removed alongside it.
	ExtensionUninstallEvent = "ext.uninstall"
	// ExtensionPromoteEvent tracks a registry promotion (e.g., dev → main).
	ExtensionPromoteEvent = "ext.promote"
	// ExtensionUsageEvent carries one usage event an extension reported
	// through the telemetry service. The host stamps the extension's
	// identity and namespaces every attribute the extension supplied.
	ExtensionUsageEvent = "ext.usage"
)

// Copilot agent related events.
const (
	// CopilotInitializeEvent tracks the agent initialization flow (model/reasoning config).
	CopilotInitializeEvent = "copilot.initialize"

	// CopilotSessionEvent tracks session creation or resumption.
	CopilotSessionEvent = "copilot.session"
)

// Provision validation events.
const (
	// ProvisionValidationEvent tracks the local provision validation operation
	// and its outcome (passed, warnings accepted, canceled).
	ProvisionValidationEvent = "validation.provision"
)

// Hook execution events.
const (
	// HooksExecEvent tracks the execution of a lifecycle hook.
	HooksExecEvent = "hooks.exec"
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

// Execution graph events.
const (
	// ExeGraphRunEvent is the root span for executing an entire graph.
	ExeGraphRunEvent = "exegraph.run"

	// ExeGraphStepEvent is the span for a single step execution within the graph.
	ExeGraphStepEvent = "exegraph.step"
)
