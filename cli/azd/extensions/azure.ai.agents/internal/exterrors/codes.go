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
	// CodeInvalidAgentManifest is retained while azd still reads the deprecated
	// on-disk agent manifest (agent.yaml/agent.manifest.yaml) during the
	// migration window. Rename or retire it once the on-disk manifest path is
	// removed and the agent definition is read only from azure.yaml (see the
	// unify-azure-yaml design, §2.9).
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
	CodeMissingCodeZipArtifact    = "missing_code_zip_artifact"
	CodeModelDeploymentNotFound   = "model_deployment_not_found"
	CodeConflictingArguments      = "conflicting_arguments"
	CodeInvalidPositionalArg      = "invalid_positional_arg"
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
	CodeMissingAzureTenantId      = "missing_azure_tenant_id"
	CodeMissingAiProjectId        = "missing_ai_project_id"
	CodeMissingAzureSubscription  = "missing_azure_subscription_id"
	CodeMissingAgentEnvVars       = "missing_agent_env_vars"
	CodeMissingProjectEndpoint    = "missing_project_endpoint"
	CodeGitHubDownloadFailed      = "github_download_failed"
	CodePromptFailed              = "prompt_failed"
)

// Error codes for ACR dependency errors.
const (
	CodePrivateACRNetworkAccessFailed = "private_acr_network_access_failed"
	CodeACRPermissionDenied           = "acr_permission_denied"
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
	CodeTokenProtectionBlocked   = "token_protection_blocked"
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
	CodeModelCatalogFailed        = "model_catalog_failed"
	CodeModelResolutionFailed     = "model_resolution_failed"
	CodeRegionsFetchFailed        = "regions_fetch_failed"
	CodeNoSupportedModelLocations = "no_supported_model_locations"
)

// Error codes for session errors.
const (
	CodeSessionNotFound = "session_not_found"
)

// Error codes for agent delete errors.
const (
	CodeAgentNotFound          = "agent_not_found"
	CodeAgentHasActiveSessions = "agent_has_active_sessions"
)

// Error codes for file operation errors.
const (
	CodeFileNotFound     = "file_not_found"
	CodeFileUploadFailed = "file_upload_failed"
	CodeInvalidFilePath  = "invalid_file_path"
)

// Error codes for packaging/deploy errors.
const (
	CodeBundledDepsNotFound = "bundled_deps_not_found"
)

// Error codes for $ref file-include resolution.
const (
	CodeInvalidFileRef = "invalid_file_ref"
)

// Error codes for toolbox operations.
const (
	CodeInvalidToolbox             = "invalid_toolbox"
	CodeCreateToolboxVersionFailed = "create_toolbox_version_failed"
)

// Error codes for connection operations.
const (
	CodeInvalidConnection      = "invalid_connection"
	CodeConnectionCreationFail = "connection_creation_failed"
	CodeMissingConnectionField = "missing_connection_field"
)

// Error codes for memory store operations.
const (
	CodeInvalidMemoryStore = "invalid_memory_store"
)

// Error codes for interactive agent selection.
const (
	CodeNonInteractiveAgentSelection = "non_interactive_agent_selection"
)

// Error codes for agent identity RBAC operations.
const (
	CodeAgentIdentityNotFound   = "agent_identity_not_found"
	CodeAgentIdentityRBACFailed = "agent_identity_rbac_failed"
)

// Error codes for developer RBAC pre-flight checks.
const (
	CodeDeveloperMissingAIUserRole          = "developer_missing_ai_user_role"
	CodeDeveloperMissingRoleAssignWriteRole = "developer_missing_role_assign_write_role"
	CodeDeveloperMissingACRRole             = "developer_missing_acr_role"
	CodeACRResolutionFailed                 = "acr_resolution_failed"
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
	CodeAgentCreateFailed             = "agent_create_failed"
)

// Operation names for [ServiceFromAzure] errors.
// These are prefixed to the Azure error code (e.g., "create_agent.NotFound").
const (
	OpGetFoundryProject     = "get_foundry_project"
	OpContainerBuild        = "container_build"
	OpContainerPackage      = "container_package"
	OpContainerPublish      = "container_publish"
	OpCreateAgent           = "create_agent"
	OpDeleteAgent           = "delete_agent"
	OpStartContainer        = "start_container"
	OpGetContainerOperation = "get_container_operation"
	OpCreateSession         = "create_session"
	OpGetSession            = "get_session"
	OpDeleteSession         = "delete_session"
	OpStopSession           = "stop_session"
	OpListSessions          = "list_sessions"
	OpCreateToolboxVersion  = "create_toolbox_version"
	OpGetToolbox            = "get_toolbox"
	OpProvisionMemoryStore  = "provision_memory_store"
)

// Error codes for eval and optimize operations.
const (
	CodeEvalRunFailed      = "eval_run_failed"
	CodeEvalRunCancelled   = "eval_run_cancelled"
	CodeEvalRunTimeout     = "eval_run_timeout"
	CodeEvalConfigInvalid  = "eval_config_invalid"
	CodeOptimizeJobFailed  = "optimize_job_failed"
	CodeOptimizeJobTimeout = "optimize_job_timeout"
	CodeInvalidTargetAttr  = "invalid_target_attribute"
	CodeReservedEnvVar     = "reserved_env_var"
)

// Error codes for the microsoft.foundry provisioning provider.
const (
	CodeInvalidAzureYaml            = "invalid_azure_yaml"
	CodeProvisioningServiceNotFound = "provisioning_service_not_found"
	CodeBrownfieldNotSupported      = "brownfield_not_supported"
	CodeMissingFoundryProjectName   = "missing_foundry_project_name"
	CodeMissingResourceGroup        = "missing_resource_group"
	CodeMissingAzureLocation        = "missing_azure_location"
	CodeDestroyRequiresForce        = "destroy_requires_force"
	CodeOnDiskBicepCompileFailed    = "ondisk_bicep_compile_failed"
	CodeOnDiskBicepParseFailed      = "ondisk_bicep_parse_failed"
	CodeOnDiskParametersInvalid     = "ondisk_parameters_invalid"
	CodeOnDiskTemplateMissing       = "ondisk_template_missing"
	CodeArmWhatIfFailed             = "arm_what_if_failed"
)

// Error codes for `azd ai agent init --infra` (infrastructure eject).
const (
	CodeInfraEjectExists                  = "infra_eject_exists"
	CodeInfraEjectNoFoundryService        = "infra_eject_no_foundry_service"
	CodeInfraEjectMultipleFoundryServices = "infra_eject_multiple_foundry_services"
	CodeInfraEjectAzureYamlMissing        = "infra_eject_azure_yaml_missing"
	CodeInfraEjectWriteFailed             = "infra_eject_write_failed"
	CodeInfraEjectConflictingArguments    = "infra_eject_conflicting_arguments"
	CodeInfraEjectNetworkUnsupported      = "infra_eject_network_unsupported"
	CodeInfraEjectBrownfieldUnsupported   = "infra_eject_brownfield_unsupported"
)

// Operation names for the microsoft.foundry provisioning provider.
const (
	OpArmDeploymentCreate       = "arm_deployment_create"
	OpArmDeploymentGet          = "arm_deployment_get"
	OpArmDeploymentWhatIf       = "arm_deployment_what_if"
	OpResourceGroupDelete       = "resource_group_delete"
	OpCognitiveAccountList      = "cognitive_account_list"
	OpCognitiveAccountPurge     = "cognitive_account_purge"
	OpCognitiveDeploymentList   = "cognitive_deployment_list"
	OpCognitiveDeploymentDelete = "cognitive_deployment_delete"
)
