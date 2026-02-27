// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package update

import "fmt"

// UpdateError represents a typed update error with a result code for telemetry.
type UpdateError struct {
	// Code is the telemetry result code (e.g. "update.downloadFailed").
	Code string
	// Err is the underlying error.
	Err error
}

func (e *UpdateError) Error() string {
	return e.Err.Error()
}

func (e *UpdateError) Unwrap() error {
	return e.Err
}

// Result codes matching the design doc.
const (
	CodeSuccess                  = "update.success"
	CodeAlreadyUpToDate          = "update.alreadyUpToDate"
	CodeDownloadFailed           = "update.downloadFailed"
	CodeReplaceFailed            = "update.replaceFailed"
	CodeElevationFailed          = "update.elevationFailed"
	CodePackageManagerFailed     = "update.packageManagerFailed"
	CodeVersionCheckFailed       = "update.versionCheckFailed"
	CodeChannelSwitchDecline     = "update.channelSwitchDowngrade"
	CodeSkippedCI                = "update.skippedCI"
	CodeSignatureInvalid         = "update.signatureInvalid"
	CodeElevationRequired        = "update.elevationRequired"
	CodeUnsupportedInstallMethod = "update.unsupportedInstallMethod"
)

func newUpdateError(code string, err error) *UpdateError {
	return &UpdateError{Code: code, Err: err}
}

func newUpdateErrorf(code, format string, args ...any) *UpdateError {
	return &UpdateError{Code: code, Err: fmt.Errorf(format, args...)}
}
