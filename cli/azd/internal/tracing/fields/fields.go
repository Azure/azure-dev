// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package fields provides definitions and functions related to the definition of telemetry fields.
package fields

import (
	"github.com/microsoft/ApplicationInsights-Go/appinsights/contracts"
	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
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
	OSVersionKey = semconv.OSVersionKey // os.version

	// The CPU architecture the host system is running on.
	HostArchKey = semconv.HostArchKey // host.arch

	// The version of the runtime of this process, as returned by the runtime without
	// modification.
	ProcessRuntimeVersionKey = semconv.ProcessRuntimeVersionKey // process.runtime.version

	// A unique ID associated to the machine the application is installed on.
	//
	// This shares implementation with VSCode's machineId and can match exactly on a given device, although there are no
	// guarantees.
	MachineIdKey = AttributeKey{
		Key:            attribute.Key("machine.id"),
		Classification: EndUserPseudonymizedInformation,
		Purpose:        BusinessInsight,
	}

	// The unique DevDeviceId associated with the device.
	DevDeviceIdKey = AttributeKey{
		Key:            attribute.Key("machine.devdeviceid"),
		Classification: EndUserPseudonymizedInformation,
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
	// Tracks what alpha features are enabled on each command
	AlphaFeaturesKey = AttributeKey{
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

	EnvModifierAzureSpace = "Azure App Spaces Portal"
)

// All possible enumerations of AccountTypeKey
const (
	// A user.
	AccountTypeUser = "User"
	// A service principal, typically an application.
	AccountTypeServicePrincipal = "Service Principal"
)

// The value used for ServiceNameKey
const ServiceNameAzd = "azd"

// Error related fields
var (
	// Error code that describes an error.
	ErrCode = AttributeKey{
		Key:            attribute.Key("error.code"),
		Classification: SystemMetadata,
		Purpose:        PerformanceAndHealth,
	}

	// Inner error.
	ErrInner = AttributeKey{
		Key:            attribute.Key("error.inner"),
		Classification: SystemMetadata,
		Purpose:        PerformanceAndHealth,
	}

	// The frame of the error.
	ErrFrame = AttributeKey{
		Key:            attribute.Key("error.frame"),
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
	}
	AppInitModifyRemoveCount = AttributeKey{
		Key:            attribute.Key("appinit.modify_remove.count"),
		Classification: SystemMetadata,
		Purpose:        FeatureInsight,
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
	}
)

// JSON-RPC related fields
const (
	// Logical name of the method from the RPC interface
	// perspective, which can be different from the name of any implementing
	// method/function. See semconv.RPCMethodKey.
	RpcMethod = semconv.RPCMethodKey

	// `id` property of JSON-RPC request or response.
	JsonRpcId = semconv.RPCJSONRPCRequestIDKey

	// `error_code` property of JSON-RPC request or response. Type: int.
	JsonRpcErrorCode = semconv.RPCJSONRPCErrorCodeKey
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
)
