// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exterrors

// Error codes for connection validation.
const (
	CodeConflictingArguments    = "conflicting_arguments"
	CodeMissingConnectionField  = "missing_connection_field"
	CodeInvalidConnectionKind   = "invalid_connection_kind"
	CodeInvalidAuthType         = "invalid_auth_type"
	CodeInvalidFromFile         = "invalid_from_file"
	CodeMissingForceFlag        = "missing_force_flag"
	CodeConnectionAlreadyExists = "connection_already_exists"
)

// Error codes for endpoint resolution.
const (
	CodeMissingProjectEndpoint = "missing_project_endpoint"
)

// Error codes for auth.
const (
	//nolint:gosec // error code identifier, not a credential
	CodeCredentialCreationFailed = "credential_creation_failed"
)

// Operation names for ServiceFromAzure errors.
const (
	OpCreateConnection         = "create_connection"
	OpUpdateConnection         = "update_connection"
	OpDeleteConnection         = "delete_connection"
	OpGetConnection            = "get_connection"
	OpGetConnectionCredentials = "get_connection_credentials"
	OpListConnections          = "list_connections"
)
