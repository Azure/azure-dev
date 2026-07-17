// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package fields provides definitions and functions related to the definition of telemetry fields.
package fields

import (
	"github.com/microsoft/ApplicationInsights-Go/appinsights/contracts"
	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.39.0"
)

// AttributeKey represents an attribute key with additional metadata.
type AttributeKey struct {
	attribute.Key
	Classification Classification
	Purpose        Purpose
	Endpoint       string
	IsMeasurement  bool
}

type Classification string

const (
	PublicPersonalData                    Classification = "PublicPersonalData"
	SystemMetadata                        Classification = "SystemMetadata"
	CallstackOrException                  Classification = "CallstackOrException"
	CustomerContent                       Classification = "CustomerContent"
	EndUserPseudonymizedInformation       Classification = "EndUserPseudonymizedInformation"
	OrganizationalIdentifiableInformation Classification = "OrganizationalIdentifiableInformation"
)

type Purpose string

const (
	FeatureInsight       Purpose = "FeatureInsight"
	BusinessInsight      Purpose = "BusinessInsight"
	PerformanceAndHealth Purpose = "PerformanceAndHealth"
)

// Application-level fields. Guaranteed to be set and available for all events.
var (
	// Application name. Value is always "azd".
	ServiceNameKey = AttributeKey{
		Key: semconv.ServiceNameKey, // service.name
	}

	// Application version.
	ServiceVersionKey = AttributeKey{
		Key: semconv.ServiceVersionKey, // service.version
	}

	// The operating system type.
	OSTypeKey = AttributeKey{
		Key: semconv.OSTypeKey, // os.type
	}

	// The operating system version.
	//
	// Examples:
	//   - On Windows systems: The Windows version 10.x.x
	//   - On Unix-based systems: The release portion of uname. https://en.wikipedia.org/wiki/Uname#Examples
	//   - On MacOS: The MacOS version. For example: 12.5.1 for macOS Monterey
	OSVersionKey = AttributeKey{
		Key:            semconv.OSVersionKey, // os.version
		Classification: SystemMetadata,
		Purpose:        PerformanceAndHealth,
	}

	// The CPU architecture the host system is running on.
	HostArchKey = AttributeKey{
		Key:            semconv.HostArchKey, // host.arch
		Classification: SystemMetadata,
		Purpose:        PerformanceAndHealth,
	}

	// The version of the runtime of this process, as returned by the runtime without
	// modification.
	ProcessRuntimeVersionKey = AttributeKey{
		Key:            semconv.ProcessRuntimeVersionKey, // process.runtime.version
		Classification: SystemMetadata,
		Purpose:        PerformanceAndHealth,
	}

	// A unique ID associated to the machine the application is installed on.
	//
	// This shares implementation with VSCode's machineId and can match exactly on a given device, although there are no
	// guarantees.
	MachineIdKey = AttributeKey{
		Key:            attribute.Key("machine.id"),
		Classification: EndUserPseudonymizedInformation,
		Endpoint:       "MacAddressHash",
		Purpose:        BusinessInsight,
	}

	// The unique DevDeviceId associated with the device.
	DevDeviceIdKey = AttributeKey{
		Key:            attribute.Key("machine.devdeviceid"),
		Classification: EndUserPseudonymizedInformation,
		Endpoint:       "SQMUserId",
		Purpose:        BusinessInsight,
	}

	// An enumeration of possible environments that the application is running on.
	//
	// Example: Desktop, Azure Pipelines, Visual Studio.
	//
	// See EnvDesktop for complete set of values.
	ExecutionEnvironmentKey = AttributeKey{
		Key:            attribute.Key("execution.environment"),
		Classification: SystemMetadata,
		Purpose:        BusinessInsight,
	}

	// Installer used to install the application. Set in .installed-by.txt file
	// located in the same folder as the executable.
	//
	// Example: "msi", "brew", "choco", "rpm", "deb"
	InstalledByKey = AttributeKey{
		Key:            attribute.Key("service.installer"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
)

// Fields related to the experimentation platform
var (
	// The assignment context as returned by the experimentation platform.
	ExpAssignmentContextKey = AttributeKey{
		Key:            attribute.Key("exp.assignmentContext"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
)

// Context level fields. Availability depends on the command running.
var (
	// Object ID of the principal.
	ObjectIdKey = attribute.Key(contracts.UserAuthUserId) // user_AuthenticatedId
	// Tenant ID of the principal.
	TenantIdKey = AttributeKey{
		Key:            attribute.Key("ad.tenant.id"),
		Classification: SystemMetadata,
		Purpose:        BusinessInsight,
	}
	// The type of account. See AccountTypeUser for all possible options.
	AccountTypeKey = AttributeKey{
		Key:            attribute.Key("ad.account.type"),
		Classification: SystemMetadata,
		Purpose:        BusinessInsight,
	}
	// Currently selected Subscription ID.
	SubscriptionIdKey = AttributeKey{
		Key:            attribute.Key("ad.subscription.id"),
		Classification: OrganizationalIdentifiableInformation,
		Purpose:        PerformanceAndHealth,
		Endpoint:       "AzureSubscriptionId",
	}
)

// Project (azure.yaml) related attributes
var (
	// Hashed template ID metadata
	ProjectTemplateIdKey = AttributeKey{
		Key:            attribute.Key("project.template.id"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
	// Hashed template.version metadata
	ProjectTemplateVersionKey = AttributeKey{
		Key:            attribute.Key("project.template.version"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
	// Hashed project name. Could be used as an indicator for number of different azd projects.
	ProjectNameKey = AttributeKey{
		Key:            attribute.Key("project.name"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
	// The collection of hashed service hosts in the project.
	ProjectServiceHostsKey = AttributeKey{
		Key:            attribute.Key("project.service.hosts"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
	// The collection of service targets (resolved service hosts) in the project.
	ProjectServiceTargetsKey = AttributeKey{
		Key:            attribute.Key("project.service.targets"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
	// The collection of hashed service languages in the project.
	ProjectServiceLanguagesKey = AttributeKey{
		Key:            attribute.Key("project.service.languages"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
	// The service language being executed.
	ProjectServiceLanguageKey = AttributeKey{
		Key:            attribute.Key("project.service.language"),
		Classification: SystemMetadata,
		Purpose:        PerformanceAndHealth,
	}
)

// Platform related attributes for integrations like devcenter / ADE
var (
	PlatformTypeKey = AttributeKey{
		Key:            attribute.Key("platform.type"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
)

// Machine-level configuration related attribute.
var (
	// Tracks features associated to the current event.
	FeaturesKey = AttributeKey{
		Key:            attribute.Key("config.features"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
)

// Environment related attributes
var (
	// Hashed environment name
	EnvNameKey = AttributeKey{
		Key:            attribute.Key("env.name"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
)

// Command entry-point attributes
var (
	// Flags set by the user. Only parsed flag names are available. Values are not recorded.
	CmdFlags = AttributeKey{
		Key:            attribute.Key("cmd.flags"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
	// Number of positional arguments set.
	CmdArgsCount = AttributeKey{
		Key:            attribute.Key("cmd.args.count"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
		IsMeasurement:  true,
	}
	// The command invocation entrypoint.
	//
	// The command invocation is formatted using [events.GetCommandEventName]. This makes it consistent with how
	// commands are represented in telemetry.
	CmdEntry = AttributeKey{
		Key:            attribute.Key("cmd.entry"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
)

// Deployment attributes
var (
	// DeployAttemptKey tracks the retry attempt number for App Service zip deployments.
	DeployAttemptKey = AttributeKey{
		Key:            attribute.Key("deploy.appservice.attempt"),
		Classification: SystemMetadata,
		Purpose:        PerformanceAndHealth,
		IsMeasurement:  true,
	}

	// DeployLinuxKey tracks whether an App Service deployment targets a Linux web app.
	DeployLinuxKey = AttributeKey{
		Key:            attribute.Key("deploy.appservice.linux"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
)

// All possible enumerations of ExecutionEnvironmentKey
//
// Environments are mutually exclusive. Modifiers can be set additionally to signal different types of usages.
// An execution environment is formatted as follows:
// `<environment>[;<modifier1>;<modifier2>...]`
const (
	// A desktop environment. The user is directly interacting with azd via a terminal.
	EnvDesktop = "Desktop"

	// Environments that are wrapped by an intermediate calling program, and are significant enough to warrant
	// being an environment and not an environment modifier.

	EnvVisualStudio       = "Visual Studio"
	EnvVisualStudioCode   = "Visual Studio Code"
	EnvVSCodeAzureCopilot = "VS Code Azure GitHub Copilot"
	EnvCloudShell         = "Azure CloudShell"

	// AI Coding Agent environments
	EnvClaudeCode       = "Claude Code"
	EnvGitHubCopilotCLI = "GitHub Copilot CLI"
	EnvGemini           = "Gemini"
	EnvOpenCode         = "OpenCode"

	// Continuous Integration environments

	EnvUnknownCI          = "UnknownCI"
	EnvAzurePipelines     = "Azure Pipelines"
	EnvGitHubActions      = "GitHub Actions"
	EnvAppVeyor           = "AppVeyor"
	EnvBamboo             = "Bamboo"
	EnvBitBucketPipelines = "BitBucket Pipelines"
	EnvTravisCI           = "Travis CI"
	EnvCircleCI           = "Circle CI"
	EnvGitLabCI           = "GitLab CI"
	EnvJenkins            = "Jenkins"
	EnvAwsCodeBuild       = "AWS CodeBuild"
	EnvGoogleCloudBuild   = "Google Cloud Build"
	EnvTeamCity           = "TeamCity"
	EnvJetBrainsSpace     = "JetBrains Space"
	EnvCodespaces         = "GitHub Codespaces"

	// Environment modifiers. These are not environments themselves, but rather modifiers to the environment
	// that signal specific types of usages.

	EnvModifierAzureSpace            = "Azure App Spaces Portal"
	EnvModifierMicrosoftFoundrySkill = "Microsoft Foundry Skill"
)

// All possible enumerations of AccountTypeKey
const (
	// A user.
	AccountTypeUser = "User"
	// A service principal, typically an application.
	AccountTypeServicePrincipal = "Service Principal"
)

// Auth command related fields
var (
	// The authentication method used for login.
	//
	// Example: "browser", "device-code", "service-principal-secret", "managed-identity"
	AuthMethodKey = AttributeKey{
		Key:            attribute.Key("auth.method"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
)

// Environment command related fields
var (
	// The number of environments that exist for the current project.
	EnvCountKey = AttributeKey{
		Key:            attribute.Key("env.count"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
		IsMeasurement:  true,
	}
)

// Hooks command related fields
var (
	// The name of the hook being run.
	HooksNameKey = AttributeKey{
		Key:            attribute.Key("hooks.name"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
	// The type of the hook run scope (project, layer, or service).
	HooksTypeKey = AttributeKey{
		Key:            attribute.Key("hooks.type"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
	// The executor kind used to run the hook (e.g., "sh", "pwsh", "python", "js", "ts", "dotnet").
	HooksKindKey = AttributeKey{
		Key:            attribute.Key("hooks.kind"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
)

// Pipeline command related fields
var (
	// The pipeline provider being configured.
	//
	// Example: "github", "azdo"
	PipelineProviderKey = AttributeKey{
		Key:            attribute.Key("pipeline.provider"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
	// The authentication type used for pipeline configuration.
	PipelineAuthKey = AttributeKey{
		Key:            attribute.Key("pipeline.auth"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
)

// Infrastructure command related fields
var (
	// The IaC provider used for infrastructure generation.
	//
	// Example: "bicep", "terraform"
	InfraProviderKey = AttributeKey{
		Key:            attribute.Key("infra.provider"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
)

// Tool command related fields — telemetry for the `azd tool` feature
// (first-run experience and tool install/upgrade/check operations).
//
// Privacy: only built-in tool IDs (e.g. "az-cli") and version strings are
// captured. No file paths, no user-identifiable data, no raw error text.
var (
	// ToolFirstRunSkipReasonKey records why the first-run experience was
	// bypassed for this invocation.
	// Example: "env_var", "no_prompt", "ci_cd", "non_interactive",
	//          "already_completed", "config_error"
	ToolFirstRunSkipReasonKey = AttributeKey{
		Key:            attribute.Key("tool.firstrun.skip_reason"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}

	// ToolFirstRunOptInKey records whether the user accepted the first-run
	// "Would you like to check your Azure development tools?" prompt.
	ToolFirstRunOptInKey = AttributeKey{
		Key:            attribute.Key("tool.firstrun.opt_in"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}

	// ToolFirstRunToolsDetectedKey records the number of built-in tools that
	// were already installed when the first-run check ran.
	ToolFirstRunToolsDetectedKey = AttributeKey{
		Key:            attribute.Key("tool.firstrun.tools_detected"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
		IsMeasurement:  true,
	}

	// ToolFirstRunToolsOfferedKey records the number of recommended tools
	// offered to the user for installation during first-run.
	ToolFirstRunToolsOfferedKey = AttributeKey{
		Key:            attribute.Key("tool.firstrun.tools_offered"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
		IsMeasurement:  true,
	}

	// ToolFirstRunToolsSelectedKey records the number of tools the user
	// selected to install from the offered set.
	ToolFirstRunToolsSelectedKey = AttributeKey{
		Key:            attribute.Key("tool.firstrun.tools_selected"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
		IsMeasurement:  true,
	}

	// ToolFirstRunToolsSelectedNamesKey records the comma-separated list of
	// built-in tool IDs the user selected for installation during first-run.
	//
	// Example: "az-cli,vscode-bicep,github-copilot-cli"
	ToolFirstRunToolsSelectedNamesKey = AttributeKey{
		Key:            attribute.Key("tool.firstrun.tools_selected_names"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}

	// ToolFirstRunToolsDeselectedNamesKey records the comma-separated list of
	// offered built-in tool IDs the user deselected during first-run.
	ToolFirstRunToolsDeselectedNamesKey = AttributeKey{
		Key:            attribute.Key("tool.firstrun.tools_deselected_names"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}

	// ToolFirstRunOutcomeKey records the terminal state of the first-run
	// experience as a low-cardinality string enum. Mutually exclusive with
	// fields.ToolFirstRunSkipReasonKey — `skip_reason` is set only when the
	// flow never ran, while `outcome` is set only when the flow ran to a
	// terminal state.
	//
	// Values:
	//   - "completed"      — full flow succeeded (with or without installs)
	//   - "declined"       — user declined the opt-in prompt
	//   - "cancelled"      — user cancelled a prompt mid-flow (Ctrl+C / Esc)
	//   - "detect_failed"  — tool detection failed before any selection was offered
	//   - "install_failed" — the install batch itself errored at infrastructure level
	//
	// Replaces the prior `tool.firstrun.completed` bool field, which was
	// always `true` and therefore not filterable for the failure / decline
	// cases described above.
	ToolFirstRunOutcomeKey = AttributeKey{
		Key:            attribute.Key("tool.firstrun.outcome"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}

	// ToolIdKey records the built-in tool ID for single-tool operations
	// (e.g., `azd tool show`, single-target `azd tool install`).
	//
	// Example: "az-cli", "vscode-bicep", "github-copilot-cli", "azure-mcp-server"
	ToolIdKey = AttributeKey{
		Key:            attribute.Key("tool.id"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}

	// ToolIdsKey records the comma-separated list of built-in tool IDs
	// targeted by a batch operation (e.g., `azd tool install az-cli vscode-bicep`).
	ToolIdsKey = AttributeKey{
		Key:            attribute.Key("tool.ids"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}

	// ToolDryRunKey records whether `--dry-run` was specified for an
	// `azd tool install` or `azd tool upgrade` invocation.
	ToolDryRunKey = AttributeKey{
		Key:            attribute.Key("tool.dry_run"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}

	// ToolInstallStrategyKey records the installation strategy used by a
	// single-target install/upgrade.
	//
	// Example: "winget", "brew", "apt", "npm", "manual"
	ToolInstallStrategyKey = AttributeKey{
		Key:            attribute.Key("tool.install.strategy"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}

	// ToolInstallSuccessKey records whether a single-target tool install or
	// upgrade succeeded.
	ToolInstallSuccessKey = AttributeKey{
		Key:            attribute.Key("tool.install.success"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}

	// ToolInstallSuccessCountKey records the number of tools that succeeded
	// in a batch install / upgrade.
	ToolInstallSuccessCountKey = AttributeKey{
		Key:            attribute.Key("tool.install.success_count"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
		IsMeasurement:  true,
	}

	// ToolInstallFailureCountKey records the number of tools that failed in
	// a batch install / upgrade.
	ToolInstallFailureCountKey = AttributeKey{
		Key:            attribute.Key("tool.install.failure_count"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
		IsMeasurement:  true,
	}

	// ToolInstallFailedIdsKey records the comma-separated list of built-in
	// tool IDs whose install / upgrade did not succeed.  Per-tool error
	// messages are intentionally *not* captured — only the IDs that failed
	// — so the global error middleware remains the source of error detail
	// and no PII can leak through tool-specific error strings.
	ToolInstallFailedIdsKey = AttributeKey{
		Key:            attribute.Key("tool.install.failed_ids"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}

	// ToolInstallDurationMsKey records the total duration of an install /
	// upgrade operation in milliseconds.
	ToolInstallDurationMsKey = AttributeKey{
		Key:            attribute.Key("tool.install.duration_ms"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
		IsMeasurement:  true,
	}

	// ToolFirstRunInstallSuccessCountKey mirrors ToolInstallSuccessCountKey
	// but is emitted only from the first-run middleware, so the user's
	// subsequent `azd tool install` / `azd tool upgrade` command (which
	// emits its own `tool.install.success_count`) does not overwrite the
	// first-run signal on the same span.
	ToolFirstRunInstallSuccessCountKey = AttributeKey{
		Key:            attribute.Key("tool.firstrun.install_success_count"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
		IsMeasurement:  true,
	}

	// ToolFirstRunInstallFailureCountKey — first-run counterpart of
	// ToolInstallFailureCountKey. See ToolFirstRunInstallSuccessCountKey.
	ToolFirstRunInstallFailureCountKey = AttributeKey{
		Key:            attribute.Key("tool.firstrun.install_failure_count"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
		IsMeasurement:  true,
	}

	// ToolFirstRunInstallFailedIdsKey — first-run counterpart of
	// ToolInstallFailedIdsKey. See ToolFirstRunInstallSuccessCountKey.
	ToolFirstRunInstallFailedIdsKey = AttributeKey{
		Key:            attribute.Key("tool.firstrun.install_failed_ids"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}

	// ToolFirstRunInstallDurationMsKey — first-run counterpart of
	// ToolInstallDurationMsKey. See ToolFirstRunInstallSuccessCountKey.
	ToolFirstRunInstallDurationMsKey = AttributeKey{
		Key:            attribute.Key("tool.firstrun.install_duration_ms"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
		IsMeasurement:  true,
	}

	// ToolUpgradeFromVersionKey records the previous version of a tool
	// being upgraded (single-target upgrades only).
	ToolUpgradeFromVersionKey = AttributeKey{
		Key:            attribute.Key("tool.upgrade.from_version"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}

	// ToolUpgradeToVersionKey records the new version after upgrade
	// (single-target upgrades only). Emitted only when the upgrade
	// succeeds.
	ToolUpgradeToVersionKey = AttributeKey{
		Key:            attribute.Key("tool.upgrade.to_version"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}

	// ToolCheckUpdatesAvailableKey records the number of installed tools
	// that have an available update, as reported by `azd tool check`.
	ToolCheckUpdatesAvailableKey = AttributeKey{
		Key:            attribute.Key("tool.check.updates_available"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
		IsMeasurement:  true,
	}
)

// Provision validation related fields
var (
	// ProvisionValidationOutcomeKey records the outcome of provision validation.
	//
	// Example: "passed", "warnings_accepted", "canceled_by_errors",
	//          "canceled_by_user", "skipped", "error"
	ProvisionValidationOutcomeKey = AttributeKey{
		Key:            attribute.Key("validation.provision.outcome"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}

	// ProvisionValidationDiagnosticsKey records the list of diagnostic IDs emitted by provision
	// validation checks.
	//
	// Example: ["role_assignment_missing", "role_assignment_conditional"]
	ProvisionValidationDiagnosticsKey = AttributeKey{
		Key:            attribute.Key("validation.provision.diagnostics"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}

	// ProvisionValidationRulesKey records the list of rule IDs that were executed.
	//
	// Example: ["role_assignment_permissions"]
	ProvisionValidationRulesKey = AttributeKey{
		Key:            attribute.Key("validation.provision.rules"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}

	// ProvisionValidationWarningCountKey records the number of warnings produced by provision
	// validation.
	ProvisionValidationWarningCountKey = AttributeKey{
		Key:            attribute.Key("validation.provision.warning.count"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
		IsMeasurement:  true,
	}

	// ProvisionValidationErrorCountKey records the number of errors produced by provision validation.
	ProvisionValidationErrorCountKey = AttributeKey{
		Key:            attribute.Key("validation.provision.error.count"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
		IsMeasurement:  true,
	}

	// ProvisionValidationExtensionRulesKey records the list of rule IDs from extension-provided
	// validation checks that were executed. Separate from ProvisionValidationRulesKey (core rules)
	// to distinguish the source of checks in telemetry.
	//
	// Example: ["todo_resource_name", "naming_convention"]
	ProvisionValidationExtensionRulesKey = AttributeKey{
		Key:            attribute.Key("validation.provision.extension_rules"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}

	// ProvisionValidationCheckTypeKey records which validation dispatch site emitted the
	// event, since the same ProvisionValidationEvent is emitted from both the
	// Bicep-only "arm-provision" dispatch and the provider-agnostic
	// "provision" dispatch. Without it, downstream consumers would double-count
	// the event for Bicep provisions (where both sites fire). Values are the
	// fixed, code-defined check-type identifiers.
	//
	// Example: "arm-provision", "provision"
	ProvisionValidationCheckTypeKey = AttributeKey{
		Key:            attribute.Key("validation.provision.check_type"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
)

// Provision-related fields
var (
	// ProvisionCancellationKey records how a Ctrl+C interrupt during
	// `azd provision` / `azd up` was handled.
	//
	// Example: "none" (no interrupt observed), "leave_running" (user chose to
	// keep the Azure deployment running), "canceled" (Azure confirmed the
	// deployment reached the Canceled state), "cancel_timed_out" (cancel was
	// submitted but azd stopped waiting for the top-level terminal state),
	// "cancel_timed_out_nested" (top-level was canceled, but one or more
	// descendant deployments did not reach terminal state within the global
	// budget), "cancel_raced_succeeded" / "cancel_raced_failed" /
	// "cancel_raced_deleted" (Azure reached the corresponding terminal state
	// before the cancel took effect — split from the legacy "cancel_too_late"
	// so dashboards can answer "how often does cancel race a *successful*
	// deployment?"), "cancel_too_late" (fallback for unexpected terminal
	// states), "cancel_failed" (the cancel request itself returned an error).
	ProvisionCancellationKey = AttributeKey{
		Key:            attribute.Key("provision.cancellation"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
)

// Execution graph scheduler related fields
var (
	// ExeGraphStepCountKey records the total number of steps in the graph.
	ExeGraphStepCountKey = AttributeKey{
		Key:            attribute.Key("exegraph.step.count"),
		Classification: SystemMetadata,
		Purpose:        PerformanceAndHealth,
		IsMeasurement:  true,
	}

	// ExeGraphMaxConcurrencyKey records the effective concurrency limit used.
	ExeGraphMaxConcurrencyKey = AttributeKey{
		Key:            attribute.Key("exegraph.max_concurrency"),
		Classification: SystemMetadata,
		Purpose:        PerformanceAndHealth,
	}

	// ExeGraphErrorPolicyKey records the error policy (fail_fast or continue_on_error).
	ExeGraphErrorPolicyKey = AttributeKey{
		Key:            attribute.Key("exegraph.error_policy"),
		Classification: SystemMetadata,
		Purpose:        PerformanceAndHealth,
	}

	// ExeGraphStepNameKey records the step name within an exegraph.step span.
	// Hashed: step names embed user-chosen service or layer names from
	// azure.yaml (e.g., "deploy-<svc.Name>", "<layer.Name>"); emit via
	// fields.StringHashed.
	ExeGraphStepNameKey = AttributeKey{
		Key:            attribute.Key("exegraph.step.name"),
		Classification: SystemMetadata,
		Purpose:        PerformanceAndHealth,
	}

	// ExeGraphStepDepsKey records the dependency list for a step.
	// Hashed: each entry is another step name and therefore embeds user-chosen
	// identifiers; emit via fields.StringSliceHashed.
	ExeGraphStepDepsKey = AttributeKey{
		Key:            attribute.Key("exegraph.step.deps"),
		Classification: SystemMetadata,
		Purpose:        PerformanceAndHealth,
	}

	// ExeGraphStepTagsKey records the tags for a step. Tags are a fixed
	// internal vocabulary (e.g., "provision", "deploy", "package", "cmdhook",
	// "event") set by azd code; they do not contain user input and are
	// emitted raw.
	ExeGraphStepTagsKey = AttributeKey{
		Key:            attribute.Key("exegraph.step.tags"),
		Classification: SystemMetadata,
		Purpose:        PerformanceAndHealth,
	}

	// ExeGraphStepTimeoutKey records the per-step timeout if set (in seconds).
	ExeGraphStepTimeoutKey = AttributeKey{
		Key:            attribute.Key("exegraph.step.timeout_s"),
		Classification: SystemMetadata,
		Purpose:        PerformanceAndHealth,
		IsMeasurement:  true,
	}
)

// Multi-layer provision related fields. These power telemetry that lets the
// azd team measure adoption and safety of `infra.layers[]` parallel
// provisioning — answering questions like "what fraction of projects use
// multi-layer?", "how parallel is the typical project?", and "how often
// does the safe-by-default fallback engage on real templates?".
var (
	// ProvisionLayerCountKey records the total number of `infra.layers[]`
	// declared in `azure.yaml` for the current `azd provision`/`azd up` run.
	// 0 or 1 means single-layer (the legacy path).
	ProvisionLayerCountKey = AttributeKey{
		Key:            attribute.Key("provision.layer.count"),
		Classification: SystemMetadata,
		Purpose:        PerformanceAndHealth,
		IsMeasurement:  true,
	}

	// ProvisionLayerMaxParallelKey records the largest number of layers
	// scheduled in a single dependency level after static analysis. This
	// is the maximum *achievable* parallelism for the run — different from
	// `exegraph.max_concurrency`, which is the configured cap.
	ProvisionLayerMaxParallelKey = AttributeKey{
		Key:            attribute.Key("provision.layer.max_parallel"),
		Classification: SystemMetadata,
		Purpose:        PerformanceAndHealth,
		IsMeasurement:  true,
	}

	// ProvisionLayerSafeFallbackCountKey records how many layers triggered
	// the safe-by-default detector fallback (forced to depend on all
	// earlier layers because the static analyzer encountered a syntax
	// pattern it could not resolve to a literal env-var name). A non-zero
	// value here means that layer's parallelism opportunity was sacrificed
	// for correctness — useful for sizing future detector improvements.
	ProvisionLayerSafeFallbackCountKey = AttributeKey{
		Key:            attribute.Key("provision.layer.safe_fallback_count"),
		Classification: SystemMetadata,
		Purpose:        PerformanceAndHealth,
		IsMeasurement:  true,
	}

	// ProvisionLayerExplicitDependsOnCountKey records how many layers used
	// the explicit `infra.layers[].dependsOn` schema (the documented
	// escape hatch for hook-mediated edges that no static analyzer can
	// infer). Adoption of this field signals that authors are reaching
	// for the explicit override.
	ProvisionLayerExplicitDependsOnCountKey = AttributeKey{
		Key:            attribute.Key("provision.layer.explicit_dependson_count"),
		Classification: SystemMetadata,
		Purpose:        PerformanceAndHealth,
		IsMeasurement:  true,
	}
)

// The value used for ServiceNameKey
const ServiceNameAzd = "azd"

// Error related fields
var (
	// Error category that classifies an error.
	ErrCategory = AttributeKey{
		Key:            attribute.Key("error.category"),
		Classification: SystemMetadata,
		Purpose:        PerformanceAndHealth,
	}

	// Error code that describes an error.
	ErrCode = AttributeKey{
		Key:            attribute.Key("error.code"),
		Classification: SystemMetadata,
		Purpose:        PerformanceAndHealth,
	}

	// Error type.
	ErrType = AttributeKey{
		Key:            attribute.Key("error.type"),
		Classification: SystemMetadata,
		Purpose:        PerformanceAndHealth,
	}

	// ErrChainTypes records the wrapped-error type chain (outermost
	// first). Type names are code-defined and PII-free, so they're
	// emitted as system metadata for triaging the catch-all bucket.
	ErrChainTypes = AttributeKey{
		Key:            attribute.Key("error.chain.types"),
		Classification: SystemMetadata,
		Purpose:        PerformanceAndHealth,
	}
)

// Service related fields.
var (
	// Hostname of the service.
	// The list of allowed values can be found in [Domains].
	ServiceHost = AttributeKey{
		Key:            attribute.Key("service.host"),
		Classification: SystemMetadata,
		Purpose:        PerformanceAndHealth,
	}

	// Name of the service.
	ServiceName = AttributeKey{
		Key:            attribute.Key("service.name"),
		Classification: SystemMetadata,
		Purpose:        PerformanceAndHealth,
	}

	// Status code of a response returned by the service.
	// For HTTP, this corresponds to the HTTP status code.
	ServiceStatusCode = AttributeKey{
		Key:            attribute.Key("service.statusCode"),
		Classification: SystemMetadata,
		Purpose:        PerformanceAndHealth,
		IsMeasurement:  true,
	}

	// Method of a request to the service.
	// For HTTP, this corresponds to the HTTP method of the request made.
	ServiceMethod = AttributeKey{
		Key:            attribute.Key("service.method"),
		Classification: SystemMetadata,
		Purpose:        PerformanceAndHealth,
	}

	// An error code returned by the service in a response.
	// For HTTP, the error code can be found in the response header or body.
	ServiceErrorCode = AttributeKey{
		Key:            attribute.Key("service.errorCode"),
		Classification: SystemMetadata,
		Purpose:        PerformanceAndHealth,
		IsMeasurement:  true,
	}

	// Correlation ID for a request to the service.
	ServiceCorrelationId = AttributeKey{
		Key:            attribute.Key("service.correlationId"),
		Classification: SystemMetadata,
		Purpose:        PerformanceAndHealth,
	}
)

// Tool related fields
var (
	// The name of the tool.
	ToolName = AttributeKey{
		Key:            attribute.Key("tool.name"),
		Classification: SystemMetadata,
		Purpose:        PerformanceAndHealth,
	}

	// The exit code of the tool after invocation.
	ToolExitCode = AttributeKey{
		Key:            attribute.Key("tool.exitCode"),
		Classification: SystemMetadata,
		Purpose:        PerformanceAndHealth,
	}
)

// Performance related fields
var (
	// The time spent waiting on user interaction in milliseconds.
	PerfInteractTime = AttributeKey{
		Key:            attribute.Key("perf.interact_time"),
		Classification: SystemMetadata,
		Purpose:        PerformanceAndHealth,
		IsMeasurement:  true,
	}

	// PerfProvisionDurationMs is the wall-clock provisioning phase duration in milliseconds.
	// Measured from the earliest provision step start to the latest provision step end.
	PerfProvisionDurationMs = AttributeKey{
		Key:            attribute.Key("perf.provision_duration_ms"),
		Classification: SystemMetadata,
		Purpose:        PerformanceAndHealth,
		IsMeasurement:  true,
	}

	// PerfDeployDurationMs is the wall-clock deploying phase duration in milliseconds.
	// Measured from the earliest deploy step start to the latest deploy step end.
	// Package and publish steps are excluded (they run concurrently with provisioning).
	PerfDeployDurationMs = AttributeKey{
		Key:            attribute.Key("perf.deploy_duration_ms"),
		Classification: SystemMetadata,
		Purpose:        PerformanceAndHealth,
		IsMeasurement:  true,
	}

	// PerfTotalDurationMs is the total wall-clock duration for the entire up-graph execution.
	PerfTotalDurationMs = AttributeKey{
		Key:            attribute.Key("perf.total_duration_ms"),
		Classification: SystemMetadata,
		Purpose:        PerformanceAndHealth,
		IsMeasurement:  true,
	}
)

// Pack related fields
var (
	// The builder image used. Hashed when a user-defined image is used.
	PackBuilderImage = AttributeKey{
		Key:            attribute.Key("pack.builder.image"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}

	// The tag of the builder image used. Hashed when a user-defined image is used.
	PackBuilderTag = AttributeKey{
		Key:            attribute.Key("pack.builder.tag"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
)

// Mcp related fields
var (
	// The name of the MCP client.
	McpClientName = AttributeKey{
		Key:            attribute.Key("mcp.client.name"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}

	// The version of the MCP client.
	McpClientVersion = AttributeKey{
		Key:            attribute.Key("mcp.client.version"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
)

// Initialization from app related fields
var (
	InitMethod = AttributeKey{
		Key:            attribute.Key("init.method"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}

	AppInitDetectedDatabase = AttributeKey{
		Key:            attribute.Key("appinit.detected.databases"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
	AppInitDetectedServices = AttributeKey{
		Key:            attribute.Key("appinit.detected.services"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}

	AppInitConfirmedDatabases = AttributeKey{
		Key:            attribute.Key("appinit.confirmed.databases"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
	AppInitConfirmedServices = AttributeKey{
		Key:            attribute.Key("appinit.confirmed.services"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}

	AppInitModifyAddCount = AttributeKey{
		Key:            attribute.Key("appinit.modify_add.count"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
		IsMeasurement:  true,
	}
	AppInitModifyRemoveCount = AttributeKey{
		Key:            attribute.Key("appinit.modify_remove.count"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
		IsMeasurement:  true,
	}

	// The last step recorded during the app init process.
	AppInitLastStep = AttributeKey{
		Key:            attribute.Key("appinit.lastStep"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
)

// Remote docker build related fields
var (
	RemoteBuildCount = AttributeKey{
		Key:            attribute.Key("container.remoteBuild.count"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
		IsMeasurement:  true,
	}
)

// JSON-RPC related fields
var (
	// Logical name of the method from the RPC interface
	// perspective, which can be different from the name of any implementing
	// method/function. See semconv.RPCMethodKey.
	RpcMethod = AttributeKey{
		Key:            semconv.RPCMethodKey,
		Classification: SystemMetadata,
		Purpose:        PerformanceAndHealth,
	}

	// `id` property of JSON-RPC request or response.
	JsonRpcId = AttributeKey{
		Key:            semconv.JSONRPCRequestIDKey,
		Classification: SystemMetadata,
		Purpose:        PerformanceAndHealth,
	}

	// `error_code` property of JSON-RPC request or response. Type: int.
	JsonRpcErrorCode = AttributeKey{
		Key:            attribute.Key("rpc.jsonrpc.error_code"),
		Classification: SystemMetadata,
		Purpose:        PerformanceAndHealth,
		IsMeasurement:  true,
	}
)

// Agent-troubleshooting related fields
var (
	// Number of auto-fix.attempts
	AgentFixAttempts = AttributeKey{
		Key:            attribute.Key("agent.fix.attempts"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
)

// Extension related fields
var (
	// The identifier of the extension.
	ExtensionId = AttributeKey{
		Key:            attribute.Key("extension.id"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
	// The version of the extension.
	ExtensionVersion = AttributeKey{
		Key:            attribute.Key("extension.version"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
	// The list of installed extensions, each formatted as "id@version".
	ExtensionsInstalled = AttributeKey{
		Key:            attribute.Key("extension.installed"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
	// ExtensionVersionFrom is the installed version before an upgrade.
	ExtensionVersionFrom = AttributeKey{
		Key:            attribute.Key("extension.version.from"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
	// ExtensionVersionTo is the target version after an upgrade.
	ExtensionVersionTo = AttributeKey{
		Key:            attribute.Key("extension.version.to"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
	// ExtensionSource is the registry source used for the upgrade.
	ExtensionSource = AttributeKey{
		Key:            attribute.Key("extension.source"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
	// ExtensionSourceKind is the kind of --source argument: none, registered, or location.
	ExtensionSourceKind = AttributeKey{
		Key:            attribute.Key("extension.source.kind"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
	// ExtensionSourceFrom is the registry source before a promotion.
	ExtensionSourceFrom = AttributeKey{
		Key:            attribute.Key("extension.source.from"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
	// ExtensionSourceTo is the registry source after a promotion.
	ExtensionSourceTo = AttributeKey{
		Key:            attribute.Key("extension.source.to"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
	// ExtensionUpgradeDurationMs is the time in milliseconds for one upgrade.
	ExtensionUpgradeDurationMs = AttributeKey{
		Key:            attribute.Key("extension.upgrade.duration_ms"),
		Classification: SystemMetadata,
		Purpose:        PerformanceAndHealth,
		IsMeasurement:  true,
	}
	// ExtensionUpgradeOutcome is the upgrade result status.
	ExtensionUpgradeOutcome = AttributeKey{
		Key:            attribute.Key("extension.upgrade.outcome"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
	// ExtensionDependencyOf is the parent extension for a dependency upgrade.
	ExtensionDependencyOf = AttributeKey{
		Key:            attribute.Key("extension.dependency_of"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
	// ExtensionDependencyUpgradeCount is the recursive dependency upgrade count.
	ExtensionDependencyUpgradeCount = AttributeKey{
		Key:            attribute.Key("extension.dependency_upgrade_count"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
		IsMeasurement:  true,
	}
)

// Update related fields
var (
	// UpdateChannel is the update channel (stable, daily).
	UpdateChannel = AttributeKey{
		Key:            attribute.Key("update.channel"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
	// UpdateInstallMethod is the install method (brew, winget, choco, script, etc.).
	UpdateInstallMethod = AttributeKey{
		Key:            attribute.Key("update.installMethod"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
	// UpdateFromVersion is the version before the update.
	UpdateFromVersion = AttributeKey{
		Key:            attribute.Key("update.fromVersion"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
	// UpdateToVersion is the target version for the update.
	UpdateToVersion = AttributeKey{
		Key:            attribute.Key("update.toVersion"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
	// UpdateResult is the outcome of the update operation.
	UpdateResult = AttributeKey{
		Key:            attribute.Key("update.result"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
)

// Copilot agent session related fields
var (
	// CopilotSessionId is the session ID for correlation across messages.
	CopilotSessionId = AttributeKey{
		Key:            attribute.Key("copilot.session.id"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
	// CopilotSessionIsNew indicates whether this was a new session (true) or resumed (false).
	CopilotSessionIsNew = AttributeKey{
		Key:            attribute.Key("copilot.session.isNew"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
	// CopilotSessionMessageCount is the number of messages sent in the session.
	CopilotSessionMessageCount = AttributeKey{
		Key:            attribute.Key("copilot.session.messageCount"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
		IsMeasurement:  true,
	}
)

// Copilot agent initialization related fields
var (
	// CopilotInitIsFirstRun indicates whether this was the user's first agent initialization.
	CopilotInitIsFirstRun = AttributeKey{
		Key:            attribute.Key("copilot.init.isFirstRun"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
	// CopilotInitReasoningEffort is the reasoning level selected (low/medium/high).
	CopilotInitReasoningEffort = AttributeKey{
		Key:            attribute.Key("copilot.init.reasoningEffort"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
	// CopilotInitModel is the model ID selected (empty string = default).
	CopilotInitModel = AttributeKey{
		Key:            attribute.Key("copilot.init.model"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
	// CopilotInitConsentScope is the workflow consent scope chosen (session/project/global).
	CopilotInitConsentScope = AttributeKey{
		Key:            attribute.Key("copilot.init.consentScope"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
)

// Copilot agent mode and message related fields
var (
	// CopilotMode is the agent operating mode (interactive/autopilot/plan).
	CopilotMode = AttributeKey{
		Key:            attribute.Key("copilot.mode"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
	// CopilotMessageModel is the model used for a specific message.
	CopilotMessageModel = AttributeKey{
		Key:            attribute.Key("copilot.message.model"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
	}
	// CopilotMessageInputTokens is the number of input tokens consumed per message.
	CopilotMessageInputTokens = AttributeKey{
		Key:            attribute.Key("copilot.message.inputTokens"),
		Classification: SystemMetadata,
		Purpose:        PerformanceAndHealth,
		IsMeasurement:  true,
	}
	// CopilotMessageOutputTokens is the number of output tokens consumed per message.
	CopilotMessageOutputTokens = AttributeKey{
		Key:            attribute.Key("copilot.message.outputTokens"),
		Classification: SystemMetadata,
		Purpose:        PerformanceAndHealth,
		IsMeasurement:  true,
	}
	// CopilotMessageBillingRate is the billing rate multiplier per message.
	CopilotMessageBillingRate = AttributeKey{
		Key:            attribute.Key("copilot.message.billingRate"),
		Classification: SystemMetadata,
		Purpose:        BusinessInsight,
		IsMeasurement:  true,
	}
	// CopilotMessagePremiumRequests is the number of premium requests used per message.
	CopilotMessagePremiumRequests = AttributeKey{
		Key:            attribute.Key("copilot.message.premiumRequests"),
		Classification: SystemMetadata,
		Purpose:        BusinessInsight,
		IsMeasurement:  true,
	}
	// CopilotMessageDurationMs is the API call duration in milliseconds per message.
	CopilotMessageDurationMs = AttributeKey{
		Key:            attribute.Key("copilot.message.durationMs"),
		Classification: SystemMetadata,
		Purpose:        PerformanceAndHealth,
		IsMeasurement:  true,
	}
)

// Copilot consent related fields
var (
	// CopilotConsentApprovedCount is the running count of tool calls approved during the session.
	CopilotConsentApprovedCount = AttributeKey{
		Key:            attribute.Key("copilot.consent.approvedCount"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
		IsMeasurement:  true,
	}
	// CopilotConsentDeniedCount is the running count of tool calls denied during the session.
	CopilotConsentDeniedCount = AttributeKey{
		Key:            attribute.Key("copilot.consent.deniedCount"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
		IsMeasurement:  true,
	}
)
