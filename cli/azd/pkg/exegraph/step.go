// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package exegraph provides an execution graph engine for running
// named steps with explicit dependencies and maximum parallelism.
package exegraph

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// StepStatus represents the lifecycle state of a step during execution.
type StepStatus int

const (
	StepPending StepStatus = iota
	StepRunning
	StepDone
	StepFailed
	StepSkipped
)

// String returns the string representation of the StepStatus.
func (s StepStatus) String() string {
	switch s {
	case StepPending:
		return "pending"
	case StepRunning:
		return "running"
	case StepDone:
		return "done"
	case StepFailed:
		return "failed"
	case StepSkipped:
		return "skipped"
	default:
		return "unknown"
	}
}

// StepSkippedError indicates a step was not executed because one of its
// dependencies failed. Consumers can check for this with errors.As to
// distinguish "skipped due to dependency failure" from "step itself failed."
type StepSkippedError struct {
	StepName string
}

func (e *StepSkippedError) Error() string {
	return fmt.Sprintf("step %q skipped: dependency failed", e.StepName)
}

// IsStepSkipped returns true if the error (or any error in its chain) is a
// [StepSkippedError].
func IsStepSkipped(err error) bool {
	_, ok := errors.AsType[*StepSkippedError](err)
	return ok
}

// StepFunc is the function a step executes. It receives a context that is canceled
// when the scheduler shuts down (FailFast policy) or the parent context is canceled.
type StepFunc func(ctx context.Context) error

// Step is a named unit of work with explicit dependencies on other steps.
type Step struct {
	// Name uniquely identifies the step within a graph (e.g., "provision-networking").
	Name string

	// DependsOn lists the names of steps that must complete successfully before
	// this step can start.
	DependsOn []string

	// Tags are optional labels for querying related steps (e.g., "provision", "deploy").
	Tags []string

	// Action is the function to execute when all dependencies are satisfied.
	Action StepFunc
}

// StepTiming captures wall-clock timing for a single step.
type StepTiming struct {
	Name     string
	Status   StepStatus
	Start    time.Time
	End      time.Time
	Duration time.Duration
	Tags     []string
	Err      error
}

// RunResult captures the outcome of a graph execution including per-step timing.
type RunResult struct {
	// Steps contains timing for every step in execution order (by completion time).
	Steps []StepTiming

	// TotalDuration is the wall-clock time from the first step starting to the
	// last step completing.
	TotalDuration time.Duration

	// Error is the combined error from all failed/skipped steps (same as Run returns).
	Error error
}
