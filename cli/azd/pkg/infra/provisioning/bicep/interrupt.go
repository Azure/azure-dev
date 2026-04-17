// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

// Default timeouts for interrupt-driven cancellation.
const (
	// cancelRequestTimeout bounds the time spent waiting for the ARM Cancel
	// API call itself to return.
	cancelRequestTimeout = 30 * time.Second
	// cancelTerminalTimeout bounds the total time we wait for the Azure
	// deployment to transition to a terminal state after the cancel request
	// has been accepted.
	cancelTerminalTimeout = 2 * time.Minute
	// cancelPollInterval controls how often we poll the deployment for state
	// changes after submitting cancel.
	cancelPollInterval = 5 * time.Second
)

// User-facing labels for the interrupt prompt. Kept as constants so tests can
// reason about the prompt selection without depending on copy edits.
const (
	interruptOptionCancel       = "Cancel the Azure deployment"
	interruptOptionLeaveRunning = "Leave the Azure deployment running and stop azd"
)

// interruptOutcome is produced by the interrupt handler and consumed by the
// main deploy goroutine after the ARM operation unblocks.
type interruptOutcome struct {
	// err is the typed sentinel error from pkg/infra/provisioning that
	// describes how the interrupt was handled.
	err error
	// telemetryValue is the value to record on the cancellation telemetry
	// attribute (see fields.ProvisionCancellationKey).
	telemetryValue string
}

// installDeploymentInterruptHandler registers a Ctrl+C handler covering the
// in-flight ARM deployment. It returns:
//
//   - deployCtx: a context derived from ctx that the caller MUST pass to the
//     ARM deploy call; it will be cancelled as soon as the user presses
//     Ctrl+C, which unblocks PollUntilDone and returns control to Deploy.
//   - startedCh: closed as soon as the user presses Ctrl+C (before the prompt
//     is shown). Callers should check it after the deploy call returns to
//     decide whether to block-wait for an interrupt outcome instead of taking
//     the normal success path. This is what guarantees that a Ctrl+C arriving
//     while the deployment happens to finish naturally cannot be silently
//     dropped.
//   - outcomeCh: receives the interrupt outcome once the user has chosen.
//     The channel is buffered (size 1).
//   - cleanup: must be called (via defer) to unregister the interrupt handler
//     and release the deploy context.
//
// onInterruptStart, if non-nil, is invoked synchronously at the start of the
// interrupt handler before any prompt is shown. Callers use this hook to stop
// background activity (e.g. the deployment progress reporter) so it doesn't
// stomp on the prompt rendering.
func (p *BicepProvider) installDeploymentInterruptHandler(
	ctx context.Context,
	deployment infra.Deployment,
	onInterruptStart func(),
) (
	deployCtx context.Context,
	startedCh <-chan struct{},
	outcomeCh <-chan interruptOutcome,
	cleanup func(),
) {
	deployCtx, cancelDeploy := context.WithCancel(ctx)
	ch := make(chan interruptOutcome, 1)
	started := make(chan struct{})

	pop := input.PushInterruptHandler(func() bool {
		// Signal interrupt-in-progress and unblock the ARM deploy call
		// immediately so Deploy can transition to "wait for outcome" mode
		// rather than racing against a natural completion.
		close(started)
		cancelDeploy()

		if onInterruptStart != nil {
			onInterruptStart()
		}
		// Stop the in-progress spinner so we can render the prompt cleanly.
		p.console.StopSpinner(ctx, "", input.Step)

		outcome := p.runInterruptPrompt(ctx, deployment)
		ch <- outcome
		// Returning true tells the runtime that we own the shutdown sequence.
		// We don't actually os.Exit here — Deploy will return the typed
		// sentinel error and the action / error middleware translates that
		// into the user-facing exit message.
		return true
	})

	cleanup = func() {
		pop()
		cancelDeploy()
	}
	return deployCtx, started, ch, cleanup
}

// runInterruptPrompt presents the user with the choice of cancelling the
// running Azure deployment or leaving it to run. It returns the outcome that
// should be propagated back to Deploy.
func (p *BicepProvider) runInterruptPrompt(
	ctx context.Context,
	deployment infra.Deployment,
) interruptOutcome {
	portalUrl, urlErr := deployment.DeploymentUrl(ctx)
	if urlErr != nil {
		// Not fatal — we just won't include the URL in the prompt.
		log.Printf("interrupt handler: failed to fetch deployment URL: %v", urlErr)
	}

	help := "An Azure deployment is currently in progress."
	if portalUrl != "" {
		help = fmt.Sprintf("%s\nPortal: %s", help, portalUrl)
	}

	choice, err := p.console.Select(ctx, input.ConsoleOptions{
		Message: "azd was interrupted. What would you like to do?",
		Help:    help,
		Options: []string{
			interruptOptionLeaveRunning,
			interruptOptionCancel,
		},
		DefaultValue: interruptOptionLeaveRunning,
	})
	if err != nil {
		// If we can't even show the prompt (e.g. non-interactive), fall back
		// to the safer "leave running" behavior so the user can decide
		// manually via the portal.
		log.Printf("interrupt handler: failed to show prompt, defaulting to leave-running: %v", err)
		return interruptOutcome{
			err:            provisioning.ErrDeploymentInterruptedLeaveRunning,
			telemetryValue: "leave_running",
		}
	}

	switch choice {
	case 0: // leave running
		if portalUrl != "" {
			p.console.Message(ctx,
				output.WithHighLightFormat("The Azure deployment will continue running. Track it here:\n  %s",
					portalUrl))
		}
		return interruptOutcome{
			err:            provisioning.ErrDeploymentInterruptedLeaveRunning,
			telemetryValue: "leave_running",
		}
	case 1: // cancel
		return p.cancelAndAwaitTerminal(ctx, deployment, portalUrl)
	default:
		// Should never happen, but fall back to leave-running.
		return interruptOutcome{
			err:            provisioning.ErrDeploymentInterruptedLeaveRunning,
			telemetryValue: "leave_running",
		}
	}
}

// cancelAndAwaitTerminal submits the Azure cancel request and polls the
// deployment until it reaches a terminal provisioning state (Canceled, Failed,
// or Succeeded) or the wait budget is exhausted.
func (p *BicepProvider) cancelAndAwaitTerminal(
	ctx context.Context,
	deployment infra.Deployment,
	portalUrl string,
) interruptOutcome {
	p.console.ShowSpinner(ctx, "Cancelling Azure deployment", input.Step)

	// Use a fresh context for the cancel API call so it isn't affected by
	// the deploy-side cancellation we issue right after.
	cancelReqCtx, cancelReqDone := context.WithTimeout(
		context.WithoutCancel(ctx), cancelRequestTimeout)
	defer cancelReqDone()

	if err := deployment.Cancel(cancelReqCtx); err != nil {
		// Some providers (e.g. Deployment Stacks) do not support per-deployment
		// cancel. Surface that as the safer "leave running" outcome rather
		// than a cancel failure so the user gets consistent UX/telemetry with
		// the documented provider behavior.
		if errors.Is(err, azapi.ErrCancelNotSupported) {
			p.console.StopSpinner(ctx, "Cancel is not supported for this deployment kind", input.StepWarning)
			if portalUrl != "" {
				p.console.Message(ctx,
					output.WithHighLightFormat(
						"The Azure deployment will continue running. Track it here:\n  %s",
						portalUrl))
			}
			return interruptOutcome{
				err:            provisioning.ErrDeploymentInterruptedLeaveRunning,
				telemetryValue: "leave_running",
			}
		}
		// If the deployment is already in a terminal state, route through
		// the same terminal-outcome reporter so the user sees consistent
		// messaging (including the portal URL).
		if state, getErr := deployment.Get(context.WithoutCancel(ctx)); getErr == nil &&
			isTerminalProvisioningState(state.ProvisioningState) {
			return terminalToOutcome(state.ProvisioningState, portalUrl, p, ctx)
		}
		p.console.StopSpinner(ctx, "Cancel request failed", input.StepFailed)
		log.Printf("interrupt handler: cancel request failed: %v", err)
		if portalUrl != "" {
			p.console.Message(ctx,
				output.WithWarningFormat(
					"Azure cancel request failed. Track the deployment here:\n  %s", portalUrl))
		}
		return interruptOutcome{
			err: fmt.Errorf("%w: %w",
				provisioning.ErrDeploymentCancelFailed, err),
			telemetryValue: "cancel_failed",
		}
	}

	p.console.StopSpinner(ctx, "", input.Step)
	p.console.ShowSpinner(ctx, "Waiting for Azure to confirm cancellation", input.Step)

	// Poll until terminal or until our wait budget elapses.
	pollCtx, pollDone := context.WithTimeout(
		context.WithoutCancel(ctx), cancelTerminalTimeout)
	defer pollDone()

	timer := time.NewTimer(cancelPollInterval)
	defer timer.Stop()

	var lastState azapi.DeploymentProvisioningState
	for {
		state, err := deployment.Get(pollCtx)
		if err == nil {
			lastState = state.ProvisioningState
			if isTerminalProvisioningState(lastState) {
				return terminalToOutcome(lastState, portalUrl, p, ctx)
			}
		} else {
			// Don't fail the whole flow on a transient Get error — keep
			// polling until either we observe a terminal state or the
			// timeout fires.
			log.Printf("interrupt handler: poll Get failed (will retry): %v", err)
		}

		select {
		case <-pollCtx.Done():
			p.console.StopSpinner(ctx, "Cancellation still in progress on Azure", input.StepWarning)
			if portalUrl != "" {
				p.console.Message(ctx,
					output.WithWarningFormat(
						"Azure has not confirmed cancellation within %s. Track the deployment here:\n  %s",
						cancelTerminalTimeout, portalUrl))
			}
			return interruptOutcome{
				err:            provisioning.ErrDeploymentCancelTimeout,
				telemetryValue: "cancel_timed_out",
			}
		case <-timer.C:
			timer.Reset(cancelPollInterval)
		}
	}
}

func terminalToOutcome(
	state azapi.DeploymentProvisioningState,
	portalUrl string,
	p *BicepProvider,
	ctx context.Context,
) interruptOutcome {
	switch state {
	case azapi.DeploymentProvisioningStateCanceled:
		p.console.StopSpinner(ctx, "Deployment cancelled", input.StepDone)
		if portalUrl != "" {
			p.console.Message(ctx,
				output.WithHighLightFormat(
					"Cancelled deployment is recorded in the portal:\n  %s", portalUrl))
		}
		return interruptOutcome{
			err:            provisioning.ErrDeploymentCanceledByUser,
			telemetryValue: "canceled",
		}
	case azapi.DeploymentProvisioningStateSucceeded,
		azapi.DeploymentProvisioningStateFailed:
		p.console.StopSpinner(ctx, "Deployment finished before cancel could take effect", input.StepWarning)
		if portalUrl != "" {
			p.console.Message(ctx,
				output.WithWarningFormat(
					"The Azure deployment reached %q before the cancel request took effect. Review:\n  %s",
					string(state), portalUrl))
		}
		return interruptOutcome{
			err:            provisioning.ErrDeploymentCancelTooLate,
			telemetryValue: "cancel_too_late",
		}
	default:
		// isTerminalProvisioningState should prevent reaching here, but be
		// defensive: stop the spinner and warn the user so the UI is left in
		// a clean state, then surface as too-late so the caller exits.
		p.console.StopSpinner(ctx, "Deployment reached an unexpected terminal state", input.StepWarning)
		if portalUrl != "" {
			p.console.Message(ctx,
				output.WithWarningFormat(
					"The Azure deployment reached unexpected terminal state %q after the cancel request. Review:\n  %s",
					string(state), portalUrl))
		} else {
			p.console.Message(ctx,
				output.WithWarningFormat(
					"The Azure deployment reached unexpected terminal state %q after the cancel request.",
					string(state)))
		}
		return interruptOutcome{
			err:            provisioning.ErrDeploymentCancelTooLate,
			telemetryValue: "cancel_too_late",
		}
	}
}

// isTerminalProvisioningState reports whether an Azure deployment provisioning
// state represents a terminal outcome (no further transitions expected).
func isTerminalProvisioningState(state azapi.DeploymentProvisioningState) bool {
	switch state {
	case azapi.DeploymentProvisioningStateCanceled,
		azapi.DeploymentProvisioningStateFailed,
		azapi.DeploymentProvisioningStateSucceeded,
		azapi.DeploymentProvisioningStateDeleted:
		return true
	}
	return false
}

// applyInterruptOutcome decides what to return from BicepProvider.Deploy when
// an interrupt outcome was produced. It composes any pre-existing deploy error
// with the interrupt sentinel so error wrapping (`errors.Is`) keeps working.
func applyInterruptOutcome(outcome interruptOutcome, deployErr error) error {
	if deployErr == nil {
		return outcome.err
	}
	// Most likely deployErr is "context canceled" wrapped by the SDK (because
	// we cancelled deployCtx to unblock PollUntilDone). Prefer the typed
	// interrupt sentinel for the user-visible error chain.
	if errors.Is(deployErr, context.Canceled) {
		return outcome.err
	}
	return fmt.Errorf("%w: %w", outcome.err, deployErr)
}
