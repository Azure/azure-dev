// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package validate

import (
	"context"
)

// CheckFn is a function that performs a single validation check within a gate.
// It receives the pipeline context for access to environment, project, and
// inter-gate state. Returns zero or more results (or nil if nothing to report)
// and an error if the check itself failed to execute.
type CheckFn func(ctx context.Context, pCtx *PipelineContext) ([]CheckResult, error)

// Check pairs a unique rule identifier with its check function.
type Check struct {
	// RuleID is a unique, stable identifier for the rule
	// (e.g. "role_assignment_permissions").
	RuleID string
	// Fn is the check function that performs the validation.
	Fn CheckFn
}

// CheckBasedGate is a convenience [Gate] implementation that runs a list
// of [Check] functions sequentially and aggregates their results.
//
// Use this when implementing a gate that is composed of independent checks
// rather than a single monolithic validation.
type CheckBasedGate struct {
	// GateName is the unique identifier for this gate.
	GateName string
	// Checks is the ordered list of checks to execute.
	Checks []Check
}

// Name returns the gate's unique identifier.
func (g *CheckBasedGate) Name() string { return g.GateName }

// AddCheck appends a check to the gate's check list.
func (g *CheckBasedGate) AddCheck(check Check) {
	g.Checks = append(g.Checks, check)
}

// Run executes all registered checks sequentially and returns the
// aggregated results. If a check returns an error, execution stops
// and the error is returned.
func (g *CheckBasedGate) Run(
	ctx context.Context, pCtx *PipelineContext,
) (*GateResult, error) {
	result := &GateResult{
		GateName: g.GateName,
		Results:  []CheckResult{},
	}

	for _, check := range g.Checks {
		checkResults, err := check.Fn(ctx, pCtx)
		if err != nil {
			return result, err
		}
		result.Results = append(result.Results, checkResults...)
	}

	return result, nil
}
