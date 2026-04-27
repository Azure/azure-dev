// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func TestIsTerminalProvisioningState(t *testing.T) {
	terminal := []azapi.DeploymentProvisioningState{
		azapi.DeploymentProvisioningStateCanceled,
		azapi.DeploymentProvisioningStateFailed,
		azapi.DeploymentProvisioningStateSucceeded,
		azapi.DeploymentProvisioningStateDeleted,
	}
	nonTerminal := []azapi.DeploymentProvisioningState{
		azapi.DeploymentProvisioningStateAccepted,
		azapi.DeploymentProvisioningStateCanceling,
		azapi.DeploymentProvisioningStateRunning,
		azapi.DeploymentProvisioningStateDeploying,
		azapi.DeploymentProvisioningStateValidating,
		azapi.DeploymentProvisioningStateWaiting,
		azapi.DeploymentProvisioningStateNotSpecified,
		"",
	}
	for _, s := range terminal {
		require.Truef(t, isTerminalProvisioningState(s), "expected %q to be terminal", s)
	}
	for _, s := range nonTerminal {
		require.Falsef(t, isTerminalProvisioningState(s), "expected %q to NOT be terminal", s)
	}
}

func TestApplyInterruptOutcome(t *testing.T) {
	leave := interruptOutcome{
		err:            provisioning.ErrDeploymentInterruptedLeaveRunning,
		telemetryValue: "leave_running",
	}
	canceled := interruptOutcome{
		err:            provisioning.ErrDeploymentCanceledByUser,
		telemetryValue: "canceled",
	}

	t.Run("nil deploy error returns outcome err", func(t *testing.T) {
		require.ErrorIs(t, applyInterruptOutcome(leave, nil),
			provisioning.ErrDeploymentInterruptedLeaveRunning)
		require.ErrorIs(t, applyInterruptOutcome(canceled, nil),
			provisioning.ErrDeploymentCanceledByUser)
	})

	t.Run("context canceled is replaced by outcome err", func(t *testing.T) {
		err := applyInterruptOutcome(canceled, context.Canceled)
		require.ErrorIs(t, err, provisioning.ErrDeploymentCanceledByUser)
		require.NotErrorIs(t, err, context.Canceled)
	})

	t.Run("wrapped context canceled is replaced by outcome err", func(t *testing.T) {
		wrapped := fmt.Errorf("PollUntilDone: %w", context.Canceled)
		err := applyInterruptOutcome(leave, wrapped)
		require.ErrorIs(t, err, provisioning.ErrDeploymentInterruptedLeaveRunning)
		require.NotErrorIs(t, err, context.Canceled)
	})

	t.Run("non-cancel deploy error is preserved alongside outcome", func(t *testing.T) {
		other := errors.New("template validation failed")
		err := applyInterruptOutcome(canceled, other)
		require.ErrorIs(t, err, provisioning.ErrDeploymentCanceledByUser)
		require.ErrorIs(t, err, other)
	})
}

// fakeDeployment is a programmable infra.Deployment used by interrupt tests.
// Only the methods that the interrupt flow exercises (Cancel, Get, DeploymentUrl)
// have meaningful behavior; the rest panic if invoked.
type fakeDeployment struct {
	deploymentUrl    string
	deploymentUrlErr error

	cancelCalls atomic.Int32
	cancelFn    func(ctx context.Context) error

	getCalls atomic.Int32
	// getFn is invoked on each Get; the int passed in is the 1-based call
	// index so tests can sequence different responses.
	getFn func(ctx context.Context, callIndex int32) (*azapi.ResourceDeployment, error)
}

func (f *fakeDeployment) Cancel(ctx context.Context) error {
	n := f.cancelCalls.Add(1)
	if f.cancelFn == nil {
		return nil
	}
	_ = n
	return f.cancelFn(ctx)
}

func (f *fakeDeployment) Get(ctx context.Context) (*azapi.ResourceDeployment, error) {
	n := f.getCalls.Add(1)
	if f.getFn == nil {
		return &azapi.ResourceDeployment{}, nil
	}
	return f.getFn(ctx, n)
}

func (f *fakeDeployment) DeploymentUrl(ctx context.Context) (string, error) {
	return f.deploymentUrl, f.deploymentUrlErr
}

// The remaining infra.Deployment surface is unused by the interrupt flow.
func (f *fakeDeployment) SubscriptionId() string { panic("unused") }
func (f *fakeDeployment) ListDeployments(context.Context) ([]*azapi.ResourceDeployment, error) {
	panic("unused")
}
func (f *fakeDeployment) Deployment(string) infra.Deployment         { panic("unused") }
func (f *fakeDeployment) Name() string                               { return "fake-deployment" }
func (f *fakeDeployment) PortalUrl(context.Context) (string, error)  { panic("unused") }
func (f *fakeDeployment) OutputsUrl(context.Context) (string, error) { panic("unused") }
func (f *fakeDeployment) ValidatePreflight(
	context.Context, azure.RawArmTemplate, azure.ArmParameters, map[string]*string, map[string]any,
) error {
	panic("unused")
}
func (f *fakeDeployment) Deploy(
	context.Context, azure.RawArmTemplate, azure.ArmParameters, map[string]*string, map[string]any,
) (*azapi.ResourceDeployment, error) {
	panic("unused")
}
func (f *fakeDeployment) Delete(
	context.Context, map[string]any, *async.Progress[azapi.DeleteDeploymentProgress],
) error {
	panic("unused")
}
func (f *fakeDeployment) DeployPreview(
	context.Context, azure.RawArmTemplate, azure.ArmParameters,
) (*armresources.WhatIfOperationResult, error) {
	panic("unused")
}
func (f *fakeDeployment) Operations(context.Context) ([]*armresources.DeploymentOperation, error) {
	panic("unused")
}
func (f *fakeDeployment) Resources(context.Context) ([]*armresources.ResourceReference, error) {
	panic("unused")
}

// withFastInterruptPolling shrinks the cancel poll/timeout knobs for tests
// that exercise cancelAndAwaitTerminal so the suite stays sub-second.
func withFastInterruptPolling(t *testing.T) {
	t.Helper()
	prevReq, prevTerm, prevPoll := cancelRequestTimeout, cancelTerminalTimeout, cancelPollInterval
	cancelRequestTimeout = 100 * time.Millisecond
	cancelTerminalTimeout = 200 * time.Millisecond
	cancelPollInterval = 5 * time.Millisecond
	t.Cleanup(func() {
		cancelRequestTimeout = prevReq
		cancelTerminalTimeout = prevTerm
		cancelPollInterval = prevPoll
	})
}

// newTestProvider builds a minimal BicepProvider with only the fields the
// interrupt flow touches populated (the console).
func newTestProvider(mockContext *mocks.MockContext) *BicepProvider {
	return &BicepProvider{console: mockContext.Console}
}

func TestRunInterruptPrompt_LeaveRunning(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	mockContext.Console.WhenSelect(func(o input.ConsoleOptions) bool {
		return true
	}).RespondFn(func(o input.ConsoleOptions) (any, error) {
		require.Equal(t, interruptOptionLeaveRunning, o.Options[0])
		require.Equal(t, interruptOptionCancel, o.Options[1])
		return 0, nil
	})

	provider := newTestProvider(mockContext)
	deployment := &fakeDeployment{deploymentUrl: "https://portal/deployment"}

	outcome := provider.runInterruptPrompt(t.Context(), deployment)
	require.ErrorIs(t, outcome.err, provisioning.ErrDeploymentInterruptedLeaveRunning)
	require.Equal(t, "leave_running", outcome.telemetryValue)
	require.Equal(t, int32(0), deployment.cancelCalls.Load(),
		"leave-running must not submit a cancel request")
}

func TestRunInterruptPrompt_PromptError_FallsBackToLeaveRunning(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	mockContext.Console.WhenSelect(func(o input.ConsoleOptions) bool { return true }).
		RespondFn(func(o input.ConsoleOptions) (any, error) {
			return 0, errors.New("non-interactive: stdin closed")
		})

	provider := newTestProvider(mockContext)
	deployment := &fakeDeployment{deploymentUrl: "https://portal/deployment"}

	outcome := provider.runInterruptPrompt(t.Context(), deployment)
	require.ErrorIs(t, outcome.err, provisioning.ErrDeploymentInterruptedLeaveRunning)
	require.Equal(t, "leave_running", outcome.telemetryValue)
	require.Equal(t, int32(0), deployment.cancelCalls.Load())
}

func TestRunInterruptPrompt_DeploymentUrlError_StillPrompts(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	mockContext.Console.WhenSelect(func(o input.ConsoleOptions) bool { return true }).
		RespondFn(func(o input.ConsoleOptions) (any, error) {
			require.NotContains(t, o.Help, "Portal:",
				"prompt help must omit Portal line when URL is unavailable")
			return 0, nil
		})

	provider := newTestProvider(mockContext)
	deployment := &fakeDeployment{deploymentUrlErr: errors.New("ARM unreachable")}

	outcome := provider.runInterruptPrompt(t.Context(), deployment)
	require.ErrorIs(t, outcome.err, provisioning.ErrDeploymentInterruptedLeaveRunning)
}

func TestCancelAndAwaitTerminal_CancelNotSupported_ReturnsLeaveRunning(t *testing.T) {
	withFastInterruptPolling(t)
	mockContext := mocks.NewMockContext(context.Background())
	provider := newTestProvider(mockContext)

	deployment := &fakeDeployment{
		cancelFn: func(ctx context.Context) error { return azapi.ErrCancelNotSupported },
	}

	outcome := provider.cancelAndAwaitTerminal(t.Context(), deployment, "https://portal/x")
	require.ErrorIs(t, outcome.err, provisioning.ErrDeploymentInterruptedLeaveRunning)
	require.Equal(t, "leave_running", outcome.telemetryValue)
	require.Equal(t, int32(1), deployment.cancelCalls.Load())
	require.Equal(t, int32(0), deployment.getCalls.Load(),
		"cancel-not-supported must short-circuit before any Get poll")
}

func TestCancelAndAwaitTerminal_CancelFailed_NoTerminalState(t *testing.T) {
	withFastInterruptPolling(t)
	mockContext := mocks.NewMockContext(context.Background())
	provider := newTestProvider(mockContext)

	cancelErr := errors.New("ARM 503")
	deployment := &fakeDeployment{
		cancelFn: func(ctx context.Context) error { return cancelErr },
		getFn: func(ctx context.Context, n int32) (*azapi.ResourceDeployment, error) {
			return nil, errors.New("ARM Get also failing")
		},
	}

	outcome := provider.cancelAndAwaitTerminal(t.Context(), deployment, "https://portal/x")
	require.ErrorIs(t, outcome.err, provisioning.ErrDeploymentCancelFailed)
	require.ErrorIs(t, outcome.err, cancelErr)
	require.Equal(t, "cancel_failed", outcome.telemetryValue)
}

func TestCancelAndAwaitTerminal_CancelFailed_ButDeploymentAlreadyTerminal(t *testing.T) {
	withFastInterruptPolling(t)
	mockContext := mocks.NewMockContext(context.Background())
	provider := newTestProvider(mockContext)

	deployment := &fakeDeployment{
		cancelFn: func(ctx context.Context) error { return errors.New("ARM 409 conflict") },
		getFn: func(ctx context.Context, n int32) (*azapi.ResourceDeployment, error) {
			return &azapi.ResourceDeployment{
				ProvisioningState: azapi.DeploymentProvisioningStateSucceeded,
			}, nil
		},
	}

	outcome := provider.cancelAndAwaitTerminal(t.Context(), deployment, "https://portal/x")
	require.ErrorIs(t, outcome.err, provisioning.ErrDeploymentCancelTooLate)
	require.Equal(t, "cancel_too_late", outcome.telemetryValue)
}

func TestCancelAndAwaitTerminal_FirstGetIsImmediate(t *testing.T) {
	// Use a very long poll interval so that if the impl regresses to
	// "tick-then-Get", this test would block far longer than the deadline.
	t.Helper()
	prevReq, prevTerm, prevPoll := cancelRequestTimeout, cancelTerminalTimeout, cancelPollInterval
	cancelRequestTimeout = 100 * time.Millisecond
	cancelTerminalTimeout = 5 * time.Second
	cancelPollInterval = 5 * time.Second
	t.Cleanup(func() {
		cancelRequestTimeout = prevReq
		cancelTerminalTimeout = prevTerm
		cancelPollInterval = prevPoll
	})

	mockContext := mocks.NewMockContext(context.Background())
	provider := newTestProvider(mockContext)

	deployment := &fakeDeployment{
		// First Get already returns Canceled — no poll-interval wait needed.
		getFn: func(ctx context.Context, n int32) (*azapi.ResourceDeployment, error) {
			require.Equal(t, int32(1), n,
				"first Get must be issued before any poll-interval wait")
			return &azapi.ResourceDeployment{
				ProvisioningState: azapi.DeploymentProvisioningStateCanceled,
			}, nil
		},
	}

	start := time.Now()
	outcome := provider.cancelAndAwaitTerminal(t.Context(), deployment, "https://portal/x")
	elapsed := time.Since(start)

	require.ErrorIs(t, outcome.err, provisioning.ErrDeploymentCanceledByUser)
	require.Less(t, elapsed, time.Second,
		"fast-path cancellation should not wait a full poll interval; took %s", elapsed)
	require.Equal(t, int32(1), deployment.getCalls.Load())
}

func TestCancelAndAwaitTerminal_PollsUntilCanceled(t *testing.T) {
	withFastInterruptPolling(t)
	mockContext := mocks.NewMockContext(context.Background())
	provider := newTestProvider(mockContext)

	deployment := &fakeDeployment{
		getFn: func(ctx context.Context, n int32) (*azapi.ResourceDeployment, error) {
			if n < 3 {
				return &azapi.ResourceDeployment{
					ProvisioningState: azapi.DeploymentProvisioningStateRunning,
				}, nil
			}
			return &azapi.ResourceDeployment{
				ProvisioningState: azapi.DeploymentProvisioningStateCanceled,
			}, nil
		},
	}

	outcome := provider.cancelAndAwaitTerminal(t.Context(), deployment, "https://portal/x")
	require.ErrorIs(t, outcome.err, provisioning.ErrDeploymentCanceledByUser)
	require.Equal(t, "canceled", outcome.telemetryValue)
	require.GreaterOrEqual(t, deployment.getCalls.Load(), int32(3))
}

func TestCancelAndAwaitTerminal_TimeoutWhilePolling(t *testing.T) {
	withFastInterruptPolling(t)
	mockContext := mocks.NewMockContext(context.Background())
	provider := newTestProvider(mockContext)

	// Always Running → never reaches a terminal state, so the poll budget
	// must elapse and we must report cancel_timed_out.
	deployment := &fakeDeployment{
		getFn: func(ctx context.Context, n int32) (*azapi.ResourceDeployment, error) {
			return &azapi.ResourceDeployment{
				ProvisioningState: azapi.DeploymentProvisioningStateRunning,
			}, nil
		},
	}

	outcome := provider.cancelAndAwaitTerminal(t.Context(), deployment, "https://portal/x")
	require.ErrorIs(t, outcome.err, provisioning.ErrDeploymentCancelTimeout)
	require.Equal(t, "cancel_timed_out", outcome.telemetryValue)
}

func TestInstallDeploymentInterruptHandler_MarkCompletedWinsRace(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	provider := newTestProvider(mockContext)
	deployment := &fakeDeployment{}

	deployCtx, started, _, markCompleted, cleanup := provider.installDeploymentInterruptHandler(
		t.Context(), deployment, nil)
	defer cleanup()

	// Deploy completes naturally first → markCompleted wins the CAS.
	require.True(t, markCompleted(),
		"markCompleted must succeed when no interrupt has fired")

	// A handler invocation that arrives after completion must be a no-op:
	// it must not close started and must not cancel deployCtx.
	stack := input.SnapshotInterruptStack()
	require.NotEmpty(t, stack, "handler should still be on the stack pre-cleanup")
	handled := stack[len(stack)-1]()
	require.False(t, handled,
		"handler must return false (decline ownership) when deploy already completed")

	select {
	case <-started:
		t.Fatal("started must NOT be closed when markCompleted wins the race")
	case <-deployCtx.Done():
		t.Fatal("deployCtx must NOT be cancelled when markCompleted wins the race")
	default:
	}
}

func TestInstallDeploymentInterruptHandler_InterruptWinsRace(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	mockContext.Console.WhenSelect(func(o input.ConsoleOptions) bool { return true }).
		RespondFn(func(o input.ConsoleOptions) (any, error) {
			// "Leave running" — keeps the test deterministic and avoids
			// exercising the cancel poll loop here.
			return 0, nil
		})

	provider := newTestProvider(mockContext)
	deployment := &fakeDeployment{deploymentUrl: "https://portal/x"}

	onStartCalled := false
	deployCtx, started, outcomeCh, markCompleted, cleanup := provider.installDeploymentInterruptHandler(
		t.Context(), deployment, func() { onStartCalled = true })
	defer cleanup()

	stack := input.SnapshotInterruptStack()
	require.NotEmpty(t, stack)
	handled := stack[len(stack)-1]()
	require.True(t, handled,
		"handler must claim shutdown ownership when the interrupt wins the race")
	require.True(t, onStartCalled, "onInterruptStart must be invoked")

	// started must be closed and deployCtx must be cancelled synchronously
	// before the prompt is shown so Deploy can flip to wait-for-outcome mode.
	select {
	case <-started:
	default:
		t.Fatal("started must be closed when the handler runs")
	}
	require.ErrorIs(t, deployCtx.Err(), context.Canceled,
		"deployCtx must be cancelled when the handler runs")

	// Outcome must be available on the channel.
	select {
	case outcome := <-outcomeCh:
		require.ErrorIs(t, outcome.err, provisioning.ErrDeploymentInterruptedLeaveRunning)
	case <-time.After(time.Second):
		t.Fatal("outcome was not delivered on outcomeCh")
	}

	// Once the interrupt has won, markCompleted must fail.
	require.False(t, markCompleted(),
		"markCompleted must return false after the interrupt path has claimed the state")
}
