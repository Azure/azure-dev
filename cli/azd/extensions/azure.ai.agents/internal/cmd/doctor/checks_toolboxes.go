// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package doctor

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"azureaiagent/internal/cmd/nextstep"
	"azureaiagent/internal/pkg/envkey"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// toolboxEnvLookupFn is the seam-friendly signature for reading one
// env var from the active azd environment. The Doctor's existing
// project-endpoint and rbac checks read AZURE_AI_PROJECT_* directly
// via gRPC; this check reads N values (one per toolbox), so isolating
// the call shape behind a closure simplifies test fakes. Implementations
// may return ("", nil) for an unset key (matches the azd gRPC env
// service's actual contract).
type toolboxEnvLookupFn func(ctx context.Context, key string) (value string, err error)

// newCheckToolboxes produces Check `local.toolboxes` (P5.1 C14).
// For each toolbox declared in azure.yaml, the
// check verifies that the canonical
// `TOOLBOX_<NORMALIZED_NAME>_MCP_ENDPOINT` env var is set to a
// non-empty value in the active azd environment.
//
// The check is classified `local` (Remote: false) because it only
// reads the active azd environment — no ARM / Foundry round trips.
// `--local-only` therefore still runs it.
//
// # Skip cascade
//
//   - deps.AzdClient nil → upstream `local.grpc-extension` failure.
//   - `local.environment-selected` failed/skipped → there is no env
//     to read from. AssembleState's detectMissingVars block also
//     skips in this state, so the toolbox check would falsely Pass.
//   - `local.azure-yaml` / `local.agent-service-detected` failed →
//     no services to walk; walker output is unreliable.
//   - state.HasToolboxes == false → no manifest toolbox declarations;
//     the check has nothing to verify.
//
// # Why this check is not gated on `remote.auth` /
// `remote.foundry-endpoint`
//
// This check does NOT talk to ARM or Foundry; it only reads local
// azd env state. Gating on remote upstream checks would surface a
// false Skip in the (legitimate) case where ARM is down but the
// user can still diagnose a missing local env var.
//
// # Classification
//
//   - All toolboxes have a set endpoint → Pass.
//   - One or more missing endpoints → Fail with the missing toolbox
//     names in the Message, and `Details["missingToolboxes"]` listing
//     each missing toolbox together with the env var name the check
//     was expecting.
//   - Env service transport error → Fail (NOT Skip): a Skip would
//     leave the user with no actionable signal at all; the
//     Suggestion points at the env service / azd config as the
//     likely culprit.
func newCheckToolboxes(deps Dependencies) Check {
	return Check{
		ID:     "local.toolboxes",
		Name:   "azure.yaml toolboxes have endpoint env vars set",
		Remote: false,
		Fn: func(ctx context.Context, _ Options, prior []Result) Result {
			if deps.AzdClient == nil {
				return Result{
					Status:  StatusSkip,
					Message: "skipped: azd extension not reachable.",
				}
			}
			if priorBlocked(prior, "local.environment-selected") {
				return Result{
					Status: StatusSkip,
					Message: "skipped: no azd environment is selected " +
						"(see check `local.environment-selected`).",
				}
			}
			if priorBlocked(prior, "local.azure-yaml") ||
				priorBlocked(prior, "local.agent-service-detected") {
				return Result{
					Status: StatusSkip,
					Message: "skipped: azure.yaml / agent service detection failed " +
						"(see checks `local.azure-yaml`, `local.agent-service-detected`).",
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
				// is non-empty (state.go), but defend against a future contract
				// change so the check surfaces the real cause instead of a
				// misleading "no toolboxes declared" Skip.
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
			if len(state.ToolboxLoadErrors) > 0 {
				return Result{
					Status: StatusFail,
					Message: fmt.Sprintf(
						"could not load toolbox configuration: %s",
						strings.Join(
							state.ToolboxLoadErrors,
							"; ",
						),
					),
					Suggestion: "Fix the toolbox services in " +
						"azure.yaml or the agent manifest, then retry.",
					Details: map[string]any{
						"loadErrors": state.ToolboxLoadErrors,
					},
				}
			}
			if !state.HasToolboxes {
				return Result{
					Status: StatusSkip,
					Message: "skipped: no toolbox resources " +
						"declared in azure.yaml.",
				}
			}

			lookup := deps.lookupToolboxEnv
			if lookup == nil {
				lookup = makeRealToolboxEnvLookup(deps.AzdClient)
			}

			return classifyToolboxEndpoints(ctx, state.Toolboxes, lookup)
		},
	}
}

// normalizeToolboxName / toolboxEndpointKey have been replaced by the
// shared `internal/pkg/envkey` package. See envkey.ToolboxMCPEndpoint.

// classifyToolboxEndpoints joins state.Toolboxes to the active azd
// env. Each toolbox produces one env lookup; the first transport
// error short-circuits the check to Fail (NOT Skip — see the
// factory's doc-comment for why) so the user gets one actionable
// surface instead of a quiet pass-through.
//
// Dedup is on the canonical env key, not the toolbox name: the
// manifest walker deduplicates on (ServiceName, Name) so the same toolbox
// referenced by two services surfaces twice in state.Toolboxes.
// Without dedup here the doctor would issue two gRPC reads for the
// same key and report the same toolbox twice in the missing list.
func classifyToolboxEndpoints(
	ctx context.Context,
	toolboxes []nextstep.ResourceRef,
	lookup toolboxEnvLookupFn,
) Result {
	type toolboxLookup struct {
		Name            string `json:"name"`
		ServiceName     string `json:"service"`
		EnvVar          string `json:"envVar"`
		ManagedByDeploy bool   `json:"-"`
	}

	seen := make(map[string]struct{}, len(toolboxes))
	var missing []toolboxLookup
	matched := 0

	for _, t := range toolboxes {
		key := envkey.ToolboxMCPEndpoint(t.Name)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}

		value, err := lookup(ctx, key)
		if err != nil {
			return Result{
				Status: StatusFail,
				Message: fmt.Sprintf(
					"could not read toolbox endpoint env vars from the azd environment: %s",
					err),
				Suggestion: "Verify the azd extension is healthy and the active environment is accessible. " +
					"Try `azd env list` and `azd env get-values`.",
			}
		}
		if strings.TrimSpace(value) == "" {
			missing = append(missing, toolboxLookup{
				Name:            t.Name,
				ServiceName:     t.ServiceName,
				EnvVar:          key,
				ManagedByDeploy: t.ManagedByDeploy,
			})
			continue
		}
		matched++
	}

	sort.Slice(missing, func(i, j int) bool {
		if missing[i].Name != missing[j].Name {
			return missing[i].Name < missing[j].Name
		}
		return missing[i].ServiceName < missing[j].ServiceName
	})

	if len(missing) == 0 {
		return Result{
			Status:  StatusPass,
			Message: fmt.Sprintf("all %d declared toolbox(es) have an MCP endpoint set.", matched),
			Details: map[string]any{
				"matchedCount": matched,
			},
		}
	}

	var sb strings.Builder
	for i, m := range missing {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(fmt.Sprintf("%s (env %s, service %s)", m.Name, m.EnvVar, m.ServiceName))
	}

	needsDeploy := slices.ContainsFunc(missing, func(entry toolboxLookup) bool {
		return entry.ManagedByDeploy
	})
	needsProvision := slices.ContainsFunc(missing, func(entry toolboxLookup) bool {
		return !entry.ManagedByDeploy
	})
	suggestion := "Run `azd provision` to materialize legacy " +
		"toolbox infrastructure."
	switch {
	case needsDeploy && needsProvision:
		suggestion = "Run `azd deploy` for toolbox services and " +
			"`azd provision` for legacy toolbox resources."
	case needsDeploy:
		suggestion = "Run `azd deploy` to deploy toolbox services."
	}
	suggestion += " Alternatively, use `azd env set <ENV_VAR> " +
		"<endpoint>` to point at an existing toolbox."

	return Result{
		Status: StatusFail,
		Message: fmt.Sprintf(
			"%d toolbox(es) declared in azure.yaml have no MCP "+
				"endpoint set in the azd environment: %s",
			len(missing), sb.String()),
		Suggestion: suggestion,
		Details: map[string]any{
			"missingToolboxes": missing,
			"matchedCount":     matched,
		},
	}
}

// makeRealToolboxEnvLookup binds an `azdext.AzdClient` to a one-key
// env reader. The active environment is resolved by the gRPC server
// (caller does not need to know its name), matching the existing
// `readProjectResourceID` pattern in `checks_rbac.go:388-396`.
//
// An empty `Key` argument is treated as a programmer error and
// short-circuits with the rpc error rather than masking it. A
// missing key returns ("", nil) — the same shape every other azd
// extension expects from `GetValue`.
func makeRealToolboxEnvLookup(client *azdext.AzdClient) toolboxEnvLookupFn {
	return func(ctx context.Context, key string) (string, error) {
		resp, err := client.Environment().GetValue(ctx, &azdext.GetEnvRequest{
			Key: key,
		})
		if err != nil {
			return "", err
		}
		return resp.Value, nil
	}
}

// dedupToolboxKeys returns the slice of canonical env keys the
// classifier would probe for a given ToolboxRef slice — exposed for
// the renderer / future telemetry consumer that wants to log "we
// expected these N env vars". The classifier does its own dedup
// inline; this helper is for callers that need the list up front.
func dedupToolboxKeys(toolboxes []nextstep.ResourceRef) []string {
	seen := make(map[string]struct{}, len(toolboxes))
	keys := make([]string, 0, len(toolboxes))
	for _, t := range toolboxes {
		key := envkey.ToolboxMCPEndpoint(t.Name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
