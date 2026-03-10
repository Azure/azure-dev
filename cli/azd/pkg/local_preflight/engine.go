// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package local_preflight provides a lightweight engine for running local validation checks
// before interacting with Azure services (e.g., before provisioning infrastructure). Each check
// is independent and reports its own status, message, and optional remediation suggestion.
//
// # Usage
//
// Build an engine with one or more checks, then call Run:
//
//	engine := local_preflight.NewEngine(
//	    checks.NewAuthCheck(authManager),
//	    checks.NewSubscriptionCheck(env),
//	)
//	results, err := engine.Run(ctx)
//
// The engine never stops on the first failure; it runs every registered check and returns the
// full slice of results so callers can decide how to surface them.
package local_preflight

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

// Status represents the outcome of a single preflight check.
type Status int

const (
	// StatusPass means the check succeeded and no action is required.
	StatusPass Status = iota
	// StatusWarn means the check found a potential issue but it is not a hard blocker.
	StatusWarn
	// StatusFail means the check found a condition that is expected to prevent a successful deployment.
	StatusFail
	// StatusSkipped means the check was not applicable in the current context.
	StatusSkipped
)

// Result is the outcome of a single preflight check.
type Result struct {
	// CheckName is the human-readable name of the check that produced this result.
	CheckName string
	// Status is the outcome of the check.
	Status Status
	// Message is a short human-readable description of the result.
	Message string
	// Suggestion is an optional remediation hint displayed when Status is Warn or Fail.
	Suggestion string
}

// Check is the interface that must be implemented by every preflight check.
type Check interface {
	// Name returns a short human-readable identifier used in output and logs.
	Name() string
	// Run executes the check and returns a Result.
	Run(ctx context.Context) Result
}

// Engine runs a collection of preflight checks and aggregates their results.
type Engine struct {
	checks []Check
}

// NewEngine creates an Engine with the provided checks pre-registered.
func NewEngine(checks ...Check) *Engine {
	return &Engine{checks: checks}
}

// Add appends additional checks to the engine.
func (e *Engine) Add(checks ...Check) {
	e.checks = append(e.checks, checks...)
}

// Run executes all registered checks in order and returns every result.
// The first return value is the complete slice of results; the second is a non-nil
// error only when at least one check returned StatusFail.
func (e *Engine) Run(ctx context.Context) ([]Result, error) {
	results := make([]Result, 0, len(e.checks))
	failed := false

	for _, c := range e.checks {
		r := c.Run(ctx)
		r.CheckName = c.Name()
		results = append(results, r)
		if r.Status == StatusFail {
			failed = true
		}
	}

	if failed {
		return results, fmt.Errorf("one or more preflight checks failed")
	}

	return results, nil
}

// PrintResults writes a formatted summary of all results to the console.
// It is a convenience helper for commands that want a standard output experience.
func PrintResults(ctx context.Context, console input.Console, results []Result) {
	console.Message(ctx, "")
	console.Message(ctx, output.WithBold("Local preflight checks:"))
	for _, r := range results {
		line := fmt.Sprintf("  %s %s", statusPrefix(r.Status), r.Message)
		console.Message(ctx, line)
		if r.Suggestion != "" && (r.Status == StatusWarn || r.Status == StatusFail) {
			console.Message(ctx, fmt.Sprintf("    → %s", r.Suggestion))
		}
	}
	console.Message(ctx, "")
}

func statusPrefix(s Status) string {
	switch s {
	case StatusPass:
		return output.WithSuccessFormat("(✓)")
	case StatusWarn:
		return output.WithWarningFormat("(!)")
	case StatusFail:
		return output.WithErrorFormat("(✗)")
	default:
		return output.WithGrayFormat("(-)")
	}
}
