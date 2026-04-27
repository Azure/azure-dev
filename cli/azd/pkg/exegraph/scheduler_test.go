// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exegraph

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRun_SingleStep(t *testing.T) {
	g := NewGraph()
	var ran bool
	require.NoError(t, g.AddStep(&Step{
		Name: "a",
		Action: func(_ context.Context) error {
			ran = true
			return nil
		},
	}))
	require.NoError(t, Run(t.Context(), g, RunOptions{}))
	assert.True(t, ran)
}

func TestRun_EmptyGraph(t *testing.T) {
	g := NewGraph()
	assert.NoError(t, Run(t.Context(), g, RunOptions{}))
}

func TestRun_LinearChain(t *testing.T) {
	g := NewGraph()
	var order []string
	for _, name := range []string{"a", "b", "c"} {
		n := name
		deps := []string{}
		if n == "b" {
			deps = []string{"a"}
		} else if n == "c" {
			deps = []string{"b"}
		}
		require.NoError(t, g.AddStep(&Step{
			Name:      n,
			DependsOn: deps,
			Action: func(_ context.Context) error {
				order = append(order, n)
				return nil
			},
		}))
	}

	require.NoError(t, Run(t.Context(), g, RunOptions{}))
	require.Len(t, order, 3)
	assert.Equal(t, "a", order[0])
	assert.Equal(t, "b", order[1])
	assert.Equal(t, "c", order[2])
}

func TestRun_DiamondDeps(t *testing.T) {
	// A → B, A → C, B → D, C → D
	// B and C should run in parallel after A; D runs after both.
	g := NewGraph()
	var order []string
	var orderCh = make(chan string, 4)

	addStep := func(name string, deps []string, delay time.Duration) {
		require.NoError(t, g.AddStep(&Step{
			Name:      name,
			DependsOn: deps,
			Action: func(_ context.Context) error {
				time.Sleep(delay)
				orderCh <- name
				return nil
			},
		}))
	}

	addStep("a", nil, 10*time.Millisecond)
	addStep("b", []string{"a"}, 30*time.Millisecond)
	addStep("c", []string{"a"}, 30*time.Millisecond)
	addStep("d", []string{"b", "c"}, 10*time.Millisecond)

	require.NoError(t, Run(t.Context(), g, RunOptions{}))
	close(orderCh)

	for name := range orderCh {
		order = append(order, name)
	}

	require.Len(t, order, 4)
	assert.Equal(t, "a", order[0], "a must run first")
	assert.Equal(t, "d", order[3], "d must run last")
	// b and c can be in either order (parallel)
}

func TestRun_FanOut(t *testing.T) {
	// A → B, C, D, E (all depend on A, run in parallel)
	g := NewGraph()
	var concurrent atomic.Int32
	var maxConcurrent atomic.Int32

	require.NoError(t, g.AddStep(&Step{
		Name:   "a",
		Action: func(_ context.Context) error { return nil },
	}))

	for _, name := range []string{"b", "c", "d", "e"} {
		n := name
		require.NoError(t, g.AddStep(&Step{
			Name:      n,
			DependsOn: []string{"a"},
			Action: func(_ context.Context) error {
				cur := concurrent.Add(1)
				for {
					old := maxConcurrent.Load()
					if cur <= old || maxConcurrent.CompareAndSwap(old, cur) {
						break
					}
				}
				time.Sleep(50 * time.Millisecond)
				concurrent.Add(-1)
				return nil
			},
		}))
	}

	require.NoError(t, Run(t.Context(), g, RunOptions{}))
	assert.GreaterOrEqual(t, maxConcurrent.Load(), int32(2),
		"fan-out steps should run concurrently")
}

func TestRun_FailFast_CancelsRemaining(t *testing.T) {
	g := NewGraph()
	var bRan atomic.Bool

	require.NoError(t, g.AddStep(&Step{
		Name: "a",
		Action: func(_ context.Context) error {
			return errors.New("boom")
		},
	}))
	require.NoError(t, g.AddStep(&Step{
		Name: "b",
		Action: func(_ context.Context) error {
			bRan.Store(true)
			return nil
		},
	}))

	err := Run(t.Context(), g, RunOptions{ErrorPolicy: FailFast})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
	// b may or may not run (both have zero dependencies so both are eligible
	// immediately), but the error should propagate.
}

func TestRun_ContinueOnError_CollectsAll(t *testing.T) {
	g := NewGraph()

	require.NoError(t, g.AddStep(&Step{
		Name: "a",
		Action: func(_ context.Context) error {
			return errors.New("error-a")
		},
	}))
	require.NoError(t, g.AddStep(&Step{
		Name: "b",
		Action: func(_ context.Context) error {
			return errors.New("error-b")
		},
	}))

	err := Run(t.Context(), g, RunOptions{ErrorPolicy: ContinueOnError})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "error-a")
	assert.Contains(t, err.Error(), "error-b")
}

func TestRun_ContinueOnError_SkipsDependents(t *testing.T) {
	g := NewGraph()
	var bRan atomic.Bool

	require.NoError(t, g.AddStep(&Step{
		Name: "a",
		Action: func(_ context.Context) error {
			return errors.New("boom")
		},
	}))
	require.NoError(t, g.AddStep(&Step{
		Name:      "b",
		DependsOn: []string{"a"},
		Action: func(_ context.Context) error {
			bRan.Store(true)
			return nil
		},
	}))

	err := Run(t.Context(), g, RunOptions{ErrorPolicy: ContinueOnError})
	require.Error(t, err)
	assert.False(t, bRan.Load(), "b should be skipped because a failed")
	assert.Contains(t, err.Error(), "skipped")
}

// TestRun_ContinueOnError_TransitiveSkip verifies that when a step is skipped
// because its dependency failed, the skipped step's own dependents are also
// properly skipped rather than being silently dropped.
// Graph: a → b → c (chain of depth 3).
func TestRun_ContinueOnError_TransitiveSkip(t *testing.T) {
	g := NewGraph()
	var bRan, cRan atomic.Bool

	require.NoError(t, g.AddStep(&Step{
		Name: "a",
		Action: func(_ context.Context) error {
			return errors.New("boom")
		},
	}))
	require.NoError(t, g.AddStep(&Step{
		Name:      "b",
		DependsOn: []string{"a"},
		Action: func(_ context.Context) error {
			bRan.Store(true)
			return nil
		},
	}))
	require.NoError(t, g.AddStep(&Step{
		Name:      "c",
		DependsOn: []string{"b"},
		Action: func(_ context.Context) error {
			cRan.Store(true)
			return nil
		},
	}))

	err := Run(t.Context(), g, RunOptions{ErrorPolicy: ContinueOnError})
	require.Error(t, err)
	assert.False(t, bRan.Load(), "b should be skipped because a failed")
	assert.False(t, cRan.Load(), "c should be skipped because b was skipped")

	// Verify that both b and c are reported as skipped.
	errStr := err.Error()
	assert.Contains(t, errStr, `"b" skipped`)
	assert.Contains(t, errStr, `"c" skipped`)

	// Verify that skipped errors can be distinguished from real failures
	// using IsStepSkipped.
	var errs []error
	for _, e := range []error{err} {
		if joined, ok := e.(interface{ Unwrap() []error }); ok {
			errs = joined.Unwrap()
		}
	}
	var skipCount, failCount int
	for _, e := range errs {
		if IsStepSkipped(e) {
			skipCount++
		} else {
			failCount++
		}
	}
	assert.Equal(t, 2, skipCount, "b and c should produce StepSkippedError")
	assert.Equal(t, 1, failCount, "only a should be a real failure")
}

func TestStepSkippedError_Message(t *testing.T) {
	err := &StepSkippedError{StepName: "deploy-web"}
	assert.Contains(t, err.Error(), "deploy-web")
	assert.Contains(t, err.Error(), "skipped")
	assert.True(t, IsStepSkipped(err))
	assert.False(t, IsStepSkipped(errors.New("other error")))
}

func TestRun_ContinueOnError_OnStepDone_SkippedSteps(t *testing.T) {
	// Verify that OnStepDone receives a StepSkippedError for skipped steps,
	// allowing consumers to distinguish "failed" from "skipped".
	g := NewGraph()
	require.NoError(t, g.AddStep(&Step{
		Name:   "a",
		Action: func(_ context.Context) error { return errors.New("boom") },
	}))
	require.NoError(t, g.AddStep(&Step{
		Name:      "b",
		DependsOn: []string{"a"},
		Action:    func(_ context.Context) error { return nil },
	}))

	var doneEvents []struct {
		name    string
		skipped bool
	}
	opts := RunOptions{
		ErrorPolicy: ContinueOnError,
		OnStepDone: func(name string, err error) {
			doneEvents = append(doneEvents, struct {
				name    string
				skipped bool
			}{name: name, skipped: IsStepSkipped(err)})
		},
	}
	_ = Run(t.Context(), g, opts)

	require.Len(t, doneEvents, 2)
	// a should report as failed (not skipped)
	aEvent := doneEvents[0]
	assert.Equal(t, "a", aEvent.name)
	assert.False(t, aEvent.skipped, "a failed, not skipped")
	// b should report as skipped
	bEvent := doneEvents[1]
	assert.Equal(t, "b", bEvent.name)
	assert.True(t, bEvent.skipped, "b should be marked as skipped")
}

func TestRun_MaxConcurrency(t *testing.T) {
	g := NewGraph()
	var concurrent atomic.Int32
	var maxConcurrent atomic.Int32

	for i := range 6 {
		name := string(rune('a' + i))
		require.NoError(t, g.AddStep(&Step{
			Name: name,
			Action: func(_ context.Context) error {
				cur := concurrent.Add(1)
				for {
					old := maxConcurrent.Load()
					if cur <= old || maxConcurrent.CompareAndSwap(old, cur) {
						break
					}
				}
				time.Sleep(30 * time.Millisecond)
				concurrent.Add(-1)
				return nil
			},
		}))
	}

	require.NoError(t, Run(t.Context(), g, RunOptions{MaxConcurrency: 2}))
	assert.LessOrEqual(t, maxConcurrent.Load(), int32(2),
		"should not exceed max concurrency of 2")
}

func TestRun_ContextCancellation(t *testing.T) {
	g := NewGraph()
	ctx, cancel := context.WithCancel(t.Context())

	require.NoError(t, g.AddStep(&Step{
		Name: "a",
		Action: func(ctx context.Context) error {
			cancel()
			return nil
		},
	}))
	require.NoError(t, g.AddStep(&Step{
		Name:      "b",
		DependsOn: []string{"a"},
		Action: func(ctx context.Context) error {
			// This should get a canceled context
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(1 * time.Second):
				return errors.New("should not reach here")
			}
		},
	}))

	err := Run(ctx, g, RunOptions{})
	require.Error(t, err)
}

func TestRun_Callbacks(t *testing.T) {
	g := NewGraph()
	require.NoError(t, g.AddStep(&Step{
		Name:   "a",
		Action: func(_ context.Context) error { return nil },
	}))
	require.NoError(t, g.AddStep(&Step{
		Name:      "b",
		DependsOn: []string{"a"},
		Action:    func(_ context.Context) error { return nil },
	}))

	var started, done []string
	opts := RunOptions{
		OnStepStart: func(name string) { started = append(started, name) },
		OnStepDone:  func(name string, _ error) { done = append(done, name) },
	}

	require.NoError(t, Run(t.Context(), g, opts))
	assert.Equal(t, []string{"a", "b"}, started)
	assert.Equal(t, []string{"a", "b"}, done)
}

func TestRun_StepPanics(t *testing.T) {
	g := NewGraph()
	require.NoError(t, g.AddStep(&Step{
		Name: "a",
		Action: func(_ context.Context) error {
			panic("unexpected")
		},
	}))

	err := Run(t.Context(), g, RunOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "panicked")
}

func TestRun_CallbackPanicRecovery(t *testing.T) {
	// Verify that panics in OnStepStart and OnStepDone callbacks
	// do not crash the process. Both callbacks are wrapped with
	// panic recovery (safeNotifyStart/safeNotifyDone).
	t.Run("OnStepStart panic", func(t *testing.T) {
		g := NewGraph()
		require.NoError(t, g.AddStep(&Step{
			Name:   "a",
			Action: func(_ context.Context) error { return nil },
		}))

		// OnStepStart panics should be silently recovered —
		// the step itself still succeeds.
		assert.NotPanics(t, func() {
			err := Run(t.Context(), g, RunOptions{
				OnStepStart: func(string) { panic("start callback panic") },
				OnStepDone:  func(string, error) {},
			})
			assert.NoError(t, err)
		})
	})

	t.Run("OnStepDone panic", func(t *testing.T) {
		g := NewGraph()
		require.NoError(t, g.AddStep(&Step{
			Name:   "a",
			Action: func(_ context.Context) error { return nil },
		}))

		// OnStepDone panics should be silently recovered rather than
		// crashing the process.
		assert.NotPanics(t, func() {
			_ = Run(t.Context(), g, RunOptions{
				OnStepDone: func(string, error) { panic("done callback panic") },
			})
		})
	})
}

func TestRun_ValidationFailure(t *testing.T) {
	g := NewGraph()
	require.NoError(t, g.AddStep(&Step{
		Name:      "a",
		DependsOn: []string{"ghost"},
		Action:    func(_ context.Context) error { return nil },
	}))

	err := Run(t.Context(), g, RunOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "validation failed")
}

// --- Security-focused tests ---

func TestRun_NilGraph_ReturnsError(t *testing.T) {
	// Verify that passing a nil graph returns a descriptive error
	// rather than panicking with a nil-pointer dereference.
	err := Run(t.Context(), nil, RunOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "graph must not be nil")
}

func TestRun_PreCancelledContext(t *testing.T) {
	// When the context is already cancelled before Run starts, steps
	// should observe the cancellation and not block forever.
	g := NewGraph()
	require.NoError(t, g.AddStep(&Step{
		Name: "a",
		Action: func(ctx context.Context) error {
			return ctx.Err()
		},
	}))

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // cancel before Run

	err := Run(ctx, g, RunOptions{})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestRun_MaxConcurrency_One_IsSerial(t *testing.T) {
	// MaxConcurrency=1 must enforce strictly serial execution.
	g := NewGraph()
	var concurrent atomic.Int32
	var maxConcurrent atomic.Int32

	for i := range 4 {
		name := string(rune('a' + i))
		require.NoError(t, g.AddStep(&Step{
			Name: name,
			Action: func(_ context.Context) error {
				cur := concurrent.Add(1)
				for {
					old := maxConcurrent.Load()
					if cur <= old || maxConcurrent.CompareAndSwap(old, cur) {
						break
					}
				}
				time.Sleep(10 * time.Millisecond)
				concurrent.Add(-1)
				return nil
			},
		}))
	}

	require.NoError(t, Run(t.Context(), g, RunOptions{MaxConcurrency: 1}))
	assert.Equal(t, int32(1), maxConcurrent.Load(),
		"MaxConcurrency=1 must run at most one step at a time")
}

func TestRun_PanicPreservesMessage(t *testing.T) {
	// Verify that the panic message is captured in the error, not
	// silently swallowed. This is a security concern: hidden panics
	// could mask data-corruption bugs.
	g := NewGraph()
	require.NoError(t, g.AddStep(&Step{
		Name: "a",
		Action: func(_ context.Context) error {
			panic("data corruption detected")
		},
	}))

	err := Run(t.Context(), g, RunOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "panicked")
	assert.Contains(t, err.Error(), "data corruption detected",
		"panic recovery must preserve the original panic message")
}

func TestRun_PanicDoesNotBlockDependents(t *testing.T) {
	// If a step panics, its dependents should be properly skipped
	// (ContinueOnError) or the scheduler should shut down (FailFast),
	// not deadlock waiting for the panicked step.
	g := NewGraph()
	require.NoError(t, g.AddStep(&Step{
		Name: "panicker",
		Action: func(_ context.Context) error {
			panic("boom")
		},
	}))
	var bRan atomic.Bool
	require.NoError(t, g.AddStep(&Step{
		Name:      "dependent",
		DependsOn: []string{"panicker"},
		Action: func(_ context.Context) error {
			bRan.Store(true)
			return nil
		},
	}))

	err := Run(t.Context(), g, RunOptions{ErrorPolicy: ContinueOnError})
	require.Error(t, err)
	assert.False(t, bRan.Load(),
		"dependent of a panicked step should be skipped")
	assert.Contains(t, err.Error(), "skipped")
}

func TestRun_GoroutineCleanup(t *testing.T) {
	// After Run returns, no worker goroutines should be leaked.
	// We verify this indirectly by checking that all steps completed
	// and the function returned cleanly with multiple error scenarios.
	tests := []struct {
		name   string
		policy ErrorPolicy
		err    bool
	}{
		{"success", FailFast, false},
		{"failfast-error", FailFast, true},
		{"continue-error", ContinueOnError, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGraph()
			var count atomic.Int32
			for i := range 5 {
				name := string(rune('a' + i))
				shouldFail := tt.err && i == 0
				require.NoError(t, g.AddStep(&Step{
					Name: name,
					Action: func(_ context.Context) error {
						count.Add(1)
						if shouldFail {
							return errors.New("fail")
						}
						return nil
					},
				}))
			}
			err := Run(t.Context(), g, RunOptions{
				ErrorPolicy:    tt.policy,
				MaxConcurrency: 2,
			})
			if tt.err {
				require.Error(t, err)
			}
			// Run returned: all workers must have exited (workerWg.Wait
			// completed). If workers leaked, Run would hang.
		})
	}
}

// --- Per-step timeout tests ---

func TestRun_StepTimeout_CompletesWithin(t *testing.T) {
	g := NewGraph()
	require.NoError(t, g.AddStep(&Step{
		Name: "fast",
		Action: func(_ context.Context) error {
			time.Sleep(10 * time.Millisecond)
			return nil
		},
	}))

	err := Run(t.Context(), g, RunOptions{StepTimeout: 500 * time.Millisecond})
	assert.NoError(t, err)
}

func TestRun_StepTimeout_ExceedsDeadline(t *testing.T) {
	g := NewGraph()
	require.NoError(t, g.AddStep(&Step{
		Name: "slow",
		Action: func(ctx context.Context) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
				return nil
			}
		},
	}))

	err := Run(t.Context(), g, RunOptions{StepTimeout: 50 * time.Millisecond})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Contains(t, err.Error(), `"slow" failed`)
}

func TestRun_StepTimeout_ZeroMeansNoDeadline(t *testing.T) {
	// With StepTimeout=0 (the default), no per-step deadline should be
	// applied. A step that takes 100ms must succeed.
	g := NewGraph()
	require.NoError(t, g.AddStep(&Step{
		Name: "takes-a-bit",
		Action: func(_ context.Context) error {
			time.Sleep(100 * time.Millisecond)
			return nil
		},
	}))

	err := Run(t.Context(), g, RunOptions{StepTimeout: 0})
	assert.NoError(t, err)
}

func TestRun_StepTimeout_FailFast_CancelsDependents(t *testing.T) {
	g := NewGraph()
	var dependentRan atomic.Bool

	require.NoError(t, g.AddStep(&Step{
		Name: "slow",
		Action: func(ctx context.Context) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
				return nil
			}
		},
	}))
	require.NoError(t, g.AddStep(&Step{
		Name:      "dependent",
		DependsOn: []string{"slow"},
		Action: func(_ context.Context) error {
			dependentRan.Store(true)
			return nil
		},
	}))

	err := Run(t.Context(), g, RunOptions{
		StepTimeout: 50 * time.Millisecond,
		ErrorPolicy: FailFast,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.False(t, dependentRan.Load(),
		"dependent should not run after timeout failure with FailFast")
}

func TestRun_StepTimeout_ContinueOnError_SkipsDependents(t *testing.T) {
	// When a step times out under ContinueOnError, its dependents are
	// skipped but independent branches still run to completion.
	g := NewGraph()
	var dependentRan, independentRan atomic.Bool

	require.NoError(t, g.AddStep(&Step{
		Name: "slow",
		Action: func(ctx context.Context) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
				return nil
			}
		},
	}))
	require.NoError(t, g.AddStep(&Step{
		Name:      "dependent",
		DependsOn: []string{"slow"},
		Action: func(_ context.Context) error {
			dependentRan.Store(true)
			return nil
		},
	}))
	require.NoError(t, g.AddStep(&Step{
		Name: "independent",
		Action: func(_ context.Context) error {
			independentRan.Store(true)
			return nil
		},
	}))

	err := Run(t.Context(), g, RunOptions{
		StepTimeout: 50 * time.Millisecond,
		ErrorPolicy: ContinueOnError,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.False(t, dependentRan.Load(),
		"dependent of timed-out step should be skipped")
	assert.True(t, independentRan.Load(),
		"independent step should still run under ContinueOnError")
	assert.Contains(t, err.Error(), "skipped")
}

func TestRun_FailFast_ContextCancelledInFlight(t *testing.T) {
	// FailFast: when one step fails, in-flight steps receive a
	// cancelled context and the scheduler drains all completions
	// before returning. This tests the drain logic.
	g := NewGraph()
	var bStarted atomic.Bool

	require.NoError(t, g.AddStep(&Step{
		Name: "slow-fail",
		Action: func(_ context.Context) error {
			time.Sleep(20 * time.Millisecond)
			return errors.New("fail")
		},
	}))
	require.NoError(t, g.AddStep(&Step{
		Name: "slow-success",
		Action: func(ctx context.Context) error {
			bStarted.Store(true)
			// Block until context cancelled or timeout
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
				return nil
			}
		},
	}))

	err := Run(t.Context(), g, RunOptions{ErrorPolicy: FailFast})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fail")
	// The function must return, proving the drain completed.
}

// --- StepStatus and edge case tests ---

func TestStepStatus_String(t *testing.T) {
	tests := []struct {
		status StepStatus
		want   string
	}{
		{StepPending, "pending"},
		{StepRunning, "running"},
		{StepDone, "done"},
		{StepFailed, "failed"},
		{StepSkipped, "skipped"},
		{StepStatus(99), "unknown"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, tt.status.String())
	}
}

func TestRun_NegativeMaxConcurrency_TreatedAsUnlimited(t *testing.T) {
	// Negative MaxConcurrency should behave the same as zero (unlimited).
	// All four independent steps should run and complete.
	g := NewGraph()
	var count atomic.Int32

	for i := range 4 {
		name := string(rune('a' + i))
		require.NoError(t, g.AddStep(&Step{
			Name: name,
			Action: func(_ context.Context) error {
				count.Add(1)
				return nil
			},
		}))
	}

	err := Run(t.Context(), g, RunOptions{MaxConcurrency: -5})
	require.NoError(t, err)
	assert.Equal(t, int32(4), count.Load(), "all steps should run with negative MaxConcurrency")
}

func TestRunWithResult_CapturesStepTiming(t *testing.T) {
	g := NewGraph()
	require.NoError(t, g.AddStep(&Step{
		Name: "a",
		Tags: []string{"provision"},
		Action: func(_ context.Context) error {
			time.Sleep(10 * time.Millisecond)
			return nil
		},
	}))
	require.NoError(t, g.AddStep(&Step{
		Name:      "b",
		DependsOn: []string{"a"},
		Tags:      []string{"deploy"},
		Action: func(_ context.Context) error {
			time.Sleep(5 * time.Millisecond)
			return nil
		},
	}))

	result := RunWithResult(t.Context(), g, RunOptions{})
	require.NoError(t, result.Error)
	require.Len(t, result.Steps, 2)
	assert.True(t, result.TotalDuration > 0, "total duration should be positive")

	// Both steps should have timing data.
	for _, st := range result.Steps {
		assert.NotEmpty(t, st.Name)
		assert.Equal(t, StepDone, st.Status)
		assert.False(t, st.Start.IsZero())
		assert.False(t, st.End.IsZero())
		assert.True(t, st.Duration > 0, "step %s should have positive duration", st.Name)
		assert.NotEmpty(t, st.Tags)
		assert.NoError(t, st.Err)
	}
}

func TestRunWithResult_FailedStepRecordsTiming(t *testing.T) {
	g := NewGraph()
	wantErr := errors.New("boom")
	require.NoError(t, g.AddStep(&Step{
		Name: "fail",
		Action: func(_ context.Context) error {
			return wantErr
		},
	}))
	require.NoError(t, g.AddStep(&Step{
		Name:      "skip",
		DependsOn: []string{"fail"},
		Action:    func(_ context.Context) error { return nil },
	}))

	result := RunWithResult(t.Context(), g, RunOptions{ErrorPolicy: ContinueOnError})
	require.Error(t, result.Error)
	require.Len(t, result.Steps, 2)

	// Find each step by name.
	var failStep, skipStep *StepTiming
	for i := range result.Steps {
		switch result.Steps[i].Name {
		case "fail":
			failStep = &result.Steps[i]
		case "skip":
			skipStep = &result.Steps[i]
		}
	}

	require.NotNil(t, failStep)
	assert.Equal(t, StepFailed, failStep.Status)
	assert.Error(t, failStep.Err)

	require.NotNil(t, skipStep)
	assert.Equal(t, StepSkipped, skipStep.Status)
}

// --- Test 8: Parent context deadline canceling in-flight steps ---

func TestParentContextDeadline_CancelsInFlightSteps(t *testing.T) {
	g := NewGraph()
	var stepStarted atomic.Bool

	require.NoError(t, g.AddStep(&Step{
		Name: "long-running",
		Action: func(ctx context.Context) error {
			stepStarted.Store(true)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(10 * time.Second):
				return nil
			}
		},
	}))

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	result := RunWithResult(ctx, g, RunOptions{})
	require.Error(t, result.Error)

	// The step should have been started and then canceled.
	assert.True(t, stepStarted.Load(), "step should have started before deadline")

	// Result should contain the step with a canceled/skipped status.
	require.Len(t, result.Steps, 1)
	st := result.Steps[0]
	assert.Equal(t, "long-running", st.Name)
	// Parent context cancellation marks the step as skipped (scheduler-level cancel).
	assert.Equal(t, StepSkipped, st.Status,
		"parent deadline should cause the step to be recorded as skipped")
}

// --- Test 21: MaxConcurrency=1 doesn't panic ---

func TestMaxConcurrency1_DoesNotPanic(t *testing.T) {
	g := NewGraph()
	var order []string
	var mu sync.Mutex

	for _, name := range []string{"s1", "s2", "s3", "s4"} {
		n := name
		require.NoError(t, g.AddStep(&Step{
			Name: n,
			Action: func(_ context.Context) error {
				mu.Lock()
				order = append(order, n)
				mu.Unlock()
				time.Sleep(5 * time.Millisecond)
				return nil
			},
		}))
	}

	assert.NotPanics(t, func() {
		err := Run(t.Context(), g, RunOptions{MaxConcurrency: 1})
		require.NoError(t, err)
	})

	mu.Lock()
	defer mu.Unlock()
	assert.Len(t, order, 4, "all 4 steps must complete")
}

// --- Test 22: errors.Join aggregation under ContinueOnError ---

func TestContinueOnError_JoinsMultipleFailures(t *testing.T) {
	g := NewGraph()

	require.NoError(t, g.AddStep(&Step{
		Name:   "ok",
		Action: func(_ context.Context) error { return nil },
	}))
	require.NoError(t, g.AddStep(&Step{
		Name: "fail1",
		Action: func(_ context.Context) error {
			return errors.New("failure-alpha")
		},
	}))
	require.NoError(t, g.AddStep(&Step{
		Name: "fail2",
		Action: func(_ context.Context) error {
			return errors.New("failure-beta")
		},
	}))

	err := Run(t.Context(), g, RunOptions{ErrorPolicy: ContinueOnError})
	require.Error(t, err)

	// Both failure messages must appear in the joined error.
	assert.Contains(t, err.Error(), "failure-alpha")
	assert.Contains(t, err.Error(), "failure-beta")

	// Verify errors.Join structure: the error should unwrap to multiple errors.
	var joined interface{ Unwrap() []error }
	require.ErrorAs(t, err, &joined, "error should be a joined error")
	unwrapped := joined.Unwrap()
	assert.GreaterOrEqual(t, len(unwrapped), 2,
		"at least 2 errors from the 2 failed steps")
}
