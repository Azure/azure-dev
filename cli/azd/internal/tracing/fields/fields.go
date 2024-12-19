// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package fields provides definitions and functions related to the definition of telemetry fields.
package fields

import (
	"github.com/microsoft/ApplicationInsights-Go/appinsights/contracts"
	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.10.0"
)

// Application-level fields. Guaranteed to be set and available for all events.
const (
	// Application name. Value is always "azd".
	ServiceNameKey = semconv.ServiceNameKey // service.name
	// Application version.
	ServiceVersionKey = semconv.ServiceVersionKey // service.version

	// The operating system type.
	OSTypeKey = semconv.OSTypeKey // os.type

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
	MachineIdKey = attribute.Key("machine.id")

	// The unique DevDeviceId associated with the device.
	DevDeviceIdKey = attribute.Key("machine.devdeviceid")

	// An enumeration of possible environments that the application is running on.
	//
	// Example: Desktop, Azure Pipelines, Visual Studio.
	//
	// See EnvDesktop for complete set of values.
	ExecutionEnvironmentKey = attribute.Key("execution.environment")

	// Installer used to install the application. Set in .installed-by.txt file
	// located in the same folder as the executable.
	//
	// Example: "msi", "brew", "choco", "rpm", "deb"
	InstalledByKey = attribute.Key("service.installer")
)

// Fields related to the experimentation platform
const (
	// The assignment context as returned by the experimentation platform.
	ExpAssignmentContextKey = attribute.Key("exp.assignmentContext")
)

// Context level fields. Availability depends on the command running.
const (
	// Object ID of the principal.
	ObjectIdKey = attribute.Key(contracts.UserAuthUserId) // user_AuthenticatedId
	// Tenant ID of the principal.
	TenantIdKey = attribute.Key("ad.tenant.id")
	// The type of account. See AccountTypeUser for all possible options.
	AccountTypeKey = attribute.Key("ad.account.type")
	// Currently selected Subscription ID.
	SubscriptionIdKey = attribute.Key("ad.subscription.id")
)

// Project (azure.yaml) related attributes
const (
	// Hashed template ID metadata
	ProjectTemplateIdKey = attribute.Key("project.template.id")
	// Hashed template.version metadata
	ProjectTemplateVersionKey = attribute.Key("project.template.version")
	// Hashed project name. Could be used as an indicator for number of different azd projects.
	ProjectNameKey = attribute.Key("project.name")
	// The collection of hashed service hosts in the project.
	ProjectServiceHostsKey = attribute.Key("project.service.hosts")
	// The collection of service targets (resolved service hosts) in the project.
	ProjectServiceTargetsKey = attribute.Key("project.service.targets")
	// The collection of hashed service languages in the project.
	ProjectServiceLanguagesKey = attribute.Key("project.service.languages")
	// The service language being executed.
	ProjectServiceLanguageKey = attribute.Key("project.service.language")
)

// Platform related attributes for integrations like devcenter / ADE
const (
	PlatformTypeKey = attribute.Key("platform.type")
)

// Machine-level configuration related attribute.
const (
	// Tracks what alpha features are enabled on each command
	AlphaFeaturesKey = attribute.Key("config.features")
)

// Environment related attributes
const (
	// Hashed environment name
	EnvNameKey = attribute.Key("env.name")
)

// Command entry-point attributes
const (
	// Flags set by the user. Only parsed flag names are available. Values are not recorded.
	CmdFlags = attribute.Key("cmd.flags")
	// Number of positional arguments set.
	CmdArgsCount = attribute.Key("cmd.args.count")
	// The command invocation entrypoint.
	//
	// The command invocation is formatted using [events.GetCommandEventName]. This makes it consistent with how
	// commands are represented in telemetry.
	CmdEntry = attribute.Key("cmd.entry")
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

	EnvVisualStudio     = "Visual Studio"
	EnvVisualStudioCode = "Visual Studio Code"
	EnvCloudShell       = "Azure CloudShell"

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
const (
	// Error code that describes an error.
	ErrCode = attribute.Key("error.code")

	// Inner error.
	ErrInner = attribute.Key("error.inner")

	// The frame of the error.
	ErrFrame = attribute.Key("error.frame")
)

// Service related fields.
const (
	// Hostname of the service.
	// The list of allowed values can be found in [Domains].
	ServiceHost = attribute.Key("service.host")

	// Name of the service.
	ServiceName = attribute.Key("service.name")

	// Status code of a response returned by the service.
	// For HTTP, this corresponds to the HTTP status code.
	ServiceStatusCode = attribute.Key("service.statusCode")

	// Method of a request to the service.
	// For HTTP, this corresponds to the HTTP method of the request made.
	ServiceMethod = attribute.Key("service.method")

	// An error code returned by the service in a response.
	// For HTTP, the error code can be found in the response header or body.
	ServiceErrorCode = attribute.Key("service.errorCode")

	// Correlation ID for a request to the service.
	ServiceCorrelationId = attribute.Key("service.correlationId")
)

// Tool related fields
const (
	// The name of the tool.
	ToolName = attribute.Key("tool.name")

	// The exit code of the tool after invocation.
	ToolExitCode = attribute.Key("tool.exitCode")
)

// Performance related fields
const (
	// The time spent waiting on user interaction in milliseconds.
	PerfInteractTime = attribute.Key("perf.interact_time")
)

// Pack related fields
const (
	// The builder image used. Hashed when a user-defined image is used.
	PackBuilderImage = attribute.Key("pack.builder.image")

	// The tag of the builder image used. Hashed when a user-defined image is used.
	PackBuilderTag = attribute.Key("pack.builder.tag")
)

// Initialization from app related fields
const (
	InitMethod = attribute.Key("init.method")

	AppInitDetectedDatabase  = attribute.Key("appinit.detected.databases")
	AppInitDetectedServices  = attribute.Key("appinit.detected.services")
	AppInitDetectedAzureDeps = attribute.Key("appinit.detected.azuredeps")

	AppInitConfirmedDatabases = attribute.Key("appinit.confirmed.databases")
	AppInitConfirmedServices  = attribute.Key("appinit.confirmed.services")

	AppInitModifyAddCount    = attribute.Key("appinit.modify_add.count")
	AppInitModifyRemoveCount = attribute.Key("appinit.modify_remove.count")

	// AppInitJavaDetect indicates if java detector has started or finished
	AppInitJavaDetect = attribute.Key("appinit.java.detect")

	// The last step recorded during the app init process.
	AppInitLastStep = attribute.Key("appinit.lastStep")
)

// Remote docker build related fields
const (
	RemoteBuildCount = attribute.Key("container.remoteBuild.count")
)

// JSON-RPC related fields
const (
	// Logical name of the method from the RPC interface
	// perspective, which can be different from the name of any implementing
	// method/function. See semconv.RPCMethodKey.
	RpcMethod = semconv.RPCMethodKey

	// `id` property of JSON-RPC request or response.
	JsonRpcId = semconv.RPCJsonrpcRequestIDKey

	// `error_code` property of JSON-RPC request or response. Type: int.
	JsonRpcErrorCode = semconv.RPCJsonrpcErrorCodeKey
)
