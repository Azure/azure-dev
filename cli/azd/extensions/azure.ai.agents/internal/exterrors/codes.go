// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exterrors

// Error codes for user cancellation.
const (
	CodeCancelled = "cancelled"
)

// Error codes for validation errors.
//
// Use these with [Validation] or [ValidationWrap] when user input, manifests,
// or configuration values fail validation.
const (
	CodeInvalidAgentManifest      = "invalid_agent_manifest"
	CodeInvalidManifestPointer    = "invalid_manifest_pointer"
	CodeInvalidProjectResourceId  = "invalid_project_resource_id"
	CodeInvalidFoundryResourceId  = "invalid_foundry_resource_id"
	CodeInvalidAiProjectId        = "invalid_ai_project_id"
	CodeInvalidServiceConfig      = "invalid_service_config"
	CodeInvalidAgentRequest       = "invalid_agent_request"
	CodeUnsupportedHost           = "unsupported_host"
	CodeUnsupportedAgentKind      = "unsupported_agent_kind"
	CodeMissingAgentKind          = "missing_agent_kind"
	CodeAgentDefinitionNotFound   = "agent_definition_not_found"
	CodeSubscriptionMismatch      = "subscription_mismatch"
	CodeLocationMismatch          = "location_mismatch"
	CodeTenantMismatch            = "tenant_mismatch"
	CodeMissingPublishedContainer = "missing_published_container_artifact"
	CodeScaffoldTemplateFailed    = "scaffold_template_failed"
	CodeModelDeploymentNotFound   = "model_deployment_not_found"
)

// Error codes for dependency errors.
//
// Use these with [Dependency] or [DependencyWrap] when required external
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
	CodePromptFailed              = "prompt_failed"
)

// Error codes for auth errors.
//
// Use these with [Auth] or [AuthWrap] for authentication/authorization failures.
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
// Use these with [Compatibility] or [CompatibilityWrap] for version mismatches.
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

// Error codes for internal errors.
//
// Use these with [Internal] or [InternalWrap] for unexpected failures
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
)
