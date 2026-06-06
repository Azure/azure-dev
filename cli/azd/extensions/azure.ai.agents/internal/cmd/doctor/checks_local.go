// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package doctor

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"azureaiagent/internal/cmd/nextstep"
	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

	// AgentAPIVersion is the Foundry Agents api-version the remote
	// probes target. The doctor command's Cobra wiring populates this
	// with the package-level DefaultAgentAPIVersion constant so the
	// design's "single source of truth" requirement is honored — both
	// the runtime invoke flow (init, invoke, listen, monitor,
	// session, show) and the doctor probe pin against the same
	// constant. Tests can override per-call to assert URL composition
	// without coupling to the production value.
	AgentAPIVersion string

	// assembleState is a test seam: when non-nil it replaces the
	// production `nextstep.AssembleState` call inside the
	// `local.manual-env-vars` check, letting unit tests inject a
	// pre-computed State without standing up a temp project on disk.
	// Lowercase so external packages cannot reach it. Production code
	// (NewLocalChecks via the Cobra wiring) leaves it nil.
	assembleState func(ctx context.Context, client *azdext.AzdClient) (*nextstep.State, []error)

	// probeAuth is a test seam: when non-nil it replaces the
	// production `realProbeAuth` call inside the `remote.auth` check,
	// letting unit tests inject controlled token-acquisition outcomes
	// (error, expired, near-expiry, pass-with-UPN, pass-without-UPN)
	// without invoking `azd auth token`. Lowercase so external
	// packages cannot reach it; production wiring leaves it nil.
	probeAuth func(ctx context.Context) authProbeResult

	// probeFoundryEndpoint is a test seam: when non-nil it replaces
	// the production `realProbeFoundryEndpoint` call inside the
	// `remote.foundry-endpoint` check, letting unit tests assert each
	// HTTP-status branch (200/401/403/404/5xx/network) without
	// standing up a live Foundry service. The probe receives the
	// `FOUNDRY_PROJECT_ENDPOINT` value resolved by the upstream
	// `local.project-endpoint-set` check; production wiring leaves
	// this field nil.
	probeFoundryEndpoint func(ctx context.Context, endpoint string) foundryProbeResult

	// probeDeveloperRBAC is a test seam: when non-nil it replaces
	// the production `project.QueryDeveloperRBAC` call inside the
	// `remote.rbac` check, letting unit tests cover the Pass / Fail /
	// transient-error branches without spinning up Graph + ARM
	// fakes. The signature mirrors `project.QueryDeveloperRBAC`
	// exactly so the wiring inside `newCheckRBAC` is a single
	// `if probe == nil { probe = project.QueryDeveloperRBAC }`
	// substitution.
	probeDeveloperRBAC func(
		ctx context.Context,
		azdClient *azdext.AzdClient,
		projectResourceID string,
	) (*project.DeveloperRBACResult, error)

	// readProjectResourceIDFn is a test seam: when non-nil it
	// replaces the production `readProjectResourceID` call inside
	// the `remote.rbac` check, letting unit tests exercise the
	// downstream probe-injection paths (Pass / Fail / cascade)
	// without instantiating a real gRPC AzdClient just to read one
	// env var. Production wiring leaves this nil.
	readProjectResourceIDFn func(
		ctx context.Context,
		azdClient *azdext.AzdClient,
	) (string, error)

	// probeAgentStatus is a test seam: when non-nil it replaces
	// the production `realProbeAgentStatus` call inside the
	// `remote.agent-status` check, letting unit tests cover the
	// Active / Creating / Failed / NotFound / transport branches
	// without standing up a live Foundry agent version. The probe
	// is invoked once per service (so a single unit test can drive
	// a multi-service aggregate by returning different statuses
	// for different (name, version) pairs). Production wiring
	// leaves this nil.
	probeAgentStatus func(
		ctx context.Context,
		endpoint, agentName, agentVersion string,
	) agentStatusProbeResult

	// readAgentNameVersionFn is a test seam: when non-nil it
	// replaces the production `readAgentNameVersion` call inside
	// the `remote.agent-status` check. It returns the deployed
	// agent name + version for a given service from the active
	// azd environment. Wiring through a seam avoids the need to
	// stand up a real gRPC AzdClient for unit tests that just
	// need to assert classification logic. Production wiring
	// leaves this nil.
	readAgentNameVersionFn func(
		ctx context.Context,
		azdClient *azdext.AzdClient,
		serviceName string,
	) (name string, version string, err error)

	// probeAgentPrincipal is a test seam for the
	// `remote.agent-identity-roles` check (Phase 5 C12). It returns
	// the agent's managed-identity principal ID by calling
	// GetAgentVersion and reading `instance_identity.principal_id`.
	// Production wiring leaves this nil; the check substitutes
	// `makeRealProbeAgentPrincipal(deps.AgentAPIVersion)` when nil.
	probeAgentPrincipal func(
		ctx context.Context,
		endpoint, agentName, agentVersion string,
	) agentIdentityProbeResult

	// queryAgentIdentityRoles is a test seam for the
	// `remote.agent-identity-roles` check (Phase 5 C12). When
	// non-nil it replaces the production
	// `project.QueryAgentIdentityRoles` call inside the check,
	// letting unit tests exercise per-agent classification
	// (fine / underscoped / empty / unknown) and aggregate folding
	// without instantiating real ARM clients. Signature mirrors
	// `project.QueryAgentIdentityRoles` exactly so the wiring is a
	// single `if query == nil { query = project.QueryAgentIdentityRoles }`
	// substitution. Production wiring leaves this nil.
	queryAgentIdentityRoles func(
		ctx context.Context,
		azdClient *azdext.AzdClient,
		projectResourceID string,
		principals []project.AgentPrincipal,
	) (*project.AgentIdentityRolesResult, error)

	// lookupToolboxEnv is a test seam for the `local.toolboxes`
	// check (Phase 5 C14). When non-nil it replaces the production
	// `makeRealToolboxEnvLookup` closure inside the check, letting
	// unit tests cover the all-present / partial / none /
	// transport-error branches by returning canned `(value, err)`
	// tuples per env key. Production wiring leaves this nil and the
	// check binds `client.Environment().GetValue` on first call.
	lookupToolboxEnv func(ctx context.Context, key string) (value string, err error)

	// probeFoundryConnections is a test seam for the
	// `remote.connections` check (Phase 5 C15). When non-nil it
	// replaces the production `realProbeFoundryConnections` call
	// inside the check, letting unit tests cover the all-match /
	// partial / none / probe-error branches without going through
	// `azd auth` or hitting Foundry. The probe receives the account
	// + project derived from `AZURE_AI_PROJECT_ID` and returns the
	// connection names that exist on that project.
	probeFoundryConnections func(
		ctx context.Context,
		accountName, projectName string,
	) ([]string, error)
}

// NewLocalChecks returns the canonical sequence of local doctor checks
// in execution order. Phase 4.2 covered checks 1-3; Phase 4.3 added
// checks 4-6 (agent service detected, project endpoint set, agent.yaml
// valid). Phase 5 C9 appends check 7 (manual env vars set). Phase 5
// C14 appends check 8 (`local.toolboxes`) which reads per-toolbox MCP
// endpoint env vars; it is local because it does not call ARM /
// Foundry (only the active azd environment).
func NewLocalChecks(deps Dependencies) []Check {
	return []Check{
		newCheckGRPCAndVersion(deps),
		newCheckProjectConfig(deps),
		newCheckEnvironmentSelected(deps),
		newCheckAgentServiceDetected(deps),
		newCheckProjectEndpointSet(deps),
		newCheckAgentYAMLValid(deps),
		newCheckManualEnvVars(deps),
		newCheckToolboxes(deps),
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
					Suggestion: "Run the extension via `azd ai agent doctor` rather than launching the extension binary directly.",
				}
			}

			ver := strings.TrimSpace(deps.ExtensionVersion)
			if ver == "" || ver == "dev" {
				return Result{
					Status:  StatusPass,
					Message: fmt.Sprintf("azd extension reachable (version: %s).", coalesce(ver, "unknown")),
				}
			}

			// If the version string is non-empty/non-"dev" but still can't be parsed
			// (e.g. a build label like "canary" or an unexpected future format),
			// surface Pass but mark the floor check as skipped rather than silently
			// claiming the floor was verified.
			if _, ok := parseMainVersion(ver); !ok {
				return Result{
					Status: StatusPass,
					Message: fmt.Sprintf(
						"azd extension reachable (version %s; floor check skipped: version string not parseable).",
						ver),
					Details: map[string]any{
						"extensionVersion": ver,
						"floorChecked":     false,
					},
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
				suggestion := "Run from a directory containing `azure.yaml`, or initialize one with `azd init`."
				if isTransportFailure(err) {
					// `azdext.NewAzdClient` constructs a lazy gRPC channel, so the
					// nil-client check above cannot detect a stale/unreachable
					// `AZD_SERVER` endpoint. The transport failure surfaces here on
					// the first RPC — swap the suggestion so the user looks at the
					// channel, not at `azure.yaml`.
					suggestion = "Re-run via `azd ai agent doctor`; the extension cannot reach azd's gRPC channel."
				}
				return Result{
					Status:     StatusFail,
					Message:    fmt.Sprintf("failed to get project config: %v", err),
					Suggestion: suggestion,
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
				suggestion := "Create one with `azd env new <name>` or select an existing one with `azd env select <name>`."
				if isTransportFailure(err) {
					suggestion = "Re-run via `azd ai agent doctor`; the extension cannot reach azd's gRPC channel."
				}
				return Result{
					Status:     StatusFail,
					Message:    fmt.Sprintf("failed to get current environment: %v", err),
					Suggestion: suggestion,
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

// isTransportFailure reports whether err is a gRPC transport-class failure
// (channel unreachable, deadline exceeded) as opposed to a server-side
// application error. Used by downstream checks to swap the user-facing
// suggestion when an RPC fails because the channel itself is broken,
// rather than because the project/environment is misconfigured.
func isTransportFailure(err error) bool {
	if err == nil {
		return false
	}
	st, ok := status.FromError(err)
	if !ok {
		return false
	}
	switch st.Code() {
	case codes.Unavailable, codes.DeadlineExceeded:
		return true
	}
	return false
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
