// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package doctor

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"azureaiagent/internal/cmd/nextstep"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// newCheckManualEnvVars produces Check `local.manual-env-vars` — the
// "manual config values not set" diagnostic.
//
// "Manual" env vars are values referenced by `${...}` syntax inside an
// agent's `env` block in azure.yaml whose names are NOT declared as
// outputs of the project's infrastructure (Bicep / Terraform). They are
// operator-supplied: third-party API keys, model deployment names,
// hand-rolled connection strings. They have to be set in the active azd
// environment before `azd ai agent run` (local) or `azd deploy` (Azure)
// can resolve them — otherwise the running agent sees the literal `${KEY}`
// string and almost certainly fails on first use.
//
// The classification of "manual" vs "infra" lives in nextstep's
// AssembleState (the same pipeline that drives the `Next:` renderer's
// per-state guidance). This check forwards the result so the doctor
// surfaces the same signal users see at the end of `azd ai agent init`
// — no second source of truth, no drift.
//
// Source-of-truth: issue Azure/azure-dev#7975 "Example output (project
// ready, but manual config values missing)" lines 117-127. The doctor
// reports the gap; the post-init `Next:` block (resolver.go, manual-vars
// branch) tells the user what to type.
//
// Skip cascade — this check skips when any of the following hold:
//
//   - deps.AzdClient is nil (gRPC channel unavailable). Check
//     `local.grpc-extension` will already have failed with the actionable
//     error.
//   - `local.agent-service-detected` failed or was skipped. With no
//     Foundry service there are no agents to extract env references from,
//     which would produce an empty MissingManualVars and mislead the user
//     into thinking nothing was missing. This guard transitively covers
//     the azure-yaml → agent-service-detected arm of the local-check
//     chain (each step's own skip-cascade propagates here).
//   - `local.environment-selected` failed or was skipped.
//     `nextstep.AssembleState` early-exits its `detectMissingVars` block
//     when no env is selected (state.go: `if project != nil && envName != ""`).
//     Without this guard the check would silently produce a Pass
//     ("no manual env vars are missing") in a state where it never even
//     looked at any env values — the exact false-Pass the doctor exists
//     to prevent. `environment-selected` is a sibling chain off
//     `azure-yaml` (not upstream of `agent-yaml-valid`), so the previous
//     guard does not cover it transitively.
//
// On Fail the check lists every missing var in the Message (callers can
// also iterate `Details["missingManualVars"]` for the structured payload).
// The Suggestion picks the first missing var as a paste-ready example
// rather than concatenating one `azd env set` line per var: the formatter
// renders Suggestion as a single line, and a paragraph of newlines would
// break the indentation. Users see the full list in the Message and one
// concrete command to copy-paste.
func newCheckManualEnvVars(deps Dependencies) Check {
	return Check{
		ID:   "local.manual-env-vars",
		Name: "manual env vars set",
		Fn: func(ctx context.Context, _ Options, prior []Result) Result {
			if deps.AzdClient == nil {
				return Result{Status: StatusSkip, Message: "skipped: azd extension not reachable"}
			}
			if priorBlocked(prior, "local.agent-service-detected") {
				return Result{Status: StatusSkip, Message: "skipped: no microsoft.foundry service detected or upstream check blocked"}
			}
			if priorBlocked(prior, "local.environment-selected") {
				// Without an azd env, AssembleState's detectMissingVars
				// block is skipped (state.go:258), so MissingManualVars
				// would be empty and the check would falsely Pass.
				return Result{
					Status:  StatusSkip,
					Message: "skipped: no azd environment selected (cannot resolve azure.yaml variables)",
				}
			}

			assembler := deps.assembleState
			if assembler == nil {
				assembler = func(c context.Context, client *azdext.AzdClient) (*nextstep.State, []error) {
					return nextstep.AssembleState(c, client)
				}
			}
			state, errs := assembler(ctx, deps.AzdClient)
			if state == nil {
				// AssembleState always returns a non-nil State even when errs
				// is non-empty — but defend against a future contract change
				// so this check can't be the one to panic-dereference.
				cause := "unknown error"
				if len(errs) > 0 {
					cause = errs[0].Error()
				}
				return Result{
					Status:     StatusFail,
					Message:    fmt.Sprintf("failed to assemble agent state: %s", cause),
					Suggestion: "Re-run `azd ai agent doctor`; the state assembly returned nil unexpectedly.",
				}
			}

			missing := slices.Clone(state.MissingManualVars)
			slices.Sort(missing)

			if len(missing) == 0 {
				return Result{
					Status:  StatusPass,
					Message: "no manual env vars are missing",
				}
			}

			// Single-line Suggestion: pin a paste-ready command for the
			// first (sorted) missing var, plus a clause pointing at the
			// rest only when there ARE additional entries. When exactly
			// one var is missing the bare command is the right
			// instruction — adding "and likewise for the others" implies
			// the user missed something they didn't.
			suggestion := fmt.Sprintf("Run `azd env set %s <value>`.", missing[0])
			if len(missing) > 1 {
				suggestion += " Repeat for each of the other variables listed above."
			}

			return Result{
				Status: StatusFail,
				Message: fmt.Sprintf(
					"%d manual env var(s) referenced by azure.yaml are not set in the azd environment: %s",
					len(missing), strings.Join(missing, ", ")),
				Suggestion: suggestion,
				Details: map[string]any{
					"missingManualVars": missing,
				},
			}
		},
	}
}
