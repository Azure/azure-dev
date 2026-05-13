// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package doctor

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// rbacLearnLink is the canonical learn.microsoft.com link surfaced
// alongside the templated `az role assignment create` suggestion when
// the developer is missing the Azure AI User role on the Foundry
// project. Same target as `rbacLink` in checks_foundry_endpoint.go,
// but pinned separately here so the two checks can drift to different
// resources later (e.g., if Foundry publishes a dedicated "developer
// onboarding" page) without an awkward cross-file rename.
const rbacLearnLink = "https://learn.microsoft.com/azure/ai-foundry/concepts/rbac-azure-ai-foundry"

// projectIDVar is the azd environment variable that carries the full
// ARM resource ID of the Foundry project. It is NOT the same as
// `AZURE_AI_PROJECT_ENDPOINT` (which is the data-plane URL); RBAC
// queries run against ARM and need the full resource ID to scope the
// role-assignment list.
const projectIDVar = "AZURE_AI_PROJECT_ID"

// redactedPlaceholder is the canonical scrubbed value rendered in
// user-facing strings (Message / Suggestion / Details) when
// Options.Unredacted is false (the default). Matches the convention
// the design's "Redaction in non-interactive output" section calls
// out (line 177 of azd-ai-agent-doctor-remote-checks.md).
const redactedPlaceholder = "<redacted>"

// shellSafePlaceholderID and shellSafePlaceholderScope are the
// placeholder tokens used in templated `az role assignment create`
// commands. They DO NOT use `<...>` angle brackets because bash and
// zsh interpret `<word>` as input redirection — a user who literally
// copy-pastes `--assignee <redacted>` into a shell gets
// `redacted: No such file or directory` rather than a useful error.
// The az-doc convention is to use ALL_CAPS placeholders for tokens
// the user is expected to substitute, so we match that pattern.
const (
	shellSafePlaceholderID    = "OBJECT_ID"
	shellSafePlaceholderScope = "PROJECT_SCOPE"
)

// redactedDisplayLabel is the user-facing substitute for a real
// PrincipalDisplay when Options.Unredacted is false. We don't use
// `<redacted>` here because the Message reads "<display> has the
// required role on project '...'" — a sentence with `<redacted>` in
// it looks like a templating bug, while "the current principal"
// reads naturally and matches the empty-display-name fallback at
// the same call site.
const redactedDisplayLabel = "the current principal"

// scopeRedactRE captures any ARM scope substring of the form
// `/subscriptions/<id>[/resourceGroups/<rg>[/providers/<rp>/<type>/<name>...]]`.
// Used to scrub raw scope ARNs out of probe error text before the
// doctor surfaces it in Message / Details when redaction is on. The
// regex deliberately matches greedily but stops at whitespace and
// at characters not valid in ARM resource ID segments (quotes,
// commas, parentheses, colons) so adjacent prose ("at scope ...: 403")
// survives intact around the redacted scope.
var scopeRedactRE = regexp.MustCompile(
	`/subscriptions/[^/\s"',\):;\]]+(?:/[^/\s"',\):;\]]+)*`,
)

// guidRedactRE captures bare GUIDs (subscription IDs, tenant IDs,
// principal OIDs). Used as a second pass after scopeRedactRE so
// any GUID that escaped the scope match (e.g., bare in an error
// body) is also scrubbed.
var guidRedactRE = regexp.MustCompile(
	`\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b`,
)

// newCheckRBAC produces Check `remote.rbac`. It queries the
// developer's role assignments on the Foundry project's ARM scope
// and surfaces:
//
//   - Pass: "<display> has Azure AI access on project '<account>/<project>'"
//     (Details: matched role family, scope, principal ID)
//   - Fail: "<display> lacks Azure AI access" with a templated
//     `az role assignment create --role "Azure AI User" --assignee
//     <oid> --scope <scope>` command and a learn.microsoft.com link.
//     The Suggestion text redacts the principal ID and scope ARN
//     when Options.Unredacted is false (the default), so doctor's
//     `--output json` consumers cannot accidentally exfiltrate
//     identifiers through a pipeline.
//   - Skip: precondition unmet (auth failed, env missing the project
//     resource ID, or a transient Graph / ARM error).
//
// Per the design dependency matrix (line 115 of
// azd-ai-agent-doctor-remote-checks.md), RBAC reads ARM, not the
// Foundry data plane — so this check deliberately does NOT cascade
// from `remote.foundry-endpoint`. If a user has the role but the
// data-plane probe is failing for an unrelated reason (DNS / proxy /
// transient outage), this check still produces a useful Pass.
//
// The check is read-only. Unlike `project.CheckDeveloperRBAC`, it
// never attempts to auto-assign missing roles — doctor's contract is
// diagnostic-only. The templated `az role assignment create` command
// lets a user (or a privileged operator they share the output with)
// apply the fix explicitly.
func newCheckRBAC(deps Dependencies) Check {
	return Check{
		ID:     "remote.rbac",
		Name:   "Developer has required role on Foundry project",
		Remote: true,
		Fn: func(ctx context.Context, opts Options, prior []Result) Result {
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
			if priorBlocked(prior, "remote.auth") {
				return Result{
					Status: StatusSkip,
					Message: "skipped: auth probe did not succeed " +
						"(see check `remote.auth`).",
				}
			}

			projectIDReader := deps.readProjectResourceIDFn
			if projectIDReader == nil {
				projectIDReader = readProjectResourceID
			}
			projectID, err := projectIDReader(ctx, deps.AzdClient)
			if err != nil {
				return Result{
					Status: StatusSkip,
					Message: fmt.Sprintf(
						"skipped: could not read %s from the current "+
							"azd environment (%s).",
						projectIDVar, err),
					Suggestion: fmt.Sprintf(
						"Run `azd provision` to create the Foundry "+
							"project, or `azd env set %s "+
							"</subscriptions/.../projects/...>` to "+
							"point at an existing one.",
						projectIDVar),
				}
			}
			if projectID == "" {
				return Result{
					Status: StatusSkip,
					Message: fmt.Sprintf(
						"skipped: %s is not set in the current azd "+
							"environment.", projectIDVar),
					Suggestion: fmt.Sprintf(
						"Run `azd provision` to create the Foundry "+
							"project, or `azd env set %s "+
							"</subscriptions/.../projects/...>` to "+
							"point at an existing one.",
						projectIDVar),
				}
			}

			// Validate the resource ID shape upfront. A malformed
			// AZURE_AI_PROJECT_ID is a configuration error — the
			// user fixes it with `azd env set`, not by retrying
			// network probes. Catching it here avoids emitting a
			// misleading "check your network reachability" Suggestion
			// for what is purely a typo / wrong value (per Sonnet 4.6
			// review finding on commit 0c4d5ee31).
			if err := project.ValidateProjectResourceID(projectID); err != nil {
				details := map[string]any{
					"projectId": redactScope(projectID, opts.Unredacted),
				}
				if opts.Unredacted {
					details["validateError"] = err.Error()
				}
				return Result{
					Status: StatusSkip,
					Message: fmt.Sprintf(
						"skipped: %s is not a valid Foundry project ARM "+
							"resource ID.", projectIDVar),
					Suggestion: fmt.Sprintf(
						"Set %s to an ARM resource ID like "+
							"`/subscriptions/<sub>/resourceGroups/<rg>/"+
							"providers/Microsoft.CognitiveServices/accounts/"+
							"<acct>/projects/<proj>` with `azd env set %s "+
							"<value>`. Run `azd provision` if the project "+
							"does not yet exist.",
						projectIDVar, projectIDVar),
					Details: details,
				}
			}

			probe := deps.probeDeveloperRBAC
			if probe == nil {
				probe = project.QueryDeveloperRBAC
			}
			res, err := probe(ctx, deps.AzdClient, projectID)
			if err != nil {
				return classifyRBACProbeError(err, projectID, opts.Unredacted)
			}

			return classifyRBACResult(res, opts.Unredacted)
		},
	}
}

// classifyRBACProbeError maps a non-nil `QueryDeveloperRBAC` error
// onto a Skip Result with the most-specific user guidance available.
// Each branch is keyed on the canonical sentinel errors defined in
// the `project` package — sentinel-based detection survives wording
// changes in wrapped error text, and keeps the doctor in lockstep
// with the probe's error contract.
//
// Output redaction: scope ARNs and GUIDs are scrubbed from the
// rendered Message and from `Details["probeError"]` when
// !unredacted. `Details["probeError"]` is OMITTED entirely in the
// default (redacted) mode for non-sentinel errors, because ARM
// response bodies can carry response-body identifiers (assignment
// names, action lists) that we can't reliably enumerate.
func classifyRBACProbeError(err error, projectID string, unredacted bool) Result {
	// Cancellation propagates as a clean Skip — user aborted, this
	// is not an RBAC failure.
	if errors.Is(err, context.Canceled) {
		return Result{
			Status:  StatusSkip,
			Message: "skipped: RBAC probe was cancelled.",
		}
	}

	// Service-principal sign-in: Graph /me is user-delegated only.
	// Surface a SPN-aware Skip rather than letting the user chase a
	// generic transient retry hint.
	if errors.Is(err, project.ErrSPNDelegatedAuthRequired) {
		return Result{
			Status: StatusSkip,
			Message: "skipped: RBAC check supports user-delegated " +
				"sign-in only; a service-principal token was detected.",
			Suggestion: "Sign in with a user identity via " +
				"`azd auth login` to enable the RBAC check, or verify " +
				"the role assignment manually with " +
				"`az role assignment list --assignee <spn-object-id> " +
				"--scope <project-resource-id>`.",
		}
	}

	// Defensive: ErrInvalidProjectResourceID should not reach this
	// branch because the upfront validation catches it, but
	// QueryDeveloperRBAC also wraps this sentinel — handle it here
	// so a future code path that bypasses the validation still
	// produces useful guidance.
	if errors.Is(err, project.ErrInvalidProjectResourceID) {
		details := map[string]any{
			"projectId": redactScope(projectID, unredacted),
		}
		if unredacted {
			details["probeError"] = err.Error()
		}
		return Result{
			Status: StatusSkip,
			Message: fmt.Sprintf(
				"skipped: %s is not a valid Foundry project ARM "+
					"resource ID.", projectIDVar),
			Suggestion: fmt.Sprintf(
				"Set %s to an ARM resource ID with "+
					"`azd env set %s <value>`, or run "+
					"`azd provision` to create one.",
				projectIDVar, projectIDVar),
			Details: details,
		}
	}

	// Generic transient probe error. Sanitize the rendered error
	// text by redacting any ARM scope substring or GUID.
	displayErr := firstLine(err.Error())
	if !unredacted {
		displayErr = sanitizeScopeARNs(displayErr)
	}
	details := map[string]any{
		"projectId": redactScope(projectID, unredacted),
	}
	if unredacted {
		// Only carry the raw probe error in Details when explicitly
		// unredacted — otherwise it can echo subscription IDs / RG
		// names / response-body fragments past the redaction layer.
		details["probeError"] = err.Error()
	}
	return Result{
		Status: StatusSkip,
		Message: fmt.Sprintf(
			"skipped: could not query role assignments (%s).",
			displayErr),
		Suggestion: "Retry `azd ai agent doctor` after a moment; " +
			"if the failure persists, check `azd auth login` " +
			"output and your network reachability to " +
			"`graph.microsoft.com` and `management.azure.com`.",
		Details: details,
	}
}

// sanitizeScopeARNs scrubs ARM scope substrings and bare GUIDs out
// of arbitrary text. Used to redact probe error messages before
// they hit the user-facing Message / Details surface. Idempotent.
func sanitizeScopeARNs(text string) string {
	text = scopeRedactRE.ReplaceAllString(text, redactedPlaceholder)
	text = guidRedactRE.ReplaceAllString(text, redactedPlaceholder)
	return text
}

// classifyRBACResult maps a project.DeveloperRBACResult onto a
// doctor Result, handling the redaction switch in one place. Pulled
// out as a free function so unit tests can pin the Pass / Fail
// templating directly without standing up a fake probe.
func classifyRBACResult(res *project.DeveloperRBACResult, unredacted bool) Result {
	// PrincipalDisplay can carry UPN fragments (e.g.,
	// `Alice Example (alice@contoso.com)`) so the default redacted
	// mode substitutes a generic label in the Message rather than
	// echoing the raw display. The empty-display fallback uses
	// the same label so the Pass / Fail sentence reads naturally
	// in both redacted and missing-display modes.
	displayName := res.PrincipalDisplay
	if !unredacted || displayName == "" {
		displayName = redactedDisplayLabel
	}
	scopeShort := fmt.Sprintf("%s/%s", res.AccountName, res.ProjectName)
	details := map[string]any{
		"hasSufficientAIRole": res.HasSufficientAIRole,
		"accountName":         res.AccountName,
		"projectName":         res.ProjectName,
		"principalId":         redactID(res.PrincipalID, unredacted),
		"projectScope":        redactScope(res.ProjectScope, unredacted),
		"principalDisplay":    redactDisplay(res.PrincipalDisplay, unredacted),
	}

	if res.HasSufficientAIRole {
		return Result{
			Status: StatusPass,
			Message: fmt.Sprintf(
				"%s has the required role on project '%s'.",
				displayName, scopeShort),
			Details: details,
		}
	}

	// Templated `az` command: in redacted mode use shell-safe
	// ALL_CAPS placeholders (NOT `<redacted>`, because bash/zsh
	// interpret `<word>` as input redirection — a literal
	// copy-paste of `--assignee <redacted>` would fail with
	// `redacted: No such file or directory`).
	principalArg := res.PrincipalID
	scopeArg := res.ProjectScope
	if !unredacted {
		principalArg = shellSafePlaceholderID
		scopeArg = shellSafePlaceholderScope
	}
	return Result{
		Status: StatusFail,
		Message: fmt.Sprintf(
			"%s does not have the required role on project '%s' "+
				"(Azure AI User / Azure AI Developer / Contributor / Owner).",
			displayName, scopeShort),
		Suggestion: fmt.Sprintf(
			"Assign the Azure AI User role to the developer with:\n"+
				"  az role assignment create \\\n"+
				"    --role \"Azure AI User\" \\\n"+
				"    --assignee %s \\\n"+
				"    --scope %q",
			principalArg, scopeArg),
		Links:   []string{rbacLearnLink},
		Details: details,
	}
}

// readProjectResourceID pulls AZURE_AI_PROJECT_ID from the active
// azd environment via the EnvironmentService gRPC. Returns an empty
// string when the value is missing or whitespace-only; callers
// distinguish that from an outright error.
func readProjectResourceID(ctx context.Context, azdClient *azdext.AzdClient) (string, error) {
	resp, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		Key: projectIDVar,
	})
	if err != nil {
		return "", err
	}
	if resp == nil {
		return "", nil
	}
	return strings.TrimSpace(resp.Value), nil
}

// redactID returns the value verbatim when unredacted is true,
// otherwise the canonical redacted placeholder. Centralizing the
// branch here keeps the Details / Suggestion / Message redaction
// rules in lock-step.
func redactID(id string, unredacted bool) string {
	if unredacted {
		return id
	}
	if id == "" {
		return ""
	}
	return redactedPlaceholder
}

// redactScope mirrors redactID for ARM resource IDs. Pinned as its
// own helper so future evolution (e.g., showing a host-only short
// form when redacted) doesn't have to thread a "type" parameter
// through every call site.
func redactScope(scope string, unredacted bool) string {
	if unredacted {
		return scope
	}
	if scope == "" {
		return ""
	}
	return redactedPlaceholder
}

// redactDisplay returns the full display name when unredacted is
// true, otherwise the placeholder. Display names can contain a UPN
// fragment (e.g., "Alice Example (alice@contoso.com)") so we redact
// by default; the Message rendering still uses the bare display
// name from PrincipalDisplay for readability.
func redactDisplay(display string, unredacted bool) string {
	if unredacted {
		return display
	}
	if display == "" {
		return ""
	}
	return redactedPlaceholder
}
