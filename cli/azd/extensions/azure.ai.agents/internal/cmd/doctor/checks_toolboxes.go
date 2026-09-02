// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package doctor

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"azureaiagent/internal/cmd/nextstep"
	"azureaiagent/internal/pkg/envkey"
	"azureaiagent/internal/pkg/servicekey"

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
// The check examines each toolbox collected during next-step state
// assembly and verifies that its canonical
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
//   - state.HasToolboxes == false: there are no toolbox declarations;
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
		Name:   "Configured toolboxes have endpoint env vars set",
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

			state, errs := deps.AssembleAgentState(ctx)
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
				issues := slices.Clone(state.ToolboxLoadErrors)
				slices.Sort(issues)
				return Result{
					Status: StatusFail,
					Message: fmt.Sprintf(
						"could not load configured toolboxes: %s",
						strings.Join(issues, "; ")),
					Suggestion: "Repair azure.yaml and any referenced toolbox files, then " +
						"re-run `azd ai agent doctor`.",
					Details: map[string]any{
						"toolboxLoadErrors": issues,
					},
				}
			}
			if len(state.ToolboxDependencyErrors) > 0 {
				issues := slices.Clone(state.ToolboxDependencyErrors)
				slices.Sort(issues)
				return Result{
					Status: StatusFail,
					Message: fmt.Sprintf(
						"configured toolbox dependencies are invalid: %s",
						strings.Join(issues, "; ")),
					Suggestion: "Update azure.yaml so each agent uses an enabled " +
						"toolbox service, then re-run `azd ai agent doctor`.",
					Details: map[string]any{
						"toolboxDependencyErrors": issues,
					},
				}
			}
			if state.ToolboxEndpointsChecked &&
				len(state.ToolboxEndpointErrors) > 0 {
				return Result{
					Status: StatusFail,
					Message: fmt.Sprintf(
						"could not read toolbox endpoint values: %s",
						strings.Join(state.ToolboxEndpointErrors, "; ")),
					Suggestion: "Verify the active azd environment is accessible, then re-run " +
						"`azd ai agent doctor`.",
					Details: map[string]any{
						"toolboxEndpointErrors": state.ToolboxEndpointErrors,
					},
				}
			}
			if !state.HasToolboxes {
				return Result{
					Status:  StatusSkip,
					Message: "skipped: no configured toolbox resources were found.",
				}
			}

			if state.ToolboxEndpointsChecked {
				return classifyToolboxState(state.Toolboxes, state.MissingToolboxEndpoints)
			}

			// Keep this fallback for callers that build partial states.
			// Normal assembly sets ToolboxEndpointsChecked, so production
			// code does not use this lookup.
			lookup := deps.lookupToolboxEnv
			if lookup == nil {
				lookup = makeRealToolboxEnvLookup(deps.AzdClient)
			}
			return classifyToolboxEndpoints(ctx, state.Toolboxes, lookup)
		},
	}
}

func classifyToolboxState(
	toolboxes, missing []nextstep.ResourceRef,
) Result {
	missingKeys := make(map[string]struct{}, len(missing))
	for _, toolbox := range missing {
		missingKeys[envkey.ToolboxMCPEndpoint(toolbox.Name)] = struct{}{}
	}
	seen := make(map[string]struct{}, len(toolboxes))
	matched := 0
	for _, toolbox := range toolboxes {
		key := envkey.ToolboxMCPEndpoint(toolbox.Name)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		if _, ok := missingKeys[key]; !ok {
			matched++
		}
	}
	return classifyToolboxResults(missing, matched)
}

func classifyToolboxResults(
	missing []nextstep.ResourceRef,
	matched int,
) Result {
	uniqueMissing := uniqueToolboxRefs(missing)
	if len(uniqueMissing) == 0 {
		return Result{
			Status:  StatusPass,
			Message: fmt.Sprintf("all %d declared toolbox(es) have an MCP endpoint set.", matched),
			Details: map[string]any{"matchedCount": matched},
		}
	}

	slices.SortFunc(uniqueMissing, compareToolboxRefs)
	slices.SortFunc(missing, compareToolboxRefs)
	var names []string
	for _, toolbox := range uniqueMissing {
		names = append(names, fmt.Sprintf("%s (env %s, service %s)",
			toolbox.Name, envkey.ToolboxMCPEndpoint(toolbox.Name), toolbox.ServiceName))
	}
	hasSplit := false
	hasBundled := false
	hasLegacy := false
	for _, toolbox := range missing {
		hasSplit = hasSplit || toolbox.ToolboxSource == nextstep.ToolboxSourceSplit
		hasBundled = hasBundled || toolbox.ToolboxSource == nextstep.ToolboxSourceBundled
		hasLegacy = hasLegacy ||
			toolbox.ToolboxSource == nextstep.ToolboxSourceLegacyManifest ||
			toolbox.ToolboxSource == nextstep.ToolboxSourceUnknown
	}
	suggestion := "Run `azd provision` to materialize toolbox infrastructure, or " +
		"`azd env set <ENV_VAR> <endpoint>` to point at an existing toolbox."
	switch {
	case hasBundled && hasSplit && hasLegacy:
		suggestion = bundledToolboxMigrationSuggestion(missing) +
			" Run `azd provision` for legacy toolbox resources."
	case hasBundled && hasSplit:
		suggestion = bundledToolboxMigrationSuggestion(missing)
	case hasBundled && hasLegacy:
		suggestion = bundledToolboxMigrationSuggestion(missing) +
			" Run `azd provision` for legacy toolbox resources."
	case hasBundled:
		suggestion = bundledToolboxMigrationSuggestion(missing)
	case hasSplit && hasLegacy:
		suggestion = "Run `azd deploy` for split toolbox services and `azd provision` " +
			"for legacy toolbox resources, or set an existing endpoint."
	case hasSplit:
		suggestion = "Run `azd deploy` to materialize split toolbox services."
	}
	return Result{
		Status: StatusFail,
		Message: fmt.Sprintf("%d declared toolbox(es) have no MCP endpoint set in the azd environment: %s",
			len(uniqueMissing), strings.Join(names, ", ")),
		Suggestion: suggestion,
		Details: map[string]any{
			"missingToolboxes": toolboxLookupDetails(uniqueMissing),
			"matchedCount":     matched,
		},
	}
}

func compareToolboxRefs(a, b nextstep.ResourceRef) int {
	if a.Name != b.Name {
		return strings.Compare(a.Name, b.Name)
	}
	return strings.Compare(a.ServiceName, b.ServiceName)
}

func uniqueToolboxRefs(refs []nextstep.ResourceRef) []nextstep.ResourceRef {
	seen := make(map[string]struct{}, len(refs))
	unique := make([]nextstep.ResourceRef, 0, len(refs))
	for _, ref := range refs {
		key := envkey.ToolboxMCPEndpoint(ref.Name)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, ref)
	}
	return unique
}

func bundledToolboxMigrationSuggestion(
	missing []nextstep.ResourceRef,
) string {
	replacements := make([]string, 0)
	for _, toolbox := range missing {
		if toolbox.ToolboxSource != nextstep.ToolboxSourceBundled {
			continue
		}
		serviceKey := servicekey.SanitizeServiceName(toolbox.Name)
		if serviceKey == toolbox.Name {
			continue
		}
		replacements = append(replacements, fmt.Sprintf(
			"%q in agent %q -> service key %q",
			toolbox.Name,
			toolbox.ServiceName,
			serviceKey,
		))
	}

	suggestion := "Move bundled toolboxes to independent " +
		"azure.ai.toolbox services"
	if len(replacements) > 0 {
		suggestion += ", then replace the changed agent `toolboxes` " +
			"entries (" + strings.Join(replacements, "; ") + ")"
	}
	return suggestion + ", run `azd ai agent add toolbox <service> --agent " +
		"<agent>`, then run `azd deploy`."
}

type toolboxLookup struct {
	Name        string `json:"name"`
	ServiceName string `json:"service"`
	EnvVar      string `json:"envVar"`
}

func toolboxLookupDetails(toolboxes []nextstep.ResourceRef) []toolboxLookup {
	details := make([]toolboxLookup, 0, len(toolboxes))
	for _, toolbox := range toolboxes {
		details = append(details, toolboxLookup{
			Name:        toolbox.Name,
			ServiceName: toolbox.ServiceName,
			EnvVar:      envkey.ToolboxMCPEndpoint(toolbox.Name),
		})
	}
	return details
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
// manifest walker keeps one ref per (ServiceName, Name), so the same
// toolbox used by two services surfaces twice in state.Toolboxes.
// Env reads stay unique; missing owners are kept for attach guidance.
func classifyToolboxEndpoints(
	ctx context.Context,
	toolboxes []nextstep.ResourceRef,
	lookup toolboxEnvLookupFn,
) Result {
	seen := make(map[string]struct{}, len(toolboxes))
	missingKeys := make(map[string]struct{}, len(toolboxes))
	var missing []nextstep.ResourceRef
	matched := 0

	for _, t := range toolboxes {
		key := envkey.ToolboxMCPEndpoint(t.Name)
		if _, dup := seen[key]; !dup {
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
				missingKeys[key] = struct{}{}
			} else {
				matched++
			}
		}
		if _, ok := missingKeys[key]; ok {
			missing = append(missing, t)
		}
	}

	result := classifyToolboxResults(missing, matched)
	if result.Status == StatusFail {
		result.Message = strings.Replace(
			result.Message,
			"declared toolbox(es)",
			"toolbox(es)",
			1,
		)
	}
	return result
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
