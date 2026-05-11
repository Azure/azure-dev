// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package doctor

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunner_Run_ProducesReportWithCanonicalIDsAndNames(t *testing.T) {
	t.Parallel()

	runner := &Runner{
		Checks: []Check{
			{
				ID:   "1",
				Name: "first",
				Fn: func(_ context.Context, _ Options, _ []Result) Result {
					// Intentionally leave ID/Name unset on the result —
					// the runner is the source of truth for both.
					return Result{Status: StatusPass, Message: "ok"}
				},
			},
			{
				ID:   "2",
				Name: "second",
				Fn: func(_ context.Context, _ Options, _ []Result) Result {
					// Pretend the check overrides ID/Name maliciously;
					// the runner must clobber both.
					return Result{ID: "999", Name: "overridden", Status: StatusFail, Message: "boom"}
				},
			},
		},
	}

	report := runner.Run(t.Context(), Options{})

	require.Equal(t, CurrentSchemaVersion, report.SchemaVersion)
	require.True(t, report.Redacted, "redacted defaults to true (inverse of Unredacted)")
	require.False(t, report.Remote, "no remote checks ran")
	require.Len(t, report.Checks, 2)
	require.Equal(t, "1", report.Checks[0].ID)
	require.Equal(t, "first", report.Checks[0].Name)
	require.Equal(t, "2", report.Checks[1].ID)
	require.Equal(t, "second", report.Checks[1].Name, "runner pins Name; check return is ignored")
}

func TestRunner_Run_PriorResultsPassedToSubsequentChecks(t *testing.T) {
	t.Parallel()

	var observed []Result
	runner := &Runner{
		Checks: []Check{
			{ID: "1", Name: "first", Fn: func(_ context.Context, _ Options, _ []Result) Result {
				return Result{Status: StatusPass, Message: "first done"}
			}},
			{ID: "2", Name: "second", Fn: func(_ context.Context, _ Options, prior []Result) Result {
				observed = append([]Result(nil), prior...)
				return Result{Status: StatusPass, Message: "second done"}
			}},
		},
	}

	report := runner.Run(t.Context(), Options{})

	require.Len(t, observed, 1, "second check should see exactly the prior result")
	require.Equal(t, "1", observed[0].ID)
	require.Equal(t, StatusPass, observed[0].Status)
	require.Equal(t, "first done", observed[0].Message)
	require.Len(t, report.Checks, 2)
}

func TestRunner_Run_LocalOnly_SkipsRemoteChecks(t *testing.T) {
	t.Parallel()

	called := false
	runner := &Runner{
		Checks: []Check{
			{ID: "1", Name: "local", Fn: func(_ context.Context, _ Options, _ []Result) Result {
				return Result{Status: StatusPass, Message: "ok"}
			}},
			{ID: "7", Name: "remote", Remote: true, Fn: func(_ context.Context, _ Options, _ []Result) Result {
				called = true
				return Result{Status: StatusPass, Message: "should not run"}
			}},
		},
	}

	report := runner.Run(t.Context(), Options{LocalOnly: true})

	require.False(t, called, "remote check function must not be invoked when LocalOnly is true")
	require.False(t, report.Remote, "Remote flag should remain false when only local checks executed")
	require.Equal(t, StatusSkip, report.Checks[1].Status)
	require.Contains(t, report.Checks[1].Message, "local-only")
}

func TestRunner_Run_RemoteCheck_FlipsReportRemoteFlag(t *testing.T) {
	t.Parallel()

	runner := &Runner{
		Checks: []Check{
			{ID: "7", Name: "remote", Remote: true, Fn: func(_ context.Context, _ Options, _ []Result) Result {
				return Result{Status: StatusPass, Message: "ok"}
			}},
		},
	}

	report := runner.Run(t.Context(), Options{})

	require.True(t, report.Remote, "any executed remote check should flip the Remote flag")
}

func TestRunner_Run_NilCheckFn_YieldsFailResult(t *testing.T) {
	t.Parallel()

	runner := &Runner{
		Checks: []Check{
			{ID: "1", Name: "malformed", Fn: nil},
		},
	}

	report := runner.Run(t.Context(), Options{})

	require.Len(t, report.Checks, 1)
	require.Equal(t, StatusFail, report.Checks[0].Status)
	require.Contains(t, report.Checks[0].Message, "internal error")
}

func TestRunner_Run_EmptyStatus_NormalizedToFail(t *testing.T) {
	t.Parallel()

	runner := &Runner{
		Checks: []Check{
			{ID: "1", Name: "buggy", Fn: func(_ context.Context, _ Options, _ []Result) Result {
				return Result{Message: "did not set status"}
			}},
		},
	}

	report := runner.Run(t.Context(), Options{})

	require.Equal(t, StatusFail, report.Checks[0].Status,
		"empty status must be normalized to Fail so the bug is visible in the report")
}

func TestRunner_Run_UnknownStatus_NormalizedToFail(t *testing.T) {
	t.Parallel()

	runner := &Runner{
		Checks: []Check{
			{ID: "1", Name: "typo-status", Fn: func(_ context.Context, _ Options, _ []Result) Result {
				// Status is `type Status string`, not a closed enum at the type-system level.
				// A typo (or any non-canonical value) must be normalized so it isn't dropped
				// from Summary aggregation and the exit code.
				return Result{Status: Status("passed"), Message: "real check would have meant pass"}
			}},
		},
	}

	report := runner.Run(t.Context(), Options{})

	require.Len(t, report.Checks, 1)
	require.Equal(t, StatusFail, report.Checks[0].Status, "non-canonical status must be normalized to Fail")
	require.Equal(t, "real check would have meant pass", report.Checks[0].Message,
		"existing non-empty Message must be preserved (the bug is in Status, not Message)")
	require.Equal(t, 1, report.Summary.Fail, "the normalized fail must count toward Summary")
	require.Equal(t, 1, ExitCode(report), "a single normalized fail must drive exit code 1")
}

func TestRunner_Run_UnknownStatus_EmptyMessage_AnnotatedWithInternalError(t *testing.T) {
	t.Parallel()

	runner := &Runner{
		Checks: []Check{
			{ID: "1", Name: "typo-status", Fn: func(_ context.Context, _ Options, _ []Result) Result {
				return Result{Status: Status("ok")}
			}},
		},
	}

	report := runner.Run(t.Context(), Options{})

	require.Equal(t, StatusFail, report.Checks[0].Status)
	require.Contains(t, report.Checks[0].Message, "internal error")
	require.Contains(t, report.Checks[0].Message, "ok",
		"the offending value should appear in the error message so the bug is debuggable from the report alone")
}

func TestRunner_Run_ContextCancelled_RemainingChecksSkipped(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())

	runner := &Runner{
		Checks: []Check{
			{ID: "1", Name: "first", Fn: func(_ context.Context, _ Options, _ []Result) Result {
				cancel()
				return Result{Status: StatusPass, Message: "ok"}
			}},
			{ID: "2", Name: "second", Fn: func(_ context.Context, _ Options, _ []Result) Result {
				return Result{Status: StatusPass, Message: "should not run"}
			}},
			{ID: "3", Name: "third", Fn: func(_ context.Context, _ Options, _ []Result) Result {
				return Result{Status: StatusPass, Message: "should not run"}
			}},
		},
	}

	report := runner.Run(ctx, Options{})

	require.Len(t, report.Checks, 3)
	require.Equal(t, StatusPass, report.Checks[0].Status)
	require.Equal(t, StatusSkip, report.Checks[1].Status)
	require.Equal(t, "cancelled", report.Checks[1].Message)
	require.Equal(t, StatusSkip, report.Checks[2].Status)
}

func TestRunner_Run_SummaryAggregation(t *testing.T) {
	t.Parallel()

	statuses := []Status{StatusPass, StatusPass, StatusWarn, StatusFail, StatusSkip}
	checks := make([]Check, 0, len(statuses))
	for i, s := range statuses {
		checks = append(checks, Check{
			ID:   string(rune('a' + i)),
			Name: "x",
			Fn:   func(_ context.Context, _ Options, _ []Result) Result { return Result{Status: s, Message: "x"} },
		})
	}

	runner := &Runner{Checks: checks}
	report := runner.Run(t.Context(), Options{})

	require.Equal(t, 2, report.Summary.Pass)
	require.Equal(t, 1, report.Summary.Warn)
	require.Equal(t, 1, report.Summary.Fail)
	require.Equal(t, 1, report.Summary.Skip)
}

func TestExitCode(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		report Report
		want   int
	}{
		{
			name: "any fail wins",
			report: Report{
				Checks:  []Result{{Status: StatusPass}, {Status: StatusFail}, {Status: StatusSkip}},
				Summary: Summary{Pass: 1, Fail: 1, Skip: 1},
			},
			want: 1,
		},
		{
			name: "all skip yields 2",
			report: Report{
				Checks:  []Result{{Status: StatusSkip}, {Status: StatusSkip}},
				Summary: Summary{Skip: 2},
			},
			want: 2,
		},
		{
			name:   "no checks yields 2",
			report: Report{Checks: nil, Summary: Summary{}},
			want:   2,
		},
		{
			name: "pass + skip mixed yields 0",
			report: Report{
				Checks:  []Result{{Status: StatusPass}, {Status: StatusSkip}},
				Summary: Summary{Pass: 1, Skip: 1},
			},
			want: 0,
		},
		{
			name: "pass + warn yields 0",
			report: Report{
				Checks:  []Result{{Status: StatusPass}, {Status: StatusWarn}},
				Summary: Summary{Pass: 1, Warn: 1},
			},
			want: 0,
		},
		{
			name: "warn-only yields 2",
			report: Report{
				Checks:  []Result{{Status: StatusWarn}},
				Summary: Summary{Warn: 1},
			},
			want: 2,
		},
		{
			name: "warn + skip without pass yields 2",
			report: Report{
				Checks:  []Result{{Status: StatusWarn}, {Status: StatusSkip}},
				Summary: Summary{Warn: 1, Skip: 1},
			},
			want: 2,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, ExitCode(tc.report))
		})
	}
}

func TestRunner_Run_UnredactedFlipsRedacted(t *testing.T) {
	t.Parallel()

	runner := &Runner{Checks: []Check{{ID: "1", Name: "x", Fn: func(_ context.Context, _ Options, _ []Result) Result {
		return Result{Status: StatusPass, Message: "ok"}
	}}}}

	report := runner.Run(t.Context(), Options{Unredacted: true})

	require.False(t, report.Redacted, "Unredacted true should flip Redacted to false")
}
