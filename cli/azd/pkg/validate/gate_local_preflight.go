// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package validate

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
)

// LocalPreflightGate is a validation gate that runs the provisioning
// provider's local preflight checks. For Bicep projects this compiles
// the template, runs bicep snapshot, and executes checks for role
// assignments, AI model quota, and reserved resource names.
type LocalPreflightGate struct {
	provisionManager *provisioning.Manager
}

// NewLocalPreflightGate creates a local-preflight gate backed by the
// given provisioning manager. The manager must be initialized before
// the gate is run.
func NewLocalPreflightGate(
	provisionManager *provisioning.Manager,
) *LocalPreflightGate {
	return &LocalPreflightGate{
		provisionManager: provisionManager,
	}
}

// Name returns "local-preflight".
func (g *LocalPreflightGate) Name() string {
	return "local-preflight"
}

// Run initializes the provisioning manager and delegates to the
// provider's Validate method. The provider compiles the template,
// runs bicep snapshot, and executes local preflight checks.
func (g *LocalPreflightGate) Run(
	ctx context.Context, pCtx *PipelineContext,
) (*GateResult, error) {
	if pCtx.Project == nil {
		return &GateResult{
			GateName:   g.Name(),
			Skipped:    true,
			SkipReason: "no project configuration loaded",
		}, nil
	}

	infraOptions := pCtx.Project.Infra
	if err := g.provisionManager.Initialize(
		ctx, pCtx.Project.Path, infraOptions,
	); err != nil {
		return nil, fmt.Errorf(
			"initializing provisioning manager: %w", err)
	}

	validateResult, err := g.provisionManager.Validate(ctx)
	if err != nil {
		return nil, fmt.Errorf(
			"running provider validation: %w", err)
	}

	if validateResult.Skipped {
		return &GateResult{
			GateName:   g.Name(),
			Skipped:    true,
			SkipReason: validateResult.SkipReason,
		}, nil
	}

	results := make([]CheckResult, len(validateResult.Results))
	for i, r := range validateResult.Results {
		severity := CheckWarning
		if r.Severity == provisioning.ValidateError {
			severity = CheckError
		}

		links := make([]ux.PreflightReportLink, len(r.Links))
		for j, l := range r.Links {
			links[j] = ux.PreflightReportLink{
				URL:   l.URL,
				Title: l.Title,
			}
		}

		results[i] = CheckResult{
			Severity:     severity,
			DiagnosticID: r.DiagnosticID,
			Message:      r.Message,
			Suggestion:   r.Suggestion,
			Links:        links,
		}
	}

	return &GateResult{
		GateName: g.Name(),
		Results:  results,
	}, nil
}
