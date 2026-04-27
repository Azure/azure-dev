// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

// Default timeouts for interrupt-driven cancellation. They are package-level
// vars (not consts) so tests can override them to keep poll/timeout-driven
// flows fast.
var (
	// cancelRequestTimeout bounds the time spent waiting for the ARM Cancel
	// API call itself to return.
	cancelRequestTimeout = 30 * time.Second
	// cancelOverallTimeout is the global budget covering EVERYTHING that
	// happens after the cancel request is accepted: waiting for the
	// top-level deployment to reach a terminal state, discovering and
	// canceling any descendant deployments, and waiting for those to also
	// reach a terminal state. The user will see at most ~this much time
	// between selecting "Cancel" and azd reporting an outcome.
	cancelOverallTimeout = 5 * time.Minute
	// cancelPollInterval controls how often we poll the deployment for state
	// changes after submitting cancel.
	cancelPollInterval = 5 * time.Second
)

// cancelNestedConcurrency caps the number of concurrent ARM API calls when
// canceling/polling descendant deployments to avoid throttling on large trees.
const cancelNestedConcurrency = 5

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

// deployState tracks the lifecycle of the deployment so the interrupt handler
// and the Deploy goroutine can coordinate without races.
type deployState int32

const (
	deployStateRunning      deployState = iota // ARM deploy is in flight
	deployStateInterrupting                    // handler claimed the Ctrl+C
	deployStateCompleted                       // Deploy returned naturally
)

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
//   - markCompleted: must be called by Deploy right after deployModule returns
//     (before the select on startedCh) to atomically claim the "completed"
//     state. If the interrupt handler already claimed "interrupting", this
//     returns false and the caller must wait for the outcome.
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
	markCompleted func() bool,
	cleanup func(),
) {
	deployCtx, cancelDeploy := context.WithCancel(ctx)
	ch := make(chan interruptOutcome, 1)
	started := make(chan struct{})

	var state atomic.Int32 // deployState values

	pop := input.PushInterruptHandler(sync.OnceValue(func() bool {
		// Try to claim the "interrupting" state. If Deploy already set
		// "completed", the prompt is unnecessary — the deployment finished
		// naturally and the success path should run instead.
		if !state.CompareAndSwap(
			int32(deployStateRunning),
			int32(deployStateInterrupting),
		) {
			return false
		}

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
	}))

	markCompleted = func() bool {
		return state.CompareAndSwap(
			int32(deployStateRunning),
			int32(deployStateCompleted),
		)
	}

	cleanup = func() {
		pop()
		cancelDeploy()
	}
	return deployCtx, started, ch, markCompleted, cleanup
}

// printLeaveRunningMessage emits the standard "Azure deployment will continue
// running" message with a clickable portal link. No-op when portalUrl is empty.
func (p *BicepProvider) printLeaveRunningMessage(ctx context.Context, portalUrl string) {
	if portalUrl == "" {
		return
	}
	p.console.Message(ctx,
		output.WithHighLightFormat("The Azure deployment will continue running. Track it here:\n  %s",
			output.WithLinkFormat(portalUrl)))
}

// runInterruptPrompt presents the user with the choice of cancelling the
// running Azure deployment or leaving it to run. It returns the outcome that
// should be propagated back to Deploy.
func (p *BicepProvider) runInterruptPrompt(
	ctx context.Context,
	deployment infra.Deployment,
) interruptOutcome {
	// Best-effort URL fetch — bounded so a slow/unreachable ARM endpoint
	// doesn't block the prompt indefinitely.
	urlCtx, urlDone := context.WithTimeout(
		context.WithoutCancel(ctx), cancelRequestTimeout)
	portalUrl, urlErr := deployment.DeploymentUrl(urlCtx)
	urlDone()
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
		p.printLeaveRunningMessage(ctx, portalUrl)
		return interruptOutcome{
			err:            provisioning.ErrDeploymentInterruptedLeaveRunning,
			telemetryValue: "leave_running",
		}
	}

	switch choice {
	case 0: // leave running
		p.printLeaveRunningMessage(ctx, portalUrl)
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
	p.console.ShowSpinner(ctx, "Canceling Azure deployment", input.Step)

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
			p.printLeaveRunningMessage(ctx, portalUrl)
			return interruptOutcome{
				err:            provisioning.ErrDeploymentInterruptedLeaveRunning,
				telemetryValue: "leave_running",
			}
		}
		// If the deployment is already in a terminal state, route through
		// the same terminal-outcome reporter so the user sees consistent
		// messaging (including the portal URL).
		getCtx, getDone := context.WithTimeout(
			context.WithoutCancel(ctx), cancelRequestTimeout)
		defer getDone()

		if state, getErr := deployment.Get(getCtx); getErr == nil &&
			isTerminalProvisioningState(state.ProvisioningState) {
			return p.terminalToOutcome(ctx, state.ProvisioningState, portalUrl)
		} else if getErr != nil {
			// Don't drop this — it's useful for diagnosing the cancel-failed
			// path in production logs (the user-facing error is still the
			// original cancel failure).
			log.Printf("interrupt handler: post-cancel Get failed: %v", getErr)
		}
		p.console.StopSpinner(ctx, "Cancel request failed", input.StepFailed)
		log.Printf("interrupt handler: cancel request failed: %v", err)
		if portalUrl != "" {
			p.console.Message(ctx,
				output.WithWarningFormat(
					"Azure cancel request failed. Track the deployment here:\n  %s",
					output.WithLinkFormat(portalUrl)))
		}
		return interruptOutcome{
			err: fmt.Errorf("%w: %w",
				provisioning.ErrDeploymentCancelFailed, err),
			telemetryValue: "cancel_failed",
		}
	}

	p.console.StopSpinner(ctx, "", input.Step)
	p.console.ShowSpinner(ctx, "Waiting for Azure to confirm cancellation", input.Step)

	// Single global deadline covering BOTH the top-level wait and any
	// descendant-deployment cleanup. The user shouldn't wait more than
	// cancelOverallTimeout total between pressing Ctrl+C and seeing an
	// outcome reported by azd.
	pollCtx, pollDone := context.WithTimeout(
		context.WithoutCancel(ctx), cancelOverallTimeout)
	defer pollDone()

	state, timedOut := p.awaitTopLevelTerminal(pollCtx, deployment)
	if timedOut {
		p.console.StopSpinner(ctx, "Cancellation still in progress on Azure", input.StepWarning)
		if portalUrl != "" {
			p.console.Message(ctx,
				output.WithWarningFormat(
					"Azure has not confirmed cancellation within %s. Track the deployment here:\n  %s",
					cancelOverallTimeout, output.WithLinkFormat(portalUrl)))
		}
		return interruptOutcome{
			err:            provisioning.ErrDeploymentCancelTimeout,
			telemetryValue: "cancel_timed_out",
		}
	}

	// When the cancel actually took effect on the top-level deployment, also
	// wait for any descendant deployments to reach a terminal state. ARM's
	// cancel cascade is asynchronous, and a child deployment can still hold
	// an active deployment lease for several minutes after the parent reports
	// "Canceled" — which would cause the next `azd provision` to fail with
	// a `DeploymentActive` validation error. We skip this for Succeeded /
	// Failed / Deleted because in those cases the children should already be
	// terminal (a parent only reports Succeeded once its children are done,
	// and Failed/Deleted reflect a settled state).
	if state == azapi.DeploymentProvisioningStateCanceled {
		stuck := p.cancelAndAwaitNested(pollCtx, deployment)
		if len(stuck) > 0 {
			return p.nestedTimeoutOutcome(ctx, stuck, portalUrl)
		}
	}

	return p.terminalToOutcome(ctx, state, portalUrl)
}

// awaitTopLevelTerminal polls the top-level deployment until it reaches a
// terminal provisioning state or the supplied context is canceled (e.g. by
// the global cancelOverallTimeout). The first Get is issued immediately so
// the fast path doesn't pay a poll-interval penalty; subsequent polls are
// ticker-spaced. Returns (state, timedOut). When timedOut is true, state is
// the zero value and the caller should report the timeout outcome.
func (p *BicepProvider) awaitTopLevelTerminal(
	pollCtx context.Context,
	deployment infra.Deployment,
) (azapi.DeploymentProvisioningState, bool) {
	// Issue the first Get immediately after the cancel request was accepted
	// — Azure can transition to a terminal state very quickly for deployments
	// that were just starting, and we don't want to make the user wait a full
	// poll interval for that fast path.
	if state, err := deployment.Get(pollCtx); err == nil {
		if isTerminalProvisioningState(state.ProvisioningState) {
			return state.ProvisioningState, false
		}
	} else {
		log.Printf("interrupt handler: initial poll Get failed (will retry): %v", err)
	}

	// Subsequent polls are ticker-driven so a slow Get cannot push the loop
	// into back-to-back ARM calls (and trigger throttling).
	ticker := time.NewTicker(cancelPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-pollCtx.Done():
			return "", true
		case <-ticker.C:
		}

		state, err := deployment.Get(pollCtx)
		if err == nil {
			if isTerminalProvisioningState(state.ProvisioningState) {
				return state.ProvisioningState, false
			}
		} else {
			// Don't fail the whole flow on a transient Get error — keep
			// polling until either we observe a terminal state or the
			// timeout fires.
			log.Printf("interrupt handler: poll Get failed (will retry): %v", err)
		}
	}
}

// terminalToOutcome maps a terminal provisioning state to the interrupt outcome
// that should be propagated back to Deploy.
func (p *BicepProvider) terminalToOutcome(
	ctx context.Context,
	state azapi.DeploymentProvisioningState,
	portalUrl string,
) interruptOutcome {
	switch state {
	case azapi.DeploymentProvisioningStateCanceled:
		p.console.StopSpinner(ctx, "Deployment canceled", input.StepDone)
		if portalUrl != "" {
			p.console.Message(ctx,
				output.WithHighLightFormat(
					"Canceled deployment is recorded in the portal:\n  %s",
					output.WithLinkFormat(portalUrl)))
		}
		return interruptOutcome{
			err:            provisioning.ErrDeploymentCanceledByUser,
			telemetryValue: "canceled",
		}
	case azapi.DeploymentProvisioningStateSucceeded,
		azapi.DeploymentProvisioningStateFailed:
		p.console.StopSpinner(ctx,
			"Deployment finished before cancel could take effect", input.StepWarning)
		if portalUrl != "" {
			p.console.Message(ctx,
				output.WithWarningFormat(
					"The Azure deployment reached %q before the cancel "+
						"request took effect. Review:\n  %s",
					string(state), output.WithLinkFormat(portalUrl)))
		}
		return interruptOutcome{
			err:            provisioning.ErrDeploymentCancelTooLate,
			telemetryValue: "cancel_too_late",
		}
	case azapi.DeploymentProvisioningStateDeleted:
		p.console.StopSpinner(ctx, "Deployment was deleted", input.StepWarning)
		if portalUrl != "" {
			p.console.Message(ctx,
				output.WithWarningFormat(
					"The Azure deployment was deleted before the cancel "+
						"request could take effect. Review:\n  %s",
					output.WithLinkFormat(portalUrl)))
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
					string(state), output.WithLinkFormat(portalUrl)))
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

// isNestedDeployment reports whether a deployment-operation refers to a
// child Microsoft.Resources/deployments resource. Mirrors the predicate
// used by pkg/infra.WalkDeploymentOperations so we can reuse the same
// "child deployment" semantics without exporting that package's internal.
func isNestedDeployment(op *armresources.DeploymentOperation) bool {
	if op == nil || op.Properties == nil ||
		op.Properties.TargetResource == nil ||
		op.Properties.TargetResource.ResourceType == nil ||
		op.Properties.ProvisioningOperation == nil {
		return false
	}
	return *op.Properties.TargetResource.ResourceType ==
		string(azapi.AzureResourceTypeDeployment) &&
		*op.Properties.ProvisioningOperation == armresources.ProvisioningOperationCreate
}

// cancelAndAwaitNested discovers the descendant deployments of a freshly
// canceled top-level deployment, best-effort cancels any that are still
// non-terminal, and polls them until either all have reached a terminal
// state or pollCtx is canceled (typically by the global cancelOverallTimeout).
//
// Returns the descendant deployments that were still non-terminal when the
// wait deadline fired; on success the returned slice is empty.
//
// Any failure to discover descendants (e.g. transient ARM error listing
// operations) is logged and treated as "no descendants found" — the
// interrupt UX should never explode just because we couldn't enumerate
// child deployments.
func (p *BicepProvider) cancelAndAwaitNested(
	pollCtx context.Context,
	parent infra.Deployment,
) []infra.Deployment {
	descendants, err := p.discoverDescendantDeployments(pollCtx, parent, p.deploymentForResourceID)
	if err != nil {
		log.Printf("interrupt handler: failed to discover descendant deployments: %v", err)
		return nil
	}
	if len(descendants) == 0 {
		return nil
	}

	// Best-effort cancel of any descendant still in a non-terminal state.
	// We deliberately swallow per-descendant errors here (logged) — the
	// authoritative signal is the polling loop below, and a failed Cancel
	// for a descendant that is already terminal is expected.
	p.cancelDescendants(pollCtx, descendants)

	// Poll concurrently with a small worker pool, returning whichever
	// deployments remain non-terminal at the deadline.
	return p.pollDescendantsTerminal(pollCtx, descendants)
}

// discoverDescendantDeployments walks the parent's deployment-operations tree
// and returns one infra.Deployment per *unique* nested deployment found at
// any depth. The deployment objects are constructed by `factory` from each
// operation's TargetResource.ID, so a sub-scope parent with RG-scope
// children is handled correctly. The factory is injected for test
// purposes — production callers should pass `p.deploymentForResourceID`.
func (p *BicepProvider) discoverDescendantDeployments(
	ctx context.Context,
	parent infra.Deployment,
	factory func(*arm.ResourceID) infra.Deployment,
) ([]infra.Deployment, error) {
	type frame struct {
		ops []*armresources.DeploymentOperation
	}

	rootOps, err := parent.Operations(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing operations: %w", err)
	}

	seen := map[string]struct{}{}
	var out []infra.Deployment
	queue := []frame{{ops: rootOps}}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		for _, op := range cur.ops {
			if op == nil || op.Properties == nil {
				continue
			}
			if !isNestedDeployment(op) {
				continue
			}
			if op.Properties.TargetResource == nil ||
				op.Properties.TargetResource.ID == nil {
				continue
			}
			id := *op.Properties.TargetResource.ID
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}

			parsed, err := arm.ParseResourceID(id)
			if err != nil {
				log.Printf("interrupt handler: skipping unparsable nested deployment id %q: %v", id, err)
				continue
			}

			child := factory(parsed)
			out = append(out, child)

			// Recurse into this child's operations to pick up grandchildren.
			childOps, err := child.Operations(ctx)
			if err != nil {
				log.Printf("interrupt handler: failed to list operations for nested deployment %q: %v",
					parsed.Name, err)
				continue
			}
			queue = append(queue, frame{ops: childOps})
		}
	}
	return out, nil
}

// deploymentForResourceID constructs an infra.Deployment from the parsed
// resource ID of a nested Microsoft.Resources/deployments resource. We pick
// the right scope (subscription vs resource group) from the ID itself rather
// than inheriting from the parent, so a sub-scope parent with RG-scope
// children works correctly.
func (p *BicepProvider) deploymentForResourceID(id *arm.ResourceID) infra.Deployment {
	if id.ResourceGroupName != "" {
		// Cancel/Get on RG-scope deployments don't depend on the
		// deployment manager's location, so we pass through subscription /
		// RG / name directly.
		scope := p.deploymentManager.ResourceGroupScope(id.SubscriptionID, id.ResourceGroupName)
		return p.deploymentManager.ResourceGroupDeployment(scope, id.Name)
	}
	// Sub-scope: location is irrelevant for Cancel/Get/DeploymentUrl.
	scope := p.deploymentManager.SubscriptionScope(id.SubscriptionID, "")
	return p.deploymentManager.SubscriptionDeployment(scope, id.Name)
}

// cancelDescendants issues a best-effort Cancel on each descendant that is
// not already in a terminal state. Per-descendant errors (including
// already-terminal "Conflict" responses and ErrCancelNotSupported) are
// logged at DEBUG and otherwise ignored — the polling loop is what decides
// success.
func (p *BicepProvider) cancelDescendants(
	pollCtx context.Context,
	descendants []infra.Deployment,
) {
	sem := make(chan struct{}, cancelNestedConcurrency)
	var wg sync.WaitGroup

	for _, d := range descendants {
		select {
		case <-pollCtx.Done():
			return
		default:
		}
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()

			// Skip if already terminal — saves an unnecessary Cancel call
			// (which would just return Conflict).
			if state, err := d.Get(pollCtx); err == nil &&
				isTerminalProvisioningState(state.ProvisioningState) {
				return
			}

			// Bound the cancel call itself so a single hung ARM endpoint
			// can't consume the global budget.
			cancelCtx, cancelDone := context.WithTimeout(pollCtx, cancelRequestTimeout)
			defer cancelDone()

			if err := d.Cancel(cancelCtx); err != nil {
				if !errors.Is(err, azapi.ErrCancelNotSupported) {
					log.Printf("interrupt handler: cancel failed for nested deployment %q: %v",
						d.Name(), err)
				}
			}
		})
	}
	wg.Wait()
}

// pollDescendantsTerminal polls each non-terminal descendant deployment until
// it reaches a terminal state or pollCtx fires. Returns the slice of
// descendants that were still non-terminal when pollCtx fired (empty slice
// on full success).
func (p *BicepProvider) pollDescendantsTerminal(
	pollCtx context.Context,
	descendants []infra.Deployment,
) []infra.Deployment {
	type result struct {
		d        infra.Deployment
		terminal bool
	}

	results := make(chan result, len(descendants))
	sem := make(chan struct{}, cancelNestedConcurrency)
	var wg sync.WaitGroup

	for _, d := range descendants {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			results <- result{d: d, terminal: pollSingleTerminal(pollCtx, d)}
		})
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	var stuck []infra.Deployment
	for r := range results {
		if !r.terminal {
			stuck = append(stuck, r.d)
		}
	}
	return stuck
}

// pollSingleTerminal polls a single deployment until terminal or pollCtx fires.
// Returns true if a terminal state was observed.
func pollSingleTerminal(pollCtx context.Context, d infra.Deployment) bool {
	if state, err := d.Get(pollCtx); err == nil &&
		isTerminalProvisioningState(state.ProvisioningState) {
		return true
	}
	ticker := time.NewTicker(cancelPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-pollCtx.Done():
			return false
		case <-ticker.C:
		}
		state, err := d.Get(pollCtx)
		if err != nil {
			log.Printf("interrupt handler: poll Get failed for nested deployment %q (will retry): %v",
				d.Name(), err)
			continue
		}
		if isTerminalProvisioningState(state.ProvisioningState) {
			return true
		}
	}
}

// nestedTimeoutOutcome reports the timeout outcome when one or more
// descendant deployments did not reach a terminal state within the global
// cancelOverallTimeout. The user-facing message lists portal URLs for the
// stuck deployments so they can investigate.
func (p *BicepProvider) nestedTimeoutOutcome(
	ctx context.Context,
	stuck []infra.Deployment,
	parentPortalUrl string,
) interruptOutcome {
	p.console.StopSpinner(ctx,
		fmt.Sprintf("%d nested Azure deployment(s) did not finish within %s",
			len(stuck), cancelOverallTimeout),
		input.StepWarning)

	var lines []string
	if parentPortalUrl != "" {
		lines = append(lines,
			fmt.Sprintf("Top-level deployment was canceled, but the following nested "+
				"deployment(s) are still running. Track them in the portal:"))
	} else {
		lines = append(lines,
			"Top-level deployment was canceled, but the following nested "+
				"deployment(s) are still running. They may block the next deployment "+
				"until they reach a terminal state:")
	}

	for _, d := range stuck {
		url, err := d.DeploymentUrl(ctx)
		if err != nil || url == "" {
			lines = append(lines, fmt.Sprintf("  - %s", d.Name()))
		} else {
			lines = append(lines,
				fmt.Sprintf("  - %s\n      %s", d.Name(), output.WithLinkFormat(url)))
		}
	}

	for _, l := range lines {
		p.console.Message(ctx, output.WithWarningFormat("%s", l))
	}

	return interruptOutcome{
		err:            provisioning.ErrDeploymentCancelTimeout,
		telemetryValue: "cancel_timed_out_nested",
	}
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
