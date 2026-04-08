// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exterrors

// Error codes for user cancellation.
const (
	CodeCancelled = "cancelled"
)

// Error codes commonly used for validation errors.
//
// These are usually paired with [Validation] when user input, manifests,
// or configuration values fail validation.
const (
	CodeInvalidAgentManifest      = "invalid_agent_manifest"
	CodeInvalidManifestPointer    = "invalid_manifest_pointer"
	CodeInvalidProjectResourceId  = "invalid_project_resource_id"
	CodeInvalidFoundryResourceId  = "invalid_foundry_resource_id"
	CodeInvalidAiProjectId        = "invalid_ai_project_id"
	CodeInvalidServiceConfig      = "invalid_service_config"
	CodeInvalidAgentRequest       = "invalid_agent_request"
	CodeInvalidAgentName          = "invalid_agent_name"
	CodeInvalidAgentVersion       = "invalid_agent_version"
	CodeInvalidSessionId          = "invalid_session_id"
	CodeInvalidParameter          = "invalid_parameter"
	CodeUnsupportedHost           = "unsupported_host"
	CodeUnsupportedAgentKind      = "unsupported_agent_kind"
	CodeMissingAgentKind          = "missing_agent_kind"
	CodeAgentDefinitionNotFound   = "agent_definition_not_found"
	CodeSubscriptionMismatch      = "subscription_mismatch"
	CodeLocationMismatch          = "location_mismatch"
	CodeTenantMismatch            = "tenant_mismatch"
	CodeMissingPublishedContainer = "missing_published_container_artifact"
	CodeModelDeploymentNotFound   = "model_deployment_not_found"
)

// Error codes commonly used for dependency errors.
//
// These are usually paired with [Dependency] when required external
// resources, services, or environment values are missing or unavailable.
const (
	CodeProjectNotFound           = "project_not_found"
	CodeProjectInitFailed         = "project_init_failed"
	CodeEnvironmentNotFound       = "environment_not_found"
	CodeEnvironmentCreationFailed = "environment_creation_failed"
	CodeEnvironmentValuesFailed   = "environment_values_failed"
	CodeMissingAiProjectEndpoint  = "missing_ai_project_endpoint"
	CodeMissingAiProjectId        = "missing_ai_project_id"
	CodeMissingAzureSubscription  = "missing_azure_subscription_id"
	CodeMissingAgentEnvVars       = "missing_agent_env_vars"
	CodeGitHubDownloadFailed      = "github_download_failed"
	CodeScaffoldTemplateFailed    = "scaffold_template_failed"
	CodePromptFailed              = "prompt_failed"
)

// Error codes commonly used for auth errors.
//
// These are usually paired with [Auth] for authentication/authorization failures.
const (
	//nolint:gosec // error code identifier, not a credential
	CodeCredentialCreationFailed = "credential_creation_failed"
	CodeTenantLookupFailed       = "tenant_lookup_failed"
	CodeNotLoggedIn              = "not_logged_in"
	CodeLoginExpired             = "login_expired"
	CodeAuthFailed               = "auth_failed"
)

// Error codes for compatibility errors.
//
// These are usually paired with [Compatibility] for version mismatches.
const (
	CodeIncompatibleAzdVersion = "incompatible_azd_version"
)

// Error codes for azd host AI service errors.
//
// Used as fallback codes with [FromAiService] when the gRPC response
// doesn't include a more specific ErrorInfo reason.
const (
	CodeModelCatalogFailed    = "model_catalog_failed"
	CodeModelResolutionFailed = "model_resolution_failed"
)

// Error codes for session errors.
const (
	CodeSessionNotFound = "session_not_found"
)

// Error codes for file operation errors.
const (
	CodeFileNotFound     = "file_not_found"
	CodeFileUploadFailed = "file_upload_failed"
	CodeInvalidFilePath  = "invalid_file_path"
)

// Error codes commonly used for internal errors.
//
// These are usually paired with [Internal] for unexpected failures
// that are not directly caused by user input.
const (
	CodeAzdClientFailed               = "azd_client_failed"
	CodeCognitiveServicesClientFailed = "cognitiveservices_client_failed"
	CodeContainerStartFailed          = "container_start_failed"
	CodeContainerStartTimeout         = "container_start_timeout"
)

// Operation names for [ServiceFromAzure] errors.
// These are prefixed to the Azure error code (e.g., "create_agent.NotFound").
const (
	OpGetFoundryProject     = "get_foundry_project"
	OpContainerBuild        = "container_build"
	OpContainerPackage      = "container_package"
	OpContainerPublish      = "container_publish"
	OpCreateAgent           = "create_agent"
	OpStartContainer        = "start_container"
	OpGetContainerOperation = "get_container_operation"
	OpCreateSession         = "create_session"
	OpGetSession            = "get_session"
	OpDeleteSession         = "delete_session"
	OpListSessions          = "list_sessions"
)
