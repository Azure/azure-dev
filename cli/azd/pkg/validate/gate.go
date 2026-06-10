// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package validate

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

// Gate is a named validation stage in the pipeline. Each gate groups
// related checks and runs them against the current project and environment.
//
// Gate implementations should return a non-nil [GateResult] even when no
// findings are produced (use an empty Results slice). A nil result with a
// nil error signals that the gate was skipped entirely.
type Gate interface {
	// Name returns the unique identifier for this gate (e.g. "local-preflight").
	Name() string

	// Run executes all checks in this gate and returns the aggregated results.
	// The pipeline context provides access to shared state, project configuration,
	// and the console for user interaction.
	Run(ctx context.Context, pCtx *PipelineContext) (*GateResult, error)
}

// PipelineContext is the shared state that flows through all gates in the
// validation pipeline. It provides access to the project, environment, and
// console, and allows gates to share computed data via the Values map.
type PipelineContext struct {
	// Console provides user interaction capabilities (prompts, messages).
	Console input.Console

	// Environment is the current azd environment with subscription, location, etc.
	Environment *environment.Environment

	// Project is the loaded project configuration from azure.yaml.
	Project *project.ProjectConfig

	// Values is a key-value store for inter-gate communication.
	// Gates can store computed data (e.g. resolved resource lists) for
	// downstream gates to consume. Keys should be namespaced by gate name
	// to avoid collisions (e.g. "local-preflight.snapshot").
	Values map[string]any
}

// SetValue stores a value in the pipeline context for inter-gate communication.
func (c *PipelineContext) SetValue(key string, value any) {
	if c.Values == nil {
		c.Values = make(map[string]any)
	}
	c.Values[key] = value
}

// GetValue retrieves a value from the pipeline context.
// Returns the value and true if found, or the zero value and false if not.
func GetValue[T any](c *PipelineContext, key string) (T, bool) {
	if c.Values == nil {
		var zero T
		return zero, false
	}
	v, ok := c.Values[key]
	if !ok {
		var zero T
		return zero, false
	}
	typed, ok := v.(T)
	return typed, ok
}
