// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package internal

import (
	"errors"

	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
)

// ErrorWithSuggestion is a type alias for errorhandler.ErrorWithSuggestion.
// The canonical type lives in pkg/errorhandler so it can be used by extensions.
type ErrorWithSuggestion = errorhandler.ErrorWithSuggestion

// ErrorWithTraceId is a custom error type that includes a trace ID for the current operation
type ErrorWithTraceId struct {
	TraceId string
	Err     error
}

// Error returns the error message
func (et *ErrorWithTraceId) Error() string {
	return et.Err.Error()
}

// Unwrap returns the wrapped error
func (et *ErrorWithTraceId) Unwrap() error {
	return et.Err
}

// Command sentinel errors for telemetry classification.
// These enable MapError to produce meaningful ResultCodes instead of falling
// into the opaque "internal.errors_errorString" catch-all bucket.

// Environment command errors
var (
	ErrNoEnvironmentsFound    = errors.New("no environments found")
	ErrKeyNotFound            = errors.New("key not found in environment values")
	ErrNoKeyNameProvided      = errors.New("no key name provided")
	ErrNoEnvValuesProvided    = errors.New("no environment values provided")
	ErrInvalidFlagCombination = errors.New("invalid flag combination")
)

// Deploy command errors
var (
	ErrInfraNotProvisioned  = errors.New("infrastructure has not been provisioned")
	ErrFromPackageWithAll   = errors.New("'--from-package' cannot be specified when '--all' is set")
	ErrFromPackageNoService = errors.New(
		"'--from-package' cannot be specified when deploying all services")
)

// Provision command errors
var (
	ErrCannotChangeSubscription = errors.New("cannot change subscription for existing environment")
	ErrCannotChangeLocation     = errors.New("cannot change location for existing environment")
	ErrPreviewMultipleLayers    = errors.New("--preview cannot be used when provisioning multiple layers")
)

// Init command errors
var (
	ErrBranchRequiresTemplate = errors.New(
		"using branch argument requires a template argument to be specified")
	ErrMultipleInitModes = errors.New(
		"only one of init modes: --template, --from-code, or --minimal should be set")
)

// Auth command errors
var (
	ErrLoginDisabledDelegatedMode = errors.New(
		"'azd auth login' is disabled when the auth mode is delegated")
)

// Cross-command sentinel errors for common error patterns.

// Argument/flag validation errors
var (
	ErrNoArgsProvided     = errors.New("required arguments not provided")
	ErrInvalidArgValue    = errors.New("invalid argument value")
	ErrOperationCancelled = errors.New("operation cancelled by user")
)

// Config errors
var (
	ErrConfigKeyNotFound = errors.New("config key not found")
)

// Extension errors
var (
	ErrExtensionNotFound     = errors.New("extension not found")
	ErrNoExtensionsAvailable = errors.New("no extensions available for operation")
	ErrExtensionTokenFailed  = errors.New("failed to generate extension token")
)

// Service/resource errors
var (
	ErrServiceNotFound       = errors.New("service not found in project")
	ErrResourceNotConfigured = errors.New("required resource not configured")
)

// Validation errors
var (
	ErrValidationFailed = errors.New("validation failed")
)

// Unsupported operation errors
var (
	ErrUnsupportedOperation = errors.New("operation not supported")
)

// MCP errors
var (
	ErrMcpToolsLoadFailed = errors.New("failed to load MCP host tools")
)
