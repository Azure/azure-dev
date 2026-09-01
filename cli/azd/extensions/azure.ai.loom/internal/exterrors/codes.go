// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exterrors

const (
	CodeInvalidParameter          = "invalid_parameter"
	CodeInvalidExperimentPayload  = "invalid_experiment_payload"
	CodeMissingProjectEndpoint    = "missing_project_endpoint"
	CodeExperimentInputReadFailed = "experiment_input_read_failed"
	CodeCredentialCreationFailed  = "credential_creation_failed" //nolint:gosec // Error code, not a credential.
	CodeAuthenticationFailed      = "authentication_failed"
	CodeExperimentRequestFailed   = "experiment_request_failed"
)

const OpExperimentRequest = "experiment_request"
