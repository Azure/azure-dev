// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exegraph

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log"
	"runtime"
	"runtime/debug"
	"slices"
	"sync"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/events"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
)

// ErrorPolicy controls how the scheduler handles step failures.
type ErrorPolicy int

const (
	// FailFast cancels all in-flight steps when the first step fails.
	FailFast ErrorPolicy = iota

	// ContinueOnError lets all independent steps run to completion and
	// aggregates errors at the end. Steps whose dependencies failed are skipped.
	ContinueOnError
)

// String returns the string representation of the ErrorPolicy.
func (p ErrorPolicy) String() string {
	switch p {
	case FailFast:
		return "fail_fast"
	case ContinueOnError:
		return "continue_on_error"
	default:
		return "unknown"
	}
}

// RunOptions configures the execution graph scheduler.
type RunOptions struct {
	// MaxConcurrency limits the number of steps running simultaneously.
	// Zero (the default) caps at GOMAXPROCS×2.
	MaxConcurrency int

	// ErrorPolicy determines behavior on step failure.
	ErrorPolicy ErrorPolicy

	// StepTimeout imposes a per-step deadline. When positive, each step's
	// context is wrapped with context.WithTimeout before execution. If the
	// step does not complete within the duration the context expires with
	// context.DeadlineExceeded, which the scheduler treats as a normal step
	// failure (respecting ErrorPolicy). Zero (the default) means no per-step
	// timeout is applied.
	StepTimeout time.Duration

	// OnStepStart is called (if non-nil) when a step begins execution.
	// It is invoked from worker goroutines and must be safe for concurrent use.
	OnStepStart func(stepName string)

	// OnStepDone is called (if non-nil) when a step finishes, with a nil error on success.
	// It is invoked from worker goroutines and must be safe for concurrent use.
	OnStepDone func(stepName string, err error)
}

// Run executes all steps in the graph respecting dependency order, with bounded
// concurrency. It returns nil if all steps succeed, or a
// combined error describing all failures.
//
// The returned error may contain both genuine step failures and [StepSkippedError]
// values (for steps whose dependencies failed). Use [RunWithResult] when you need
// to distinguish failures from transitive skips via [RunResult.Steps].
func Run(ctx context.Context, g *Graph, opts RunOptions) (err error) {
	result := RunWithResult(ctx, g, opts)
	return result.Error
}

// RunWithResult executes all steps in the graph and returns a [RunResult]
// containing per-step timing, status, and aggregate error. This is the
// instrumented variant of [Run] for callers that need execution telemetry.
func RunWithResult(ctx context.Context, g *Graph, opts RunOptions) (result *RunResult) {
	result = &RunResult{}

	ctx, span := tracing.Start(ctx, events.ExeGraphRunEvent)
	defer func() { span.EndWithStatus(result.Error) }()

	if g == nil {
		result.Error = errors.New("exegraph: graph must not be nil")
		return result
	}

	span.SetAttributes(
		fields.ExeGraphStepCountKey.Int(g.Len()),
		fields.ExeGraphMaxConcurrencyKey.Int(opts.MaxConcurrency),
		fields.ExeGraphErrorPolicyKey.String(opts.ErrorPolicy.String()),
	)

	if g.Len() == 0 {
		return result
	}

	if err := g.Validate(); err != nil {
		result.Error = fmt.Errorf("graph validation failed: %w", err)
		return result
	}

	result = execute(ctx, g, opts)
	return result
}

// execute implements an event-driven scheduler with a bounded worker pool.
// Steps are dispatched as soon as all their predecessors complete, eliminating
// the head-of-line blocking of a phase-based approach. A fixed pool of worker
// goroutines pulls ready steps from a queue, executes them, and reports
// completion back to a single coordinator goroutine that updates in-degrees
// and enqueues newly unblocked successors.
func execute(ctx context.Context, g *Graph, opts RunOptions) *RunResult {
	n := g.Len()
	result := &RunResult{
		Steps: make([]StepTiming, 0, n),
	}
	var timingMu sync.Mutex

	// Build in-degree map and reverse adjacency list (step → dependents).
	inDegree := make(map[string]int, n)
	dependents := make(map[string][]string, n)
	for _, s := range g.steps {
		if _, ok := inDegree[s.Name]; !ok {
			inDegree[s.Name] = 0
		}
		for _, dep := range s.DependsOn {
			inDegree[s.Name]++
			dependents[dep] = append(dependents[dep], s.Name)
		}
	}

	// Track failed steps for dependency-skip logic.
	failed := make(map[string]bool, n)
	var allErrors []error

	// Derive a cancellable context for FailFast tear-down.
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()

	// Worker pool size: MaxConcurrency if set, otherwise cap at
	// GOMAXPROCS*2 to avoid unbounded goroutine creation.
	numWorkers := min(n, runtime.GOMAXPROCS(0)*2)
	if opts.MaxConcurrency > 0 && opts.MaxConcurrency < numWorkers {
		numWorkers = opts.MaxConcurrency
	}

	type stepCompletion struct {
		name  string
		err   error
		start time.Time
		end   time.Time
	}

	workQueue := make(chan string, n)
	completions := make(chan stepCompletion, n)

	// Start bounded worker pool.
	var workerWg sync.WaitGroup
	for range numWorkers {
		workerWg.Go(func() {
			for name := range workQueue {
				// Skip execution if the run has been canceled (FailFast or parent
				// context). Without this, queued steps start with a canceled context
				// and waste time before discovering the cancellation.
				if err := runCtx.Err(); err != nil {
					// Emit lifecycle callbacks so consumers tracking step state see
					// a terminal event for every queued step (parity with the drain
					// loop's StepSkipped cascade below).
					safeNotifyStart(opts, name)
					safeNotifyDone(opts, name, err)
					now := time.Now()
					completions <- stepCompletion{name: name, err: err, start: now, end: now}
					continue
				}
				step := g.steps[name]
				start := time.Now()
				err := runStep(runCtx, step, opts)
				end := time.Now()
				completions <- stepCompletion{name: name, err: err, start: start, end: end}
			}
		})
	}

	runStart := time.Now()

	// Seed ready queue with zero in-degree steps, sorted by transitive
	// dependent count descending (critical-path heuristic). Steps with more
	// downstream dependents start first, reducing overall wall-clock time
	// when parallelism is bounded.
	inflight := 0
	priorityOrder := g.priorityOrder()
	for _, name := range priorityOrder {
		if inDegree[name] == 0 {
			workQueue <- name
			inflight++
		}
	}

	// Event loop: process completions as they arrive. Each completion may
	// unblock successors whose in-degree drops to zero.
	for inflight > 0 {
		comp := <-completions
		inflight--

		// Record step timing.
		status := StepDone
		if comp.err != nil {
			switch {
			case IsStepSkipped(comp.err):
				status = StepSkipped
			case (errors.Is(comp.err, context.Canceled) || errors.Is(comp.err, context.DeadlineExceeded)) &&
				runCtx.Err() != nil:
				// Parent cancellation or FailFast tear-down from another step's
				// failure — never ran this Action to completion on its own.
				// Record as skipped. A per-step timeout (StepTimeout) where
				// runCtx is NOT canceled is still a real failure.
				status = StepSkipped
			default:
				status = StepFailed
			}
		}
		timingMu.Lock()
		result.Steps = append(result.Steps, StepTiming{
			Name:     comp.name,
			Status:   status,
			Start:    comp.start,
			End:      comp.end,
			Duration: comp.end.Sub(comp.start),
			Tags:     g.steps[comp.name].Tags,
			Err:      comp.err,
		})
		timingMu.Unlock()

		if comp.err != nil {
			// Align aggregation with the drain loop below: steps canceled by
			// the scheduler (parent ctx or FailFast tear-down) are not real
			// failures — don't add them to allErrors and don't mark them as
			// failed. A per-step StepTimeout where runCtx is NOT canceled is
			// still a genuine failure.
			isSchedulerCancel := (errors.Is(comp.err, context.Canceled) ||
				errors.Is(comp.err, context.DeadlineExceeded)) && runCtx.Err() != nil
			if !isSchedulerCancel {
				failed[comp.name] = true
				allErrors = append(allErrors, comp.err)
			}

			if !isSchedulerCancel && opts.ErrorPolicy == FailFast {
				runCancel()
				// Drain remaining in-flight work.
				for inflight > 0 {
					r := <-completions
					inflight--
					var drainStatus StepStatus
					switch {
					case r.err == nil:
						drainStatus = StepDone
					case errors.Is(r.err, context.Canceled) || errors.Is(r.err, context.DeadlineExceeded):
						// Steps cancelled by the scheduler's own FailFast cancellation
						// are recorded as skipped, not failed — they never ran their Action.
						drainStatus = StepSkipped
					default:
						drainStatus = StepFailed
						allErrors = append(allErrors, r.err)
					}
					timingMu.Lock()
					result.Steps = append(result.Steps, StepTiming{
						Name:     r.name,
						Status:   drainStatus,
						Start:    r.start,
						End:      r.end,
						Duration: r.end.Sub(r.start),
						Tags:     g.steps[r.name].Tags,
						Err:      r.err,
					})
					timingMu.Unlock()
				}
				break
			}
		}

		// Decrement in-degrees of dependents; enqueue newly ready steps.
		//
		// When a step succeeds, iterate dependents read-only (no allocation).
		// When a step fails under ContinueOnError, skip propagation may append
		// to the queue — use a clone to avoid aliasing the shared slice.
		//
		// SAFETY: This loop runs in the single coordinator goroutine. The
		// `queue = append(queue, ...)` pattern (growing the slice while
		// iterating by index) is safe here because there is no concurrent
		// access. Do not refactor to parallel processing without replacing
		// this with a proper work-stealing queue.
		deps := dependents[comp.name]
		needsClone := false
		var readyBatch []string // collect newly ready steps for priority sorting
		for i := 0; i < len(deps); i++ {
			dep := deps[i]
			inDegree[dep]--
			if inDegree[dep] == 0 {
				if hasFailedDep(g.steps[dep], failed) {
					failed[dep] = true
					skipErr := &StepSkippedError{StepName: dep}
					allErrors = append(allErrors, skipErr)
					safeNotifyDone(opts, dep, skipErr)

					// Record skipped step timing (zero duration).
					now := time.Now()
					timingMu.Lock()
					result.Steps = append(result.Steps, StepTiming{
						Name:   dep,
						Status: StepSkipped,
						Start:  now,
						End:    now,
						Tags:   g.steps[dep].Tags,
						Err:    skipErr,
					})
					timingMu.Unlock()

					// Cascade: process skipped step's dependents too.
					// Clone on first mutation to avoid aliasing the shared dependents slice.
					if !needsClone {
						deps = slices.Clone(deps)
						needsClone = true
					}
					deps = append(deps, dependents[dep]...)
				} else {
					readyBatch = append(readyBatch, dep)
				}
			}
		}

		// Sort newly ready steps by priority (critical-path first).
		// Stable sort so ties are deterministic across runs (tests rely on
		// this, and users benefit from reproducible scheduling order).
		if len(readyBatch) > 1 {
			slices.SortStableFunc(readyBatch, func(a, b string) int {
				return cmp.Compare(g.Priority(b), g.Priority(a))
			})
		}
		for _, name := range readyBatch {
			workQueue <- name
			inflight++
		}
	}

	close(workQueue)
	workerWg.Wait()

	result.TotalDuration = time.Since(runStart)

	// Post-execution completeness check: verify every step was
	// either executed (completed/failed) or skipped. A corrupted in-degree map
	// or a missed dependency edge would leave steps permanently pending with no
	// way to detect the bug at runtime. This is a read-only safety net.
	//
	// Skip when the run was aborted (FailFast or parent cancel) — unreachable
	// steps with inDegree > 0 are expected in that case.
	if runCtx.Err() == nil {
		resolved := 0
		for _, name := range g.order {
			if inDegree[name] == 0 {
				resolved++
			}
		}
		if resolved != n {
			var stuckNames []string
			for _, name := range g.order {
				if inDegree[name] > 0 {
					stuckNames = append(stuckNames, fmt.Sprintf("%s(in-degree=%d)", name, inDegree[name]))
				}
			}
			allErrors = append(allErrors, fmt.Errorf(
				"exegraph: %d of %d steps never became ready; this indicates a scheduler bug: %v",
				n-resolved, n, stuckNames,
			))
		}
	}

	// If the parent ctx was canceled (user ctrl-C, deploy timeout, etc.) and
	// no step-level failures were recorded, surface the cancellation once at
	// the run boundary so callers see a non-nil RunResult.Error / Run() error.
	// Individual canceled steps are NOT added to allErrors (see the main loop
	// above) to avoid duplicating `context.Canceled` for every in-flight step.
	if runCtx.Err() != nil && ctx.Err() != nil && len(allErrors) == 0 {
		allErrors = append(allErrors, ctx.Err())
	}

	result.Error = errors.Join(allErrors...)
	return result
}

// runStep executes a single step with panic recovery, tracing, and callbacks.
func runStep(ctx context.Context, step *Step, opts RunOptions) (stepErr error) {
	ctx, span := tracing.Start(ctx, events.ExeGraphStepEvent)

	span.SetAttributes(fields.ExeGraphStepNameKey.String(step.Name))
	if len(step.DependsOn) > 0 {
		span.SetAttributes(fields.ExeGraphStepDepsKey.StringSlice(step.DependsOn))
	}
	if len(step.Tags) > 0 {
		span.SetAttributes(fields.ExeGraphStepTagsKey.StringSlice(step.Tags))
	}

	if opts.StepTimeout > 0 {
		span.SetAttributes(fields.ExeGraphStepTimeoutKey.Int(int(opts.StepTimeout.Seconds())))
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.StepTimeout)
		defer cancel()
	}

	defer func() {
		if r := recover(); r != nil {
			stepErr = fmt.Errorf("step %q panicked: %v\n%s", step.Name, r, debug.Stack())
		}
		span.EndWithStatus(stepErr)
		safeNotifyDone(opts, step.Name, stepErr)
	}()

	safeNotifyStart(opts, step.Name)

	if err := step.Action(ctx); err != nil {
		return fmt.Errorf("step %q failed: %w", step.Name, err)
	}
	return nil
}

// hasFailedDep checks if any of a step's dependencies are in the failed set.
func hasFailedDep(s *Step, failed map[string]bool) bool {
	for _, dep := range s.DependsOn {
		if failed[dep] {
			return true
		}
	}
	return false
}

func notifyStart(opts RunOptions, name string) {
	if opts.OnStepStart != nil {
		opts.OnStepStart(name)
	}
}

func notifyDone(opts RunOptions, name string, err error) {
	if opts.OnStepDone != nil {
		opts.OnStepDone(name, err)
	}
}

// safeNotifyStart wraps notifyStart with panic recovery. Without this, a
// panicking OnStepStart callback kills the worker goroutine and deadlocks
// the scheduler.
func safeNotifyStart(opts RunOptions, name string) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("exegraph: callback panic for %q: %v", name, r)
		}
	}()
	notifyStart(opts, name)
}

// safeNotifyDone wraps notifyDone with panic recovery. An unrecovered panic in
// a callback would kill the goroutine (and in Go, the entire process). This is
// used in the coordinator goroutine and in runStep's deferred cleanup where
// the caller-supplied OnStepDone is invoked.
func safeNotifyDone(opts RunOptions, name string, err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("exegraph: callback panic for %q: %v", name, r)
		}
	}()
	notifyDone(opts, name, err)
}
