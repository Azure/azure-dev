// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package access

import "errors"

// Kind classifies a shared error independently of any extension error representation.
type Kind string

const (
	// Configuration identifies invalid input or mismatched targets.
	Configuration Kind = "configuration"
	// Identity identifies principal, tenant, or credential resolution failures.
	Identity Kind = "identity"
	// Remote identifies verification, probing, assignment, or propagation failures.
	Remote Kind = "remote"
)

// Code is a stable shared-module error identifier.
type Code string

const (
	// InvalidOptions identifies an invalid service or probe configuration.
	InvalidOptions Code = "foundry_access_invalid_options"
	// InvalidTarget identifies a missing or invalid project binding.
	InvalidTarget Code = "foundry_access_invalid_target"
	// IdentityUnavailable identifies an unresolved principal, tenant, or credential.
	IdentityUnavailable Code = "foundry_access_identity_unavailable"
	// TargetMismatch identifies different ARM and data-plane projects.
	TargetMismatch Code = "foundry_access_target_mismatch"
	// ResolutionFailed identifies a project lookup failure.
	ResolutionFailed Code = "foundry_access_resolution_failed"
	// ProbeFailed identifies a failed or invalid non-mutating probe.
	ProbeFailed Code = "foundry_access_probe_failed"
	// GrantFailed identifies an unsuccessful role-assignment operation.
	GrantFailed Code = "foundry_access_grant_failed"
	// PropagationFailed identifies an expired propagation deadline.
	PropagationFailed Code = "foundry_access_propagation_failed"
)

// Error contains portable diagnostics for extension-side translation. Message
// deliberately omits the underlying error text, which may contain URLs or secrets.
// Unwrap preserves Azure, authentication, and context errors for classification.
type Error struct {
	Kind       Kind
	Code       Code
	Message    string
	Suggestion string
	Cause      error
}

// Error returns the safe diagnostic message, without serializing Cause.
func (e *Error) Error() string { return e.Message }

// Unwrap returns the underlying failure.
func (e *Error) Unwrap() error { return e.Cause }

func failure(code Code, message string, cause error) error {
	if existing, ok := errors.AsType[*Error](cause); ok {
		return existing
	}
	kind, suggestion := Remote, "verify access to the target project and retry"
	switch code {
	case InvalidOptions:
		kind, suggestion = Configuration, "provide a supported access policy and non-mutating probe"
	case InvalidTarget, TargetMismatch:
		kind, suggestion = Configuration, "use the ARM resource ID and endpoint of the same Foundry project"
	case IdentityUnavailable:
		kind, suggestion = Identity, "verify the signed-in identity and its subscription tenant"
	case GrantFailed:
		suggestion = "ask an administrator to grant Foundry User on the target project"
	case PropagationFailed:
		suggestion = "allow role-assignment propagation before retrying the operation"
	}
	return &Error{Kind: kind, Code: code, Message: message, Suggestion: suggestion, Cause: cause}
}
