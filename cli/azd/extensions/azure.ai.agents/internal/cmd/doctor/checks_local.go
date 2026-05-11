// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package doctor

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// MinNewBackendVersion is the floor extension version required to talk to
// the new hosted-agents backend. Extensions below this floor can still
// drive the legacy ACA backend; the floor is advisory, surfaced as a
// Warning rather than a hard Fail. The constant lives next to its sole
// consumer (Check `local.grpc-extension`) so that bumping it is a
// one-line change with no scattered references.
//
// Source: hosted-agents quickstart docs at
// https://learn.microsoft.com/azure/foundry/agents/quickstarts/quickstart-hosted-agent
const MinNewBackendVersion = "0.1.27-preview"

// Dependencies bundles the runtime services local checks consume. The
// Cobra wiring in the parent internal/cmd package constructs this from
// `azdext.NewAzdClient()` and the extension's compiled-in version
// constant; tests inject directly.
//
// AzdClient may be nil if NewAzdClient failed at startup (e.g. when the
// extension is launched outside `azd ext run`). AzdClientErr captures
// the cause so Check `local.grpc-extension` can surface it verbatim.
// Downstream checks that need the client must Skip cleanly rather than
// Fail — a cascade of identical "no client" failures is noise.
type Dependencies struct {
	AzdClient        *azdext.AzdClient
	AzdClientErr     error
	ExtensionVersion string
}

// NewLocalChecks returns the canonical sequence of local doctor checks
// in execution order. Phase 4.2 covers checks 1-3; phase 4.3 will append
// checks 4-6 (agent service, project endpoint, agent.yaml).
func NewLocalChecks(deps Dependencies) []Check {
	return []Check{
		newCheckGRPCAndVersion(deps),
		newCheckProjectConfig(deps),
		newCheckEnvironmentSelected(deps),
	}
}

// newCheckGRPCAndVersion produces Check `local.grpc-extension`. It
// verifies the gRPC channel back to azd is available (NewAzdClient
// returned a non-nil client) and that the extension is at or above the
// new-hosted-agents backend floor. Below the floor the check Warns —
// the legacy ACA backend continues to work and the user does not need
// to upgrade immediately.
//
// Dev builds (Version == "dev" or empty) skip the floor check: there is
// no reliable comparison and a Warning on every developer iteration is
// noise.
func newCheckGRPCAndVersion(deps Dependencies) Check {
	return Check{
		ID:   "local.grpc-extension",
		Name: "azd extension reachable",
		Fn: func(_ context.Context, _ Options, _ []Result) Result {
			if deps.AzdClient == nil {
				msg := "gRPC channel to azd is unavailable"
				if deps.AzdClientErr != nil {
					msg = fmt.Sprintf("gRPC channel to azd unavailable: %v", deps.AzdClientErr)
				}
				return Result{
					Status:     StatusFail,
					Message:    msg,
					Suggestion: "Run the extension via `azd ai agent doctor` (not the extension binary directly) and ensure azd is at least 1.24.0.",
				}
			}

			ver := strings.TrimSpace(deps.ExtensionVersion)
			if ver == "" || ver == "dev" {
				return Result{
					Status:  StatusPass,
					Message: fmt.Sprintf("azd extension reachable (version: %s).", coalesce(ver, "unknown")),
				}
			}

			if compareVersions(ver, MinNewBackendVersion) < 0 {
				return Result{
					Status: StatusWarn,
					Message: fmt.Sprintf(
						"Extension version %s is older than %s; the new hosted-agents backend requires the floor.",
						ver, MinNewBackendVersion),
					Suggestion: "Upgrade with `azd ext upgrade azure.ai.agents`.",
					Links:      []string{"https://aka.ms/hostedagents/tsg/readme"},
					Details: map[string]any{
						"extensionVersion":  ver,
						"minBackendVersion": MinNewBackendVersion,
					},
				}
			}

			return Result{
				Status:  StatusPass,
				Message: fmt.Sprintf("azd extension reachable (version %s).", ver),
			}
		},
	}
}

// newCheckProjectConfig produces Check `local.azure-yaml`. It probes the
// azd Project service for the resolved project config. The check Fails
// when the call returns an error OR the response carries a nil Project
// (azd's convention for "no azure.yaml in the working directory"). The
// suggestion mirrors the wording used in helpers.go's resolveConfigPath
// so users see consistent guidance across commands.
//
// Skips cleanly when the gRPC client is unavailable — Check
// `local.grpc-extension` will already have failed and produced the
// actionable error.
func newCheckProjectConfig(deps Dependencies) Check {
	return Check{
		ID:   "local.azure-yaml",
		Name: "azure.yaml present and parseable",
		Fn: func(ctx context.Context, _ Options, _ []Result) Result {
			if deps.AzdClient == nil {
				return Result{
					Status:  StatusSkip,
					Message: "skipped: azd extension not reachable",
				}
			}

			resp, err := deps.AzdClient.Project().Get(ctx, &azdext.EmptyRequest{})
			if err != nil {
				return Result{
					Status:     StatusFail,
					Message:    fmt.Sprintf("failed to get project config: %v", err),
					Suggestion: "Run from a directory containing `azure.yaml`, or initialize one with `azd init`.",
				}
			}
			if resp == nil || resp.Project == nil {
				return Result{
					Status:     StatusFail,
					Message:    "failed to get project config (is there an azure.yaml?)",
					Suggestion: "Run from a directory containing `azure.yaml`, or initialize one with `azd init`.",
				}
			}

			return Result{
				Status:  StatusPass,
				Message: fmt.Sprintf("azure.yaml parsed (project: %s).", resp.Project.Name),
				Details: map[string]any{
					"projectPath": resp.Project.Path,
					"projectName": resp.Project.Name,
				},
			}
		},
	}
}

// newCheckEnvironmentSelected produces Check
// `local.environment-selected`. It probes the azd Environment service
// for the currently-selected environment. The check Fails when the call
// errors, or when the response carries a nil Environment / empty Name.
//
// Skips cleanly when the gRPC client is unavailable OR when the
// `local.azure-yaml` check failed — environment selection is meaningless
// without a project to anchor it.
func newCheckEnvironmentSelected(deps Dependencies) Check {
	return Check{
		ID:   "local.environment-selected",
		Name: "azd environment selected",
		Fn: func(ctx context.Context, _ Options, prior []Result) Result {
			if deps.AzdClient == nil {
				return Result{
					Status:  StatusSkip,
					Message: "skipped: azd extension not reachable",
				}
			}
			for _, p := range prior {
				if p.ID == "local.azure-yaml" && p.Status == StatusFail {
					return Result{
						Status:  StatusSkip,
						Message: "skipped: azure.yaml check failed",
					}
				}
			}

			resp, err := deps.AzdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
			if err != nil {
				return Result{
					Status:     StatusFail,
					Message:    fmt.Sprintf("failed to get current environment: %v", err),
					Suggestion: "Create one with `azd env new <name>` or select an existing one with `azd env select <name>`.",
				}
			}
			if resp == nil || resp.Environment == nil || resp.Environment.Name == "" {
				return Result{
					Status:     StatusFail,
					Message:    "no azd environment is selected",
					Suggestion: "Create one with `azd env new <name>` or select an existing one with `azd env select <name>`.",
				}
			}

			return Result{
				Status:  StatusPass,
				Message: fmt.Sprintf("environment selected: %s.", resp.Environment.Name),
				Details: map[string]any{
					"environmentName": resp.Environment.Name,
				},
			}
		},
	}
}

// coalesce returns the first non-empty string in values, or "" if all
// are empty. Used to keep the version-floor check's Pass message
// readable when the version string is blank.
func coalesce(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// compareVersions compares two version strings numerically on the first
// three dotted components, ignoring any "-suffix" pre-release or "+build"
// metadata. A leading "v" is tolerated. Returns -1 if a<b, 0 if a==b or
// either side fails to parse, +1 if a>b.
//
// The fail-open behavior on invalid input is deliberate: a malformed
// version string should never trigger a Warning suggesting the user
// "upgrade" — a noisy Warn for a real bug is worse than a missed Warn for
// a malformed string. Callers that need strict comparison should use a
// real semver library; for the doctor's floor check, three-component
// numeric comparison is sufficient (the pre-release suffix `-preview` is
// shared between extension and floor and therefore lexicographically
// equal — irrelevant to the cmp).
func compareVersions(a, b string) int {
	pa, oka := parseMainVersion(a)
	pb, okb := parseMainVersion(b)
	if !oka || !okb {
		return 0
	}
	for i := range 3 {
		if pa[i] < pb[i] {
			return -1
		}
		if pa[i] > pb[i] {
			return 1
		}
	}
	return 0
}

// parseMainVersion splits "v?X.Y.Z[-suffix][+build]" into [X, Y, Z] as
// non-negative integers. Returns (zero, false) on any parse error.
func parseMainVersion(v string) ([3]int, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.SplitN(v, ".", 3)
	if len(parts) != 3 {
		return [3]int{}, false
	}
	var out [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}
