// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package service

import (
	"context"
	"fmt"
	"strings"

	"azure.ai.customtraining/internal/utils"
)

// ComputeResolver resolves a user-friendly compute name to a full ARM resource ID.
type ComputeResolver interface {
	ResolveCompute(ctx context.Context, computeName string) (armID string, err error)
}

// CodeResolver resolves a local code path to a datastore asset ID.
type CodeResolver interface {
	ResolveCode(ctx context.Context, codePath string) (codeID string, err error)
}

// InputResolver resolves a local input path to a datastore URI.
type InputResolver interface {
	ResolveInput(ctx context.Context, inputName string, inputPath string, inputType string) (uri string, err error)
}

// JobResolver orchestrates resolution of all references in a JobDefinition.
type JobResolver struct {
	compute ComputeResolver
	code    CodeResolver
	input   InputResolver
}

// NewJobResolver creates a new JobResolver with the given resolver implementations.
func NewJobResolver(compute ComputeResolver, code CodeResolver, input InputResolver) *JobResolver {
	return &JobResolver{
		compute: compute,
		code:    code,
		input:   input,
	}
}

// ResolveJobDefinition resolves all references (compute, code, inputs) in the job definition in place.
func (r *JobResolver) ResolveJobDefinition(ctx context.Context, jobDef *utils.JobDefinition) error {
	// Resolve compute: simple name → ARM ID
	if jobDef.Compute != "" && !isARMResourceID(jobDef.Compute) {
		armID, err := r.compute.ResolveCompute(ctx, jobDef.Compute)
		if err != nil {
			return fmt.Errorf("failed to resolve compute '%s': %w", jobDef.Compute, err)
		}
		jobDef.Compute = armID
	}

	// Resolve code: local path → datastore asset ID
	if jobDef.Code != "" && !isRemoteURI(jobDef.Code) {
		codeID, err := r.code.ResolveCode(ctx, jobDef.Code)
		if err != nil {
			return fmt.Errorf("failed to resolve code path '%s': %w", jobDef.Code, err)
		}
		jobDef.Code = codeID
	}

	// Resolve inputs: local paths → datastore URIs
	for name, input := range jobDef.Inputs {
		if input.Path != "" && !isRemoteURI(input.Path) && input.Value == "" {
			uri, err := r.input.ResolveInput(ctx, name, input.Path, input.Type)
			if err != nil {
				return fmt.Errorf("failed to resolve input '%s' path '%s': %w", name, input.Path, err)
			}
			input.Path = uri
			jobDef.Inputs[name] = input
		}
	}

	return nil
}

// isARMResourceID checks if a string is a full ARM resource ID.
func isARMResourceID(s string) bool {
	return strings.HasPrefix(strings.ToLower(s), "/subscriptions/")
}

// isRemoteURI checks if a string is a remote URI (not a local path).
func isRemoteURI(s string) bool {
	lower := strings.ToLower(s)
	return strings.HasPrefix(lower, "azureml://") ||
		strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "http://")
}
