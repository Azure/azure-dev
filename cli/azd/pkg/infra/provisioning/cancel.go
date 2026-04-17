// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import "errors"

// Cancellation sentinels surfaced by providers when the user interrupts a
// running deployment with Ctrl+C. These are typed errors so the action /
// error middleware can produce a friendly, non-zero exit (with the portal URL
// and a clear message) instead of treating the case as an unexpected failure.
var (
	// ErrDeploymentInterruptedLeaveRunning is returned when the user chose to
	// stop azd but allow the in-flight Azure deployment to continue running.
	ErrDeploymentInterruptedLeaveRunning = errors.New(
		"azd was interrupted; the Azure deployment is still running")

	// ErrDeploymentCanceledByUser is returned when the user requested
	// cancellation and Azure confirmed the deployment reached the Canceled
	// terminal state.
	ErrDeploymentCanceledByUser = errors.New(
		"deployment was canceled by user request")

	// ErrDeploymentCancelTimeout is returned when azd asked Azure to cancel the
	// deployment but the deployment had not reached a terminal state before
	// the local wait budget expired. The cancellation is still in progress on
	// Azure.
	ErrDeploymentCancelTimeout = errors.New(
		"deployment cancel request was submitted but did not complete before timeout")

	// ErrDeploymentCancelTooLate is returned when azd attempted to cancel the
	// deployment but Azure had already moved it to a terminal state
	// (Succeeded or Failed) before the cancel request could take effect.
	ErrDeploymentCancelTooLate = errors.New(
		"deployment finished before the cancel request could take effect")
)
