// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exterrors

// Error codes for user cancellation.
const (
	CodeCancelled = "cancelled"
)

// Error codes for validation errors.
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
	CodeMissingPublishedContainer = "missing_published_container_artifact"
	CodeScaffoldTemplateFailed    = "scaffold_template_failed"
)

// Error codes for dependency errors.
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
)

// Error codes for auth errors.
const (
	CodeCredentialCreationFailed = "credential_creation_failed"
	CodeTenantLookupFailed       = "tenant_lookup_failed"
)

// Error codes for compatibility errors.
const (
	CodeIncompatibleAzdVersion = "incompatible_azd_version"
)

// Error codes for azd host AI service errors.
const (
	CodeModelCatalogFailed    = "model_catalog_failed"
	CodeModelResolutionFailed = "model_resolution_failed"
)

// Error codes for internal errors.
const (
	CodeAzdClientFailed               = "azd_client_failed"
	CodeCognitiveServicesClientFailed = "cognitiveservices_client_failed"
	CodeContainerStartFailed          = "container_start_failed"
	CodeContainerStartTimeout         = "container_start_timeout"
)

// Operation names for ServiceFromAzure errors.
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
