// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exterrors

// Error codes for user cancellation.
const (
	CodeCancelled = "cancelled"
)

// Error codes for validation failures (user input, manifests, flags).
const (
	CodeInvalidParameter     = "invalid_parameter"
	CodeInvalidPositionalArg = "invalid_positional_arg"
)

// Error codes for dependency failures (missing resources, services, env values).
const (
	CodeAzdClientFailed        = "azd_client_failed"
	CodeMissingProjectEndpoint = "missing_project_endpoint"
)

// Error codes for auth failures.
const (
	CodeNotLoggedIn  = "not_logged_in"
	CodeLoginExpired = "login_expired"
	CodeAuthFailed   = "auth_failed"
)

// Error codes for toolbox operations.
const (
	CodeToolboxNotFound               = "toolbox_not_found"
	CodeInvalidToolboxName            = "invalid_toolbox_name"
	CodeMissingUpdateField            = "missing_update_field"
	CodeDefaultVersionDelete          = "default_version_delete"
	CodeOnlyVersionDelete             = "only_version_delete"
	CodeMissingForceFlag              = "missing_force_flag"
	CodeUnsupportedConnectionCategory = "unsupported_connection_category"
	CodeMissingIndex                  = "missing_index"
	CodeUnsupportedIndexFlag          = "unsupported_index_flag"
	CodeMissingInstanceName           = "missing_instance_name"
	CodeUnsupportedInstanceNameFlag   = "unsupported_instance_name_flag"
	CodeInvalidSkillName              = "invalid_skill_name"
	CodeInvalidSkillSpec              = "invalid_skill_spec"
	CodeDuplicateSkill                = "duplicate_skill"
	CodeSkillNotInToolbox             = "skill_not_in_toolbox"
	CodeSkillAlreadyAttached          = "skill_already_attached"
	CodeDuplicateConnection           = "duplicate_connection"
	CodeConnectionNotFound            = "connection_not_found"
	CodeConnectionNotInToolbox        = "connection_not_in_toolbox"
	CodeConnectionMissingTarget       = "connection_missing_target"
	CodeLastToolRemoval               = "last_tool_removal"
	CodePendingToolboxStoreFailed     = "pending_toolbox_store_failed"
)

// Operation names for [ServiceFromAzure] errors.
// These are prefixed to the Azure error code (e.g., "get_toolbox.NotFound").
const (
	OpCreateToolboxVersion     = "create_toolbox_version"
	OpGetToolbox               = "get_toolbox"
	OpDeleteToolbox            = "delete_toolbox"
	OpDeleteToolboxVersion     = "delete_toolbox_version"
	OpSetDefaultVersion        = "set_default_version"
	OpListToolboxes            = "list_toolboxes"
	OpGetToolboxVersion        = "get_toolbox_version"
	OpListToolboxVersions      = "list_toolbox_versions"
	OpResolveProjectConnection = "resolve_project_connection"
)
