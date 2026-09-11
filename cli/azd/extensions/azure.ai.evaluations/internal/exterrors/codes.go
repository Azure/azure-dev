// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exterrors

// The codes azd renders alongside an error's category and suggestion.
//
// Only the ones this extension actually raises are listed. This file used to
// carry the toolbox and skill vocabulary it was copied from -- 37 codes for
// resources this extension has no concept of -- which offered anyone looking
// for the right code a menu belonging to a different product.

// Error codes for user cancellation.
const (
	CodeCancelled = "cancelled"
)

// Error codes for validation failures (user input, manifests, flags).
const (
	CodeInvalidParameter = "invalid_parameter"
)

// Error codes for dependency failures (missing resources, services, env values).
const (
	CodeMissingProjectEndpoint = "missing_project_endpoint"
)

// Error codes for auth failures.
const (
	CodeLoginExpired = "login_expired"
	CodeAuthFailed   = "auth_failed"
)
