// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exterrors

// Error codes for user cancellation.
const (
	CodeCancelled = "cancelled"
)

// Error codes commonly used for validation errors.
//
// These are paired with [Validation] when user input or configuration values
// fail validation.
const (
	CodeInvalidParameter     = "invalid_parameter"
	CodeInvalidServiceConfig = "invalid_service_config"
)

// Error codes commonly used for dependency errors.
//
// These are paired with [Dependency] when a required external value is missing.
const (
	CodeMissingProjectEndpoint   = "missing_project_endpoint"
	CodeMissingAzureSubscription = "missing_azure_subscription_id"
	CodeMissingAzureLocation     = "missing_azure_location"
	CodeEnvironmentNotFound      = "environment_not_found"
	CodeEnvironmentValuesFailed  = "environment_values_failed"
	CodeAzdClientFailed          = "azd_client_failed"
)

// Error codes commonly used for auth errors.
const (
	//nolint:gosec // error code identifier, not a credential
	CodeCredentialCreationFailed = "credential_creation_failed"
	CodeTenantLookupFailed       = "tenant_lookup_failed"
	CodeNotLoggedIn              = "not_logged_in"
	CodeLoginExpired             = "login_expired"
	CodeAuthFailed               = "auth_failed"
)

// Error codes for the microsoft.foundry provisioning provider.
const (
	CodeInvalidAzureYaml            = "invalid_azure_yaml"
	CodeProvisioningServiceNotFound = "provisioning_service_not_found"
	CodeBrownfieldNotSupported      = "brownfield_not_supported"
	CodeMissingFoundryProjectName   = "missing_foundry_project_name"
	CodeMissingResourceGroup        = "missing_resource_group"
	CodeDestroyRequiresForce        = "destroy_requires_force"
	CodeOnDiskBicepCompileFailed    = "ondisk_bicep_compile_failed"
	CodeOnDiskBicepParseFailed      = "ondisk_bicep_parse_failed"
	CodeOnDiskParametersInvalid     = "ondisk_parameters_invalid"
	CodeOnDiskTemplateMissing       = "ondisk_template_missing"
	CodeArmWhatIfFailed             = "arm_what_if_failed"
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
