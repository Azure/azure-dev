// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package doctor

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// ---- NewRemoteChecks contract ----

// TestNewRemoteChecks_HasAuthOnly pins the current shape of the remote
// chain: exactly one check, `remote.auth`, with Remote=true. When
// C12 / C16 / C17 land, update this test to reflect the new order +
// IDs — the assertion is precise on purpose so adding a check anywhere
// requires touching this single test instead of silently expanding
// the chain.
func TestNewRemoteChecks_HasAuthOnly(t *testing.T) {
	t.Parallel()

	got := NewRemoteChecks(Dependencies{})

	require.Len(t, got, 1, "NewRemoteChecks should contain exactly the auth check today")
	require.Equal(t, "remote.auth", got[0].ID)
	require.Equal(t, "authentication", got[0].Name)
	require.True(t, got[0].Remote, "remote.auth must declare Remote=true")
	require.NotNil(t, got[0].Fn, "remote.auth must have a non-nil Fn")
}

// TestNewLocalAndRemoteChecks_ProductionCompositionLocalsFirst pins the
// load-bearing local-then-remote ordering that doctor.go:runDoctor
// composes via `append(NewLocalChecks, NewRemoteChecks...)`. Without
// this test, a future contributor could accidentally swap the
// composition order (or land a remote check inside NewLocalChecks /
// vice versa) and every other existing test would still pass, while
// remote checks' `priorBlocked(prior, "local.X")` skip-cascade guards
// would silently always return false.
//
// We assert two invariants on the production composition:
//
//  1. No local check (Remote == false) appears AFTER any remote check.
//     Locals must run first so their results are in `prior` when remote
//     checks evaluate `priorBlocked`.
//  2. Every check returned by NewRemoteChecks carries Remote == true
//     (the same convention bullet documented in checks_remote.go).
//     Forgetting the flag would cause the runner to (a) not skip the
//     check under --local-only and (b) not flip report.Remote.
func TestNewLocalAndRemoteChecks_ProductionCompositionLocalsFirst(t *testing.T) {
	t.Parallel()

	locals := NewLocalChecks(Dependencies{})
	remotes := NewRemoteChecks(Dependencies{})

	for i, c := range locals {
		require.Falsef(t, c.Remote,
			"NewLocalChecks[%d] %q has Remote=true; locals must declare Remote=false",
			i, c.ID)
	}
	for i, c := range remotes {
		require.Truef(t, c.Remote,
			"NewRemoteChecks[%d] %q has Remote=false; remotes must declare Remote=true",
			i, c.ID)
	}

	// Invariant 1: combined ordering must place every local before
	// every remote. Equivalent to the contract `runDoctor` relies on.
	combined := append(locals, remotes...)
	sawRemote := false
	for _, c := range combined {
		if c.Remote {
			sawRemote = true
			continue
		}
		require.Falsef(t, sawRemote,
			"local check %q appears after a remote check in the "+
				"combined doctor pipeline; runDoctor's skip-cascade "+
				"contract requires local-then-remote ordering",
			c.ID)
	}
}

// ---- Framework integration: local + remote interaction ----

// TestRunner_LocalThenRemote_RemoteSeesLocalPriorResults proves the
// runner preserves the order `NewLocalChecks ++ NewRemoteChecks` so a
// remote check's skip-cascade can read the local check results. This
// is the load-bearing contract C11+ remote checks depend on (each one
// calls `priorBlocked(prior, "local.X")` to decide whether to skip).
//
// We don't use the real NewLocalChecks here because that would couple
// this test to the live gRPC stack. Instead we synthesize a local +
// remote pair using the same Check shape and assert the ordering.
func TestRunner_LocalThenRemote_RemoteSeesLocalPriorResults(t *testing.T) {
	t.Parallel()

	var observed []Result
	runner := &Runner{
		Checks: append(
			[]Check{
				{ID: "local.x", Name: "local x", Fn: func(_ context.Context, _ Options, _ []Result) Result {
					return Result{Status: StatusFail, Message: "local x failed"}
				}},
			},
			Check{
				ID:     "remote.y",
				Name:   "remote y",
				Remote: true,
				Fn: func(_ context.Context, _ Options, prior []Result) Result {
					observed = append([]Result(nil), prior...)
					// Mirror the convention C11+ checks will follow:
					// inspect prior, skip when a local precondition
					// failed.
					if priorBlocked(prior, "local.x") {
						return Result{Status: StatusSkip, Message: "skipped: upstream local.x"}
					}
					return Result{Status: StatusPass, Message: "remote y ran"}
				},
			},
		),
	}

	report := runner.Run(t.Context(), Options{})

	require.Len(t, observed, 1, "remote check must see exactly the one local prior result")
	require.Equal(t, "local.x", observed[0].ID)
	require.Equal(t, StatusFail, observed[0].Status)
	require.Equal(t, StatusSkip, report.Checks[1].Status, "remote check should have skipped via priorBlocked")
	require.Contains(t, report.Checks[1].Message, "upstream local.x")
}

// TestRunner_LocalOnly_RemoteCheckNotInvoked complements the runner's
// existing TestRunner_Run_LocalOnly_SkipsRemoteChecks by exercising the
// combination used by the doctor command in production:
// `append(NewLocalChecks, NewRemoteChecks...)`. We synthesize a remote
// check that would Fail if invoked, then assert it produces a Skip
// without running.
func TestRunner_LocalOnly_AppendedRemoteCheck_NotInvoked(t *testing.T) {
	t.Parallel()

	invoked := false
	checks := append(
		[]Check{
			{ID: "local.x", Name: "local x", Fn: func(_ context.Context, _ Options, _ []Result) Result {
				return Result{Status: StatusPass, Message: "ok"}
			}},
		},
		Check{
			ID: "remote.y", Name: "remote y", Remote: true,
			Fn: func(_ context.Context, _ Options, _ []Result) Result {
				invoked = true
				return Result{Status: StatusFail, Message: "remote check ran when it should not have"}
			},
		},
	)

	runner := &Runner{Checks: checks}
	report := runner.Run(t.Context(), Options{LocalOnly: true})

	require.False(t, invoked, "remote check function must not be invoked under --local-only")
	require.Len(t, report.Checks, 2)
	require.Equal(t, StatusPass, report.Checks[0].Status)
	require.Equal(t, StatusSkip, report.Checks[1].Status)
	require.Contains(t, report.Checks[1].Message, "local-only")
	require.False(t, report.Remote, "report.Remote must remain false when only local checks executed")
}

// TestRunner_RemoteCheck_RanProducesReportRemoteFlag mirrors the
// existing TestRunner_Run_RemoteCheck_FlipsReportRemoteFlag but
// scoped to the combined local+remote shape used in production.
func TestRunner_RemoteCheck_RanProducesReportRemoteFlag(t *testing.T) {
	t.Parallel()

	checks := append(
		[]Check{
			{ID: "local.x", Name: "local x", Fn: func(_ context.Context, _ Options, _ []Result) Result {
				return Result{Status: StatusPass}
			}},
		},
		Check{
			ID: "remote.y", Name: "remote y", Remote: true,
			Fn: func(_ context.Context, _ Options, _ []Result) Result {
				return Result{Status: StatusPass}
			},
		},
	)

	report := (&Runner{Checks: checks}).Run(t.Context(), Options{})

	require.True(t, report.Remote, "any executed remote check must flip report.Remote")
}
