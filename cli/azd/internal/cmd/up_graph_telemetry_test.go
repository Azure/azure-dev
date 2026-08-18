// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/exegraph"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// Regression coverage for issue #9054: the synthetic cmd.package / cmd.provision
// / cmd.deploy spans under `azd up` must reflect the real per-phase outcome
// rather than always reporting success. stepUpPhase, stepStarted, firstPhaseError,
// phaseOutcomeError, deployOutcomeError and deployPhaseSpanTiming are the pure
// building blocks the deferred span finalizer relies on, so they are asserted
// directly here.

func TestStepUpPhase(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		step exegraph.StepTiming
		want string
	}{
		{"provision tag", exegraph.StepTiming{Name: "provision-db", Tags: []string{"provision"}}, "provision"},
		{"package tag", exegraph.StepTiming{Name: "package-web", Tags: []string{"package"}}, "package"},
		{"deploy tag", exegraph.StepTiming{Name: "deploy-web", Tags: []string{"deploy"}}, "deploy"},
		{"publish tag collapses to deploy", exegraph.StepTiming{Name: "publish-web", Tags: []string{"publish"}}, "deploy"},
		{
			"tag wins over a service name that contains another phase word",
			exegraph.StepTiming{Name: "deploy-my-package-svc", Tags: []string{"deploy"}},
			"deploy",
		},
		{"preprovision hook by name", exegraph.StepTiming{Name: preProvisionHookStep}, "provision"},
		{"postprovision hook by name", exegraph.StepTiming{Name: postProvisionHookStep}, "provision"},
		{"prepackage event by name", exegraph.StepTiming{Name: prePackageEventStep}, "package"},
		{"postpackage hook by name", exegraph.StepTiming{Name: postPackageHookStep}, "package"},
		{"predeploy hook by name", exegraph.StepTiming{Name: preDeployHookStep}, "deploy"},
		{"postdeploy event by name", exegraph.StepTiming{Name: postDeployEventStep}, "deploy"},
		{"untagged service step is unclassified", exegraph.StepTiming{Name: "deploy-web"}, ""},
		{"unknown step", exegraph.StepTiming{Name: "restore-web"}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, stepUpPhase(tt.step))
		})
	}
}

func TestStepStarted(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		step exegraph.StepTiming
		want bool
	}{
		{"done ran", exegraph.StepTiming{Status: exegraph.StepDone}, true},
		{"failed ran", exegraph.StepTiming{Status: exegraph.StepFailed}, true},
		{
			"never-started skip did not run",
			exegraph.StepTiming{
				Status: exegraph.StepSkipped,
				Err:    &exegraph.StepSkippedError{StepName: "deploy-web"},
			},
			false,
		},
		{
			"in-flight cancel did run",
			exegraph.StepTiming{Status: exegraph.StepSkipped, Err: context.Canceled},
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, stepStarted(tt.step))
		})
	}
}

// TestFirstPhaseError asserts the generic phase root-cause selection over the
// two pick functions the phase span finalizer composes: genuinePhaseFailure
// (StepFailed only) and startedCancellation (an in-flight Ctrl+C/deadline). The
// higher-level phaseOutcomeError / deployOutcomeError wrappers are covered
// separately.
func TestFirstPhaseError(t *testing.T) {
	t.Parallel()

	provisionErr := errors.New("bicep deployment failed")
	packageErr := errors.New("docker build failed")
	deployErr := errors.New("zip deploy failed")
	publishErr := errors.New("image push failed")
	skipErr := &exegraph.StepSkippedError{StepName: "deploy-web"}

	tests := []struct {
		name   string
		result *exegraph.RunResult
		phase  string
		pick   func(exegraph.StepTiming) error
		want   error
	}{
		{
			name:   "nil result",
			result: nil,
			phase:  "provision",
			pick:   genuinePhaseFailure,
			want:   nil,
		},
		{
			name: "genuine provision failure is surfaced",
			result: &exegraph.RunResult{Steps: []exegraph.StepTiming{
				{Name: "provision-db", Status: exegraph.StepFailed, Tags: []string{"provision"}, Err: provisionErr},
			}},
			phase: "provision",
			pick:  genuinePhaseFailure,
			want:  provisionErr,
		},
		{
			name: "provision success returns nil",
			result: &exegraph.RunResult{Steps: []exegraph.StepTiming{
				{Name: "provision-db", Status: exegraph.StepDone, Tags: []string{"provision"}},
			}},
			phase: "provision",
			pick:  genuinePhaseFailure,
			want:  nil,
		},
		{
			name: "never-started provision skip is not blamed",
			result: &exegraph.RunResult{Steps: []exegraph.StepTiming{
				{Name: "provision-db", Status: exegraph.StepSkipped, Tags: []string{"provision"}, Err: skipErr},
			}},
			phase: "provision",
			pick:  genuinePhaseFailure,
			want:  nil,
		},
		{
			name: "in-flight provision cancel is not blamed under genuine pick",
			result: &exegraph.RunResult{Steps: []exegraph.StepTiming{
				{
					Name: "provision-db", Status: exegraph.StepSkipped,
					Tags: []string{"provision"}, Err: context.Canceled,
				},
			}},
			phase: "provision",
			pick:  genuinePhaseFailure,
			want:  nil,
		},
		{
			name: "unrelated phase failure is ignored",
			result: &exegraph.RunResult{Steps: []exegraph.StepTiming{
				{Name: "deploy-web", Status: exegraph.StepFailed, Tags: []string{"deploy"}, Err: deployErr},
			}},
			phase: "provision",
			pick:  genuinePhaseFailure,
			want:  nil,
		},
		{
			name: "package first failure in completion order wins",
			result: &exegraph.RunResult{Steps: []exegraph.StepTiming{
				{Name: "package-api", Status: exegraph.StepDone, Tags: []string{"package"}},
				{Name: "package-web", Status: exegraph.StepFailed, Tags: []string{"package"}, Err: packageErr},
				{Name: "package-worker", Status: exegraph.StepFailed, Tags: []string{"package"}, Err: deployErr},
			}},
			phase: "package",
			pick:  genuinePhaseFailure,
			want:  packageErr,
		},
		{
			name: "deploy surfaces genuine publish failure (publish collapses into deploy)",
			result: &exegraph.RunResult{Steps: []exegraph.StepTiming{
				{Name: "publish-web", Status: exegraph.StepFailed, Tags: []string{"publish"}, Err: publishErr},
			}},
			phase: "deploy",
			pick:  genuinePhaseFailure,
			want:  publishErr,
		},
		{
			name: "deploy surfaces an in-flight cancel (user Ctrl+C)",
			result: &exegraph.RunResult{Steps: []exegraph.StepTiming{
				{Name: "deploy-web", Status: exegraph.StepSkipped, Tags: []string{"deploy"}, Err: context.Canceled},
			}},
			phase: "deploy",
			pick:  startedCancellation,
			want:  context.Canceled,
		},
		{
			name: "deploy ignores a never-started skip",
			result: &exegraph.RunResult{Steps: []exegraph.StepTiming{
				{Name: "deploy-web", Status: exegraph.StepSkipped, Tags: []string{"deploy"}, Err: skipErr},
			}},
			phase: "deploy",
			pick:  startedCancellation,
			want:  nil,
		},
		{
			name: "deploy genuine failure precedes a later cancel",
			result: &exegraph.RunResult{Steps: []exegraph.StepTiming{
				{Name: "deploy-web", Status: exegraph.StepFailed, Tags: []string{"deploy"}, Err: deployErr},
				{Name: "deploy-api", Status: exegraph.StepSkipped, Tags: []string{"deploy"}, Err: context.Canceled},
			}},
			phase: "deploy",
			pick:  genuinePhaseFailure,
			want:  deployErr,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := firstPhaseError(tt.result, tt.phase, tt.pick)
			require.ErrorIs(t, got, tt.want)
		})
	}
}

// TestRunHadGenuineFailure asserts the run-level predicate the phase span
// finalizer uses to tell a real user cancellation apart from FailFast teardown:
// it is true only when some step failed outright (StepFailed), since the
// scheduler cancels in-flight steps only after a genuine failure.
func TestRunHadGenuineFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		result *exegraph.RunResult
		want   bool
	}{
		{"nil result", nil, false},
		{"empty run", &exegraph.RunResult{}, false},
		{
			name: "all done or skipped is not a genuine failure",
			result: &exegraph.RunResult{Steps: []exegraph.StepTiming{
				{Name: "provision-db", Status: exegraph.StepDone, Tags: []string{"provision"}},
				{Name: "deploy-web", Status: exegraph.StepSkipped, Tags: []string{"deploy"}, Err: context.Canceled},
			}},
			want: false,
		},
		{
			name: "a StepFailed anywhere is a genuine failure",
			result: &exegraph.RunResult{Steps: []exegraph.StepTiming{
				{Name: "package-web", Status: exegraph.StepFailed, Tags: []string{"package"}, Err: errors.New("boom")},
			}},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, runHadGenuineFailure(tt.result))
		})
	}
}

// TestPhaseOutcomeError covers the provision/package span attribution: a genuine
// phase failure is always surfaced; an in-flight cancellation is surfaced only
// when the run was aborted by the user (no other step genuinely failed), and is
// treated as FailFast teardown (nil) when some other phase actually failed.
func TestPhaseOutcomeError(t *testing.T) {
	t.Parallel()

	packageErr := errors.New("docker build failed")
	provisionErr := errors.New("bicep failed")

	tests := []struct {
		name   string
		result *exegraph.RunResult
		phase  string
		want   error
	}{
		{"nil result", nil, "package", nil},
		{
			name: "genuine package failure is surfaced",
			result: &exegraph.RunResult{Steps: []exegraph.StepTiming{
				{Name: "package-web", Status: exegraph.StepFailed, Tags: []string{"package"}, Err: packageErr},
			}},
			phase: "package",
			want:  packageErr,
		},
		{
			name: "user Ctrl+C while packaging is surfaced as the cancellation",
			result: &exegraph.RunResult{Steps: []exegraph.StepTiming{
				{Name: "package-web", Status: exegraph.StepSkipped, Tags: []string{"package"}, Err: context.Canceled},
			}},
			phase: "package",
			want:  context.Canceled,
		},
		{
			// A genuine provision failure tears down the in-flight package step;
			// that teardown must not be blamed on the package span.
			name: "package teardown by an upstream provision failure is not surfaced",
			result: &exegraph.RunResult{Steps: []exegraph.StepTiming{
				{Name: "provision-db", Status: exegraph.StepFailed, Tags: []string{"provision"}, Err: provisionErr},
				{Name: "package-web", Status: exegraph.StepSkipped, Tags: []string{"package"}, Err: context.Canceled},
			}},
			phase: "package",
			want:  nil,
		},
		{
			name: "never-started package skip is not surfaced",
			result: &exegraph.RunResult{Steps: []exegraph.StepTiming{
				{
					Name: "package-web", Status: exegraph.StepSkipped, Tags: []string{"package"},
					Err: &exegraph.StepSkippedError{StepName: "package-web"},
				},
			}},
			phase: "package",
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.ErrorIs(t, phaseOutcomeError(tt.result, tt.phase), tt.want)
		})
	}
}

// TestDeployOutcomeError covers the cmd.deploy span attribution: a genuine
// deploy failure always wins; otherwise an in-flight cancellation is surfaced
// only when no upstream provision/package failure tore the phase down.
func TestDeployOutcomeError(t *testing.T) {
	t.Parallel()

	deployErr := errors.New("zip deploy failed")

	tests := []struct {
		name           string
		result         *exegraph.RunResult
		upstreamFailed bool
		want           error
	}{
		{"nil result", nil, false, nil},
		{
			name: "genuine deploy failure is surfaced even when upstream also failed",
			result: &exegraph.RunResult{Steps: []exegraph.StepTiming{
				{Name: "deploy-web", Status: exegraph.StepFailed, Tags: []string{"deploy"}, Err: deployErr},
			}},
			upstreamFailed: true,
			want:           deployErr,
		},
		{
			name: "user Ctrl+C during deploy is surfaced when nothing upstream failed",
			result: &exegraph.RunResult{Steps: []exegraph.StepTiming{
				{Name: "deploy-web", Status: exegraph.StepSkipped, Tags: []string{"deploy"}, Err: context.Canceled},
			}},
			upstreamFailed: false,
			want:           context.Canceled,
		},
		{
			// FailFast teardown of the deploy phase after a provision/package
			// failure must leave cmd.deploy unblamed (that failure owns its span).
			name: "deploy teardown after an upstream failure is not surfaced",
			result: &exegraph.RunResult{Steps: []exegraph.StepTiming{
				{Name: "deploy-web", Status: exegraph.StepSkipped, Tags: []string{"deploy"}, Err: context.Canceled},
			}},
			upstreamFailed: true,
			want:           nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.ErrorIs(t, deployOutcomeError(tt.result, tt.upstreamFailed), tt.want)
		})
	}
}

func TestDeployPhaseSpanTiming(t *testing.T) {
	t.Parallel()

	base := time.Now()

	tests := []struct {
		name           string
		steps          []exegraph.StepTiming
		upstreamFailed bool
		wantRan        bool
		wantStart      time.Time
		wantEnd        time.Time
	}{
		{
			name:    "no deploy or publish steps",
			steps:   []exegraph.StepTiming{{Name: "provision", Status: exegraph.StepDone, Tags: []string{"provision"}}},
			wantRan: false,
		},
		{
			name: "all deploy steps skipped means phase did not run",
			steps: []exegraph.StepTiming{
				{Name: "publish-web", Status: exegraph.StepSkipped, Tags: []string{"publish"}},
				{Name: "deploy-web", Status: exegraph.StepSkipped, Tags: []string{"deploy"}},
			},
			wantRan: false,
		},
		{
			name: "single deploy step envelope",
			steps: []exegraph.StepTiming{
				{
					Name: "deploy-web", Status: exegraph.StepDone, Tags: []string{"deploy"},
					Start: base.Add(30 * time.Second), End: base.Add(90 * time.Second),
				},
			},
			wantRan:   true,
			wantStart: base.Add(30 * time.Second),
			wantEnd:   base.Add(90 * time.Second),
		},
		{
			name: "publish and deploy span the full envelope; skipped excluded",
			steps: []exegraph.StepTiming{
				{
					Name: "publish-web", Status: exegraph.StepDone, Tags: []string{"publish"},
					Start: base.Add(10 * time.Second), End: base.Add(40 * time.Second),
				},
				{
					Name: "deploy-web", Status: exegraph.StepFailed, Tags: []string{"deploy"},
					Start: base.Add(40 * time.Second), End: base.Add(75 * time.Second),
				},
				{
					Name: "deploy-api", Status: exegraph.StepSkipped, Tags: []string{"deploy"},
					Start: base, End: base.Add(500 * time.Second),
				},
			},
			wantRan:   true,
			wantStart: base.Add(10 * time.Second),
			wantEnd:   base.Add(75 * time.Second),
		},
		{
			name: "in-flight-canceled deploy step counts as ran",
			steps: []exegraph.StepTiming{
				{
					Name: "deploy-web", Status: exegraph.StepSkipped, Tags: []string{"deploy"},
					Err:   context.Canceled,
					Start: base.Add(20 * time.Second), End: base.Add(50 * time.Second),
				},
			},
			wantRan:   true,
			wantStart: base.Add(20 * time.Second),
			wantEnd:   base.Add(50 * time.Second),
		},
		{
			// A deploy lifecycle step canceled in-flight because the *user*
			// pressed Ctrl+C (no upstream provision/package failure) is a real
			// deploy-phase outcome and counts as ran.
			name: "in-flight-canceled deploy hook counts as ran when no upstream failure",
			steps: []exegraph.StepTiming{
				{
					Name: preDeployHookStep, Status: exegraph.StepSkipped, Err: context.Canceled,
					Start: base.Add(20 * time.Second), End: base.Add(35 * time.Second),
				},
			},
			upstreamFailed: false,
			wantRan:        true,
			wantStart:      base.Add(20 * time.Second),
			wantEnd:        base.Add(35 * time.Second),
		},
		{
			// #9054 flip side: when a package/provision step genuinely failed,
			// FailFast tears down an overlapping predeploy hook (StepSkipped +
			// context.Canceled). That teardown must NOT count as a deploy run, or
			// `up` would emit a spurious FAILED cmd.deploy blamed on the deploy
			// phase for a failure that belongs to package/provision.
			name: "in-flight-canceled deploy hook does not count when upstream failed",
			steps: []exegraph.StepTiming{
				{
					Name: preDeployHookStep, Status: exegraph.StepSkipped, Err: context.Canceled,
					Start: base.Add(20 * time.Second), End: base.Add(35 * time.Second),
				},
			},
			upstreamFailed: true,
			wantRan:        false,
		},
		{
			// #9054 regression guard: the predeploy hook is gated only on
			// provisioning, so it can finish before a *failing* package step. A
			// lone successful lifecycle hook must therefore NOT mark the deploy
			// phase as run, or `up` would emit a spurious Success cmd.deploy for
			// a run that never actually deployed.
			name: "successful pre-deploy hook alone does not count as ran",
			steps: []exegraph.StepTiming{
				{
					Name: preDeployHookStep, Status: exegraph.StepDone,
					Start: base.Add(5 * time.Second), End: base.Add(12 * time.Second),
				},
			},
			wantRan: false,
		},
		{
			// A *failing* pre/post-deploy hook is a real deploy-phase outcome, so
			// it counts as ran and is surfaced on cmd.deploy.
			name: "failing pre-deploy hook counts as ran",
			steps: []exegraph.StepTiming{
				{
					Name: preDeployHookStep, Status: exegraph.StepFailed,
					Err:   errors.New("predeploy hook failed"),
					Start: base.Add(5 * time.Second), End: base.Add(12 * time.Second),
				},
			},
			wantRan:   true,
			wantStart: base.Add(5 * time.Second),
			wantEnd:   base.Add(12 * time.Second),
		},
		{
			// A successful hook contributes to the envelope, but it is the
			// *service* deploy that makes the phase count as run.
			name: "successful hook plus deploy service counts as ran",
			steps: []exegraph.StepTiming{
				{
					Name: preDeployHookStep, Status: exegraph.StepDone,
					Start: base.Add(5 * time.Second), End: base.Add(12 * time.Second),
				},
				{
					Name: "deploy-web", Status: exegraph.StepDone, Tags: []string{"deploy"},
					Start: base.Add(15 * time.Second), End: base.Add(60 * time.Second),
				},
			},
			wantRan:   true,
			wantStart: base.Add(5 * time.Second),
			wantEnd:   base.Add(60 * time.Second),
		},
		{
			// Infra-only project (no services): every deploy lifecycle node runs
			// to completion but none is a service step. The terminal deploy node
			// (cmdhook-postdeploy) completing means the whole pipeline finished,
			// so `up` still records a cmd.deploy — matching legacy `azd up`, which
			// always invoked `azd deploy --all` even with nothing to deploy.
			name: "infra-only run with terminal post-deploy hook counts as ran",
			steps: []exegraph.StepTiming{
				{
					Name: preDeployHookStep, Status: exegraph.StepDone,
					Start: base.Add(5 * time.Second), End: base.Add(10 * time.Second),
				},
				{
					Name: postDeployHookStep, Status: exegraph.StepDone,
					Start: base.Add(11 * time.Second), End: base.Add(14 * time.Second),
				},
			},
			wantRan:   true,
			wantStart: base.Add(5 * time.Second),
			wantEnd:   base.Add(14 * time.Second),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			start, end, ran := deployPhaseSpanTiming(tt.steps, tt.upstreamFailed)
			assert.Equal(t, tt.wantRan, ran)
			if tt.wantRan {
				assert.True(t, tt.wantStart.Equal(start), "start = %s, want %s", start, tt.wantStart)
				assert.True(t, tt.wantEnd.Equal(end), "end = %s, want %s", end, tt.wantEnd)
			}
		})
	}
}

// findSpan returns the first recorded span with the given name, or nil.
func findSpan(spans []tracesdk.ReadOnlySpan, name string) tracesdk.ReadOnlySpan {
	for _, s := range spans {
		if s.Name() == name {
			return s
		}
	}
	return nil
}

// The azd tracing package caches its Tracer at package-init from the global
// OpenTelemetry provider, and that global only honors the first
// SetTracerProvider call for the life of the process. Every span test in this
// package must therefore share a single recorder rather than install its own.
var (
	deploySpanRecorderOnce sync.Once
	deploySpanRecorder     *tracetest.SpanRecorder
)

// recordDeploySpans installs the shared in-memory span recorder on first use and
// returns a snapshot function that yields only the spans ended after this call,
// so a test observes just the spans it emitted. Callers must not run in parallel
// (the baseline slice is not synchronized).
func recordDeploySpans(t *testing.T) func() []tracesdk.ReadOnlySpan {
	t.Helper()
	deploySpanRecorderOnce.Do(func() {
		deploySpanRecorder = tracetest.NewSpanRecorder()
		tp := tracesdk.NewTracerProvider(tracesdk.WithSpanProcessor(deploySpanRecorder))
		otel.SetTracerProvider(tp)
	})
	baseline := len(deploySpanRecorder.Ended())
	return func() []tracesdk.ReadOnlySpan {
		return deploySpanRecorder.Ended()[baseline:]
	}
}

// TestEmitDeploySpan_ErrorPath is the end-to-end assertion issue #9054 was
// missing: when the deploy phase fails under `azd up`, a cmd.deploy span must
// actually be emitted, carry an Error status with a non-empty ResultCode, and be
// time-boxed to the real deploy phase. It installs an in-memory tracer provider
// to capture the span emitted through tracing.Start.
func TestEmitDeploySpan_ErrorPath(t *testing.T) {
	// Not parallel: shares one process-wide tracer provider (see recordDeploySpans).
	newSpans := recordDeploySpans(t)

	base := time.Now()
	start := base.Add(30 * time.Second)
	end := base.Add(95 * time.Second)
	deployErr := errors.New("zip deploy failed")

	result := &exegraph.RunResult{Steps: []exegraph.StepTiming{
		{
			Name: "deploy-web", Status: exegraph.StepFailed, Tags: []string{"deploy"},
			Err: deployErr, Start: start, End: end,
		},
	}}

	emitDeploySpan(t.Context(), result, nil, nil)

	span := findSpan(newSpans(), "cmd.deploy")
	require.NotNil(t, span, "cmd.deploy span must be emitted when the deploy phase fails")
	assert.Equal(t, codes.Error, span.Status().Code)
	assert.NotEmpty(t, span.Status().Description, "failure must carry a ResultCode")
	assert.True(t, start.Equal(span.StartTime()), "start = %s, want %s", span.StartTime(), start)
	assert.True(t, end.Equal(span.EndTime()), "end = %s, want %s", span.EndTime(), end)
}

// TestEmitDeploySpan_OmittedWhenDeployDidNotRun verifies the other half of the
// contract: when provisioning fails first and the deploy phase is skipped before
// it starts, no cmd.deploy span is emitted — matching legacy `azd up`, where the
// deploy sub-command never ran.
func TestEmitDeploySpan_OmittedWhenDeployDidNotRun(t *testing.T) {
	// Not parallel: shares one process-wide tracer provider (see recordDeploySpans).
	newSpans := recordDeploySpans(t)

	result := &exegraph.RunResult{Steps: []exegraph.StepTiming{
		{
			Name: "provision-db", Status: exegraph.StepFailed,
			Tags: []string{"provision"}, Err: errors.New("bicep failed"),
		},
		{
			Name:   "deploy-web",
			Status: exegraph.StepSkipped,
			Tags:   []string{"deploy"},
			Err:    &exegraph.StepSkippedError{StepName: "deploy-web"},
		},
	}}

	emitDeploySpan(t.Context(), result, nil, nil)

	assert.Nil(t, findSpan(newSpans(), "cmd.deploy"), "no cmd.deploy span when the deploy phase never ran")
}

// TestEmitDeploySpan_OmittedWhenOnlyDeployHookSucceeded is the direct #9054
// regression guard for the concurrency window: the predeploy hook depends only
// on provisioning, so it can *succeed* before a later package step *fails*. That
// lone successful lifecycle hook must not cause a Success cmd.deploy to be
// emitted for a run whose services were never deployed.
func TestEmitDeploySpan_OmittedWhenOnlyDeployHookSucceeded(t *testing.T) {
	// Not parallel: shares one process-wide tracer provider (see recordDeploySpans).
	newSpans := recordDeploySpans(t)

	result := &exegraph.RunResult{Steps: []exegraph.StepTiming{
		{Name: preDeployHookStep, Status: exegraph.StepDone},
		{
			Name: "package-web", Status: exegraph.StepFailed,
			Tags: []string{"package"}, Err: errors.New("docker build failed"),
		},
		{
			Name:   "deploy-web",
			Status: exegraph.StepSkipped,
			Tags:   []string{"deploy"},
			Err:    &exegraph.StepSkippedError{StepName: "deploy-web"},
		},
	}}

	emitDeploySpan(t.Context(), result, nil, nil)

	assert.Nil(
		t, findSpan(newSpans(), "cmd.deploy"),
		"a successful predeploy hook alone must not emit cmd.deploy when services never deployed",
	)
}

// TestEmitDeploySpan_OmittedWhenDeployHookCanceledByUpstreamFailure is the flip
// side of the #9054 fix: when a package step genuinely fails while the predeploy
// hook is in-flight, FailFast tears the hook down (StepSkipped + context.Canceled).
// That teardown must not surface as a spurious FAILED cmd.deploy — the failure
// belongs to the package phase, which reports it on cmd.package.
func TestEmitDeploySpan_OmittedWhenDeployHookCanceledByUpstreamFailure(t *testing.T) {
	// Not parallel: shares one process-wide tracer provider (see recordDeploySpans).
	newSpans := recordDeploySpans(t)

	result := &exegraph.RunResult{Steps: []exegraph.StepTiming{
		{
			Name: "package-web", Status: exegraph.StepFailed,
			Tags: []string{"package"}, Err: errors.New("docker build failed"),
		},
		{Name: preDeployHookStep, Status: exegraph.StepSkipped, Err: context.Canceled},
		{
			Name:   "deploy-web",
			Status: exegraph.StepSkipped,
			Tags:   []string{"deploy"},
			Err:    &exegraph.StepSkippedError{StepName: "deploy-web"},
		},
	}}

	emitDeploySpan(t.Context(), result, nil, nil)

	assert.Nil(
		t, findSpan(newSpans(), "cmd.deploy"),
		"deploy teardown from an upstream package failure must not emit a cmd.deploy span",
	)
}

// TestEmitDeploySpan_InfraOnlyProjectEmitsSuccess covers an infra-only `azd up`
// (no services): the deploy phase has no service steps, but the whole pipeline
// completes through the terminal post-deploy hook. Legacy `azd up` always ran
// `azd deploy --all` even then, so a Success cmd.deploy must still be recorded.
func TestEmitDeploySpan_InfraOnlyProjectEmitsSuccess(t *testing.T) {
	// Not parallel: shares one process-wide tracer provider (see recordDeploySpans).
	newSpans := recordDeploySpans(t)

	base := time.Now()
	result := &exegraph.RunResult{Steps: []exegraph.StepTiming{
		{
			Name: preDeployHookStep, Status: exegraph.StepDone,
			Start: base.Add(5 * time.Second), End: base.Add(10 * time.Second),
		},
		{
			Name: postDeployHookStep, Status: exegraph.StepDone,
			Start: base.Add(11 * time.Second), End: base.Add(14 * time.Second),
		},
	}}

	emitDeploySpan(t.Context(), result, nil, nil)

	span := findSpan(newSpans(), "cmd.deploy")
	require.NotNil(t, span, "infra-only `azd up` must still record a cmd.deploy span")
	assert.NotEqual(t, codes.Error, span.Status().Code, "a clean infra-only run must not be an error")
	assert.True(t, base.Add(5*time.Second).Equal(span.StartTime()))
	assert.True(t, base.Add(14*time.Second).Equal(span.EndTime()))
}
