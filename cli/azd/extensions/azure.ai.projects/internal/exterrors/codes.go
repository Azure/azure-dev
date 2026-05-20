// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exterrors

// Error codes commonly used for validation errors.
//
// These are paired with [Validation] when user input or configuration values
// fail validation.
const (
	CodeInvalidParameter = "invalid_parameter"
)

// Error codes commonly used for dependency errors.
//
// These are paired with [Dependency] when a required external value is missing.
const (
	CodeMissingProjectEndpoint = "missing_project_endpoint"
	CodeAzdClientFailed        = "azd_client_failed"
)
