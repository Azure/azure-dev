// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package doctor

import (
	"context"
	"fmt"
	"strings"

	"azureaiagent/internal/cmd/nextstep"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// newCheckModels validates local model resource configuration.
func newCheckModels(deps Dependencies) Check {
	return Check{
		ID:   "local.models",
		Name: "azure.yaml model configuration valid",
		Fn: func(
			ctx context.Context,
			_ Options,
			prior []Result,
		) Result {
			if deps.AzdClient == nil {
				return Result{
					Status:  StatusSkip,
					Message: "skipped: azd extension not reachable.",
				}
			}
			if priorBlocked(prior, "local.azure-yaml") ||
				priorBlocked(prior, "local.agent-service-detected") {
				return Result{
					Status: StatusSkip,
					Message: "skipped: project configuration " +
						"checks did not succeed.",
				}
			}

			assembler := deps.assembleState
			if assembler == nil {
				assembler = func(
					ctx context.Context,
					client *azdext.AzdClient,
				) (*nextstep.State, []error) {
					return nextstep.AssembleState(ctx, client)
				}
			}
			state, errs := assembler(ctx, deps.AzdClient)
			if state == nil {
				cause := "unknown error"
				if len(errs) > 0 {
					cause = errs[0].Error()
				}
				return Result{
					Status: StatusFail,
					Message: fmt.Sprintf(
						"failed to assemble agent state: %s",
						cause,
					),
					Suggestion: "Fix azure.yaml and retry.",
				}
			}
			if len(state.ModelLoadErrors) > 0 {
				return Result{
					Status: StatusFail,
					Message: fmt.Sprintf(
						"could not load model configuration: %s",
						strings.Join(state.ModelLoadErrors, "; "),
					),
					Suggestion: "Fix the model configuration in " +
						"azure.yaml or the agent manifest, then retry.",
					Details: map[string]any{
						"loadErrors": state.ModelLoadErrors,
					},
				}
			}
			if !state.HasModels {
				return Result{
					Status: StatusSkip,
					Message: "skipped: no model resources " +
						"declared in azure.yaml.",
				}
			}
			return Result{
				Status:  StatusPass,
				Message: "model resource configuration loaded.",
			}
		},
	}
}
