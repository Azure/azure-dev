// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package validate

import (
	"context"
	"fmt"
	"log"
)

// OnErrorBehavior controls how the pipeline reacts when a gate produces errors.
type OnErrorBehavior string

const (
	// OnErrorAbort stops the pipeline when a gate produces error-level findings.
	OnErrorAbort OnErrorBehavior = "abort"
	// OnErrorContinue logs error-level findings but continues to the next gate.
	OnErrorContinue OnErrorBehavior = "continue"
)

// PipelineOptions configures how the validation pipeline executes.
type PipelineOptions struct {
	// OnError controls pipeline behavior when a gate produces errors.
	// Defaults to [OnErrorAbort] if empty.
	OnError OnErrorBehavior
}

// Pipeline orchestrates the sequential execution of validation gates.
// Gates are executed in the order they are added, and results are aggregated
// into a single [PipelineResult].
type Pipeline struct {
	gates   []Gate
	options PipelineOptions
}

// NewPipeline creates a new validation pipeline with the given options.
func NewPipeline(options PipelineOptions) *Pipeline {
	if options.OnError == "" {
		options.OnError = OnErrorAbort
	}
	return &Pipeline{
		options: options,
	}
}

// AddGate registers a gate to be executed during pipeline runs.
// Gates are executed in the order they are added.
func (p *Pipeline) AddGate(gate Gate) {
	p.gates = append(p.gates, gate)
}

// Gates returns the list of registered gates.
func (p *Pipeline) Gates() []Gate {
	return p.gates
}

// Run executes all registered gates sequentially and returns the aggregated
// results. Each gate receives the shared [PipelineContext] for access to
// project state and inter-gate communication.
//
// When [PipelineOptions.OnError] is [OnErrorAbort] (the default), the pipeline
// stops after the first gate that produces error-level findings. When set to
// [OnErrorContinue], all gates run regardless of errors.
//
// If a gate returns an error (as opposed to error-level findings), the pipeline
// stops immediately and returns the error.
func (p *Pipeline) Run(ctx context.Context, pCtx *PipelineContext) (*PipelineResult, error) {
	result := &PipelineResult{}

	for _, gate := range p.gates {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}

		log.Printf("validate: running gate %q", gate.Name())
		gateResult, err := gate.Run(ctx, pCtx)
		if err != nil {
			return result, fmt.Errorf("validation gate %q failed: %w", gate.Name(), err)
		}

		if gateResult == nil {
			// nil result means gate was skipped
			gateResult = &GateResult{
				GateName:   gate.Name(),
				Skipped:    true,
				SkipReason: "gate returned no result",
			}
		}

		result.GateResults = append(result.GateResults, gateResult)

		if gateResult.HasErrors() && p.options.OnError == OnErrorAbort {
			log.Printf(
				"validate: gate %q produced %d error(s), aborting pipeline",
				gate.Name(), gateResult.ErrorCount(),
			)
			return result, nil
		}
	}

	return result, nil
}
