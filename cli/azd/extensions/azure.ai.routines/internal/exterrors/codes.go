// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exterrors

// Error codes for user cancellation.
const (
	CodeCancelled = "cancelled"
)

// Error codes for validation errors.
const (
	CodeInvalidParameter      = "invalid_parameter"
	CodeConflictingArguments  = "conflicting_arguments"
	CodeInvalidRoutineManifest = "invalid_routine_manifest"
	CodeRoutineAlreadyExists  = "routine_already_exists"
)

// Error codes for dependency errors.
const (
	CodeMissingProjectEndpoint = "missing_project_endpoint"
	CodeFileNotFound           = "file_not_found"
)

// Error codes for auth errors.
const (
	//nolint:gosec // error code identifier, not a credential
	CodeNotLoggedIn  = "not_logged_in"
	CodeLoginExpired = "login_expired"
	CodeAuthFailed   = "auth_failed"
)

// Operation names for ServiceFromAzure errors.
// These are prefixed to the Azure error code (e.g., "get_routine.NotFound").
const (
	OpGetRoutine      = "get_routine"
	OpListRoutines    = "list_routines"
	OpCreateRoutine   = "create_routine"
	OpUpdateRoutine   = "update_routine"
	OpDeleteRoutine   = "delete_routine"
	OpEnableRoutine   = "enable_routine"
	OpDisableRoutine  = "disable_routine"
	OpDispatchRoutine = "dispatch_routine"
	OpListRoutineRuns = "list_routine_runs"
)
