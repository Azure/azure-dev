// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// foundryAuthScope is the OAuth scope required for Foundry data-plane
// calls. Kept in sync with `invoke.go`, `agent_api/operations.go`, and the
// `foundry_*_client.go` files in `internal/pkg/azure/` — every Foundry
// caller requests the same scope so a single token works for all calls.
const foundryAuthScope = "https://ai.azure.com/.default"

// doctorRemoteTimeout is the per-check wall-clock budget for remote
// checks. Doctor is diagnostic, not resilient — no retries.
const doctorRemoteTimeout = 10 * time.Second

// tokenExpiryWarnThreshold flips check 7 from Pass to Warn when the
// fetched token has less than this much validity left. Matches the
// design: "token expires in <5 min (suggest re-login proactively)".
const tokenExpiryWarnThreshold = 5 * time.Minute

// remotePreconditions captures the values from local checks 3, 5, and 4
// that the remote check runner needs. Bundled so [runRemoteChecks] does
// not need a 5-argument signature.
type remotePreconditions struct {
	// endpointSet reflects check 5's outcome (true when
	// AZURE_AI_PROJECT_ENDPOINT was found in the azd environment).
	endpointSet bool
	// endpoint is the resolved AZURE_AI_PROJECT_ENDPOINT value.
	endpoint string
	// envName is the active azd environment name (check 3). Empty when
	// no environment is selected.
	envName string
	// projectPath is the absolute path to the loaded azd project root
	// (check 2). Used by per-service checks (e.g., check 9) that need
	// to read agent.yaml files from each service's source directory.
	projectPath string
	// agentServices is the list of services with host:azure.ai.agent /
	// azure.ai.toolbox from check 4. Populated even if zero services
	// matched; remote checks that need a service iterate over the slice.
	agentServices []*azdext.ServiceConfig
}

// runRemoteChecks executes the remote (cloud) doctor checks. Returns an
// ordered slice of results; one row per check (or per service for
// per-service checks). When --local-only is set, every remote check is
// emitted as an explicit Skip with the reason `"skipped: --local-only"`
// so the rendered report stays consistent with the design ("never
// quietly drop a check").
//
// Cross-check sequencing (the design's dependency matrix):
//
//   - check 7  (auth)            gates 8, 10, 11, 12
//   - check 8  (reachability)    gates 9, 11
//   - check 11 (agent status)    gates 12
//
// R2-D extends the runner to checks 9 (model deployments) and 11 (agent
// status). Each downstream check inspects the prior results to decide
// Pass / Skip / Warn / Fail; we do not short-circuit the loop, so a
// failed reachability still surfaces explicit Skip rows for the
// dependent checks.
func (a *doctorAction) runRemoteChecks(ctx context.Context, pre remotePreconditions) []doctorResult {
	if a.flags != nil && a.flags.localOnly {
		return remoteSkipRows("skipped: --local-only")
	}

	out := make([]doctorResult, 0, 4)

	// Check 7 — Authentication. Captures the auth result (and bearer
	// token) so check 8 can either reuse the token or Skip with a clean
	// reason. The actual GetToken round-trip is wrapped in timed() so
	// the JSON envelope's duration matches wall-clock time.
	var authResult doctorResult
	var bearerToken string
	out = append(out, timed(func() doctorResult {
		token, res := a.checkAuth(ctx)
		bearerToken = token
		authResult = res
		return res
	}))

	// Check 8 — Foundry project reachability. Inspects authResult and
	// the precondition struct to either skip cleanly or fire the probe.
	var reachabilityResult doctorResult
	out = append(out, timed(func() doctorResult {
		reachabilityResult = a.checkReachability(ctx, pre, authResult.Status, bearerToken)
		return reachabilityResult
	}))

	// Check 9 — Model deployments (per service). Depends on check 6
	// (manifest valid) AND check 8 (reachability). The helper iterates
	// pre.agentServices and emits one row per service; each row is
	// re-wrapped through timed() so durations reflect per-service work.
	for _, row := range a.checkModelDeployments(ctx, pre, reachabilityResult.Status) {
		r := row
		out = append(out, timed(func() doctorResult { return r }))
	}

	// Check 11 — Agent status (per service). Depends on check 7 (auth)
	// AND check 8 (reachability). Same per-service row pattern as
	// check 9.
	for _, row := range a.checkAgentStatus(ctx, pre, authResult.Status, reachabilityResult.Status, bearerToken) {
		r := row
		out = append(out, timed(func() doctorResult { return r }))
	}

	return out
}

// remoteSkipRows returns the design's remote-check Skip placeholders
// pre-filled with `reason`. R2-D ships rows for checks 7, 8, 9, 11;
// later phases will extend the slice as checks 10 and 12 land.
func remoteSkipRows(reason string) []doctorResult {
	return []doctorResult{
		{
			ID:     "remote.auth",
			Title:  "Authentication",
			Status: doctorSkip,
			Detail: reason,
		},
		{
			ID:     "remote.reachability",
			Title:  "Foundry project reachability",
			Status: doctorSkip,
			Detail: reason,
		},
		{
			ID:     "remote.models",
			Title:  "Model deployments",
			Status: doctorSkip,
			Detail: reason,
		},
		{
			ID:     "remote.agent-status",
			Title:  "Agent status",
			Status: doctorSkip,
			Detail: reason,
		},
	}
}

// checkAuth performs doctor check 7. It calls
// [azidentity.NewAzureDeveloperCLICredential] (via [newAgentCredential])
// and requests a token for the Foundry scope. Returns the raw bearer
// token (empty on failure) and the doctorResult to render.
//
// Status mapping:
//
//   - Pass  — token acquired and >=5 minutes remaining
//   - Warn  — token acquired but <5 minutes remaining (re-login
//     proactively before the next long-running deploy / invoke)
//   - Fail  — credential creation or GetToken failed; suggest
//     `azd auth login` (plus `--local-only` if stdout is non-TTY)
func (a *doctorAction) checkAuth(ctx context.Context) (string, doctorResult) {
	cred, err := newAgentCredential()
	if err != nil {
		return "", authFailResult(err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, doctorRemoteTimeout)
	defer cancel()

	token, err := cred.GetToken(reqCtx, policy.TokenRequestOptions{
		Scopes: []string{foundryAuthScope},
	})
	if err != nil {
		return "", authFailResult(err)
	}

	upn := extractUPNFromJWT(token.Token)
	remaining := time.Until(token.ExpiresOn)
	mins := int(remaining.Round(time.Minute).Minutes())

	// Build the Detail string. The UPN is included on a best-effort
	// basis — JWT parsing can fail for non-AAD-issued tokens or future
	// payload shape changes; in that case the detail just shows the
	// expiry without an identity.
	var detail string
	switch {
	case upn != "" && mins >= 0:
		detail = fmt.Sprintf("%s · token valid for %d minute(s)", upn, mins)
	case upn != "":
		detail = fmt.Sprintf("%s · token expired", upn)
	case mins >= 0:
		detail = fmt.Sprintf("token valid for %d minute(s)", mins)
	default:
		detail = "token expired"
	}

	if remaining < tokenExpiryWarnThreshold {
		// Token is acquired but about to expire. Surface a Warn so the
		// user re-authenticates before the next long-running command
		// 401s mid-flight. Fix command keeps the user moving without
		// signing them out first (no `auth logout`).
		return token.Token, doctorResult{
			ID:     "remote.auth",
			Title:  "Authentication",
			Status: doctorWarn,
			Detail: detail,
			Fix:    "azd auth login",
			Reason: "refresh the token before the next long-running command",
		}
	}

	return token.Token, doctorResult{
		ID:     "remote.auth",
		Title:  "Authentication",
		Status: doctorOK,
		Detail: detail,
	}
}

// authFailResult produces a check-7 Fail row from a credential or
// GetToken error. When stdout is non-TTY the Reason hints at
// `--local-only`, since the most common cause of a doctor auth failure
// in CI is the absence of cached credentials.
func authFailResult(err error) doctorResult {
	reason := "run `azd auth login` to sign in"
	if !isTerminalStdout() {
		reason = "run `azd auth login`, or re-run doctor with `--local-only` to skip remote checks"
	}
	return doctorResult{
		ID:     "remote.auth",
		Title:  "Authentication",
		Status: doctorFail,
		Detail: fmt.Sprintf("token acquisition failed: %v", err),
		Fix:    "azd auth login",
		Reason: reason,
	}
}

// checkReachability performs doctor check 8 — a single
// `GET <endpoint>/agents?api-version=<DefaultAgentAPIVersion>&$top=1`
// against the resolved Foundry endpoint. Skips cleanly when check 7
// did not pass or when AZURE_AI_PROJECT_ENDPOINT was not set in the azd
// environment (check 5).
//
// HTTP status mapping matches the design doc:
//
//   - 2xx → Pass
//   - 401 → Fail (token rejected; recommend `azd auth login`)
//   - 403 → Fail (wrong tenant OR insufficient RBAC; defer to check 10)
//   - 404 → Fail (endpoint URL wrong or project deleted)
//   - 5xx → Warn (service problem, retry)
//   - other / network → Fail (VPN, firewall, typo)
func (a *doctorAction) checkReachability(
	ctx context.Context,
	pre remotePreconditions,
	authStatus doctorStatus,
	bearerToken string,
) doctorResult {
	if authStatus != doctorOK && authStatus != doctorWarn {
		return doctorResult{
			ID:     "remote.reachability",
			Title:  "Foundry project reachability",
			Status: doctorSkip,
			Detail: "skipped: authentication check did not pass",
		}
	}
	if !pre.endpointSet {
		return doctorResult{
			ID:     "remote.reachability",
			Title:  "Foundry project reachability",
			Status: doctorSkip,
			Detail: "skipped: AZURE_AI_PROJECT_ENDPOINT not set",
		}
	}

	probeURL := strings.TrimRight(pre.endpoint, "/") +
		"/agents?api-version=" + DefaultAgentAPIVersion + "&$top=1"

	reqCtx, cancel := context.WithTimeout(ctx, doctorRemoteTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, probeURL, nil)
	if err != nil {
		return doctorResult{
			ID:     "remote.reachability",
			Title:  "Foundry project reachability",
			Status: doctorFail,
			Detail: fmt.Sprintf("failed to build probe request: %v", err),
			Reason: "verify the AZURE_AI_PROJECT_ENDPOINT value is a valid URL",
		}
	}
	req.Header.Set("Authorization", "Bearer "+bearerToken)
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// Network / DNS / TLS failure. We deliberately surface the raw
		// error so misconfigured endpoints ("typo in hostname") and VPN
		// dropouts ("no such host") have actionable detail. Context
		// deadline failures get a tailored reason since users tend to
		// blame "azd is slow" before blaming their VPN.
		reason := "verify VPN / firewall and AZURE_AI_PROJECT_ENDPOINT value"
		if errors.Is(err, context.DeadlineExceeded) {
			reason = fmt.Sprintf(
				"probe timed out after %s — check VPN / firewall reachability",
				doctorRemoteTimeout,
			)
		}
		return doctorResult{
			ID:     "remote.reachability",
			Title:  "Foundry project reachability",
			Status: doctorFail,
			Detail: fmt.Sprintf("network error reaching %s: %v", pre.endpoint, err),
			Reason: reason,
		}
	}
	defer resp.Body.Close()

	return mapReachabilityStatus(resp.StatusCode, pre.endpoint)
}

// mapReachabilityStatus translates the HTTP status returned by the
// reachability probe into a doctorResult. Split out so the mapping is
// easy to test exhaustively without spinning up a fake HTTP server.
func mapReachabilityStatus(statusCode int, endpoint string) doctorResult {
	base := doctorResult{
		ID:    "remote.reachability",
		Title: "Foundry project reachability",
	}
	switch {
	case statusCode >= 200 && statusCode < 300:
		base.Status = doctorOK
		base.Detail = fmt.Sprintf("endpoint reachable (HTTP %d)", statusCode)
		return base
	case statusCode == http.StatusUnauthorized:
		base.Status = doctorFail
		base.Detail = fmt.Sprintf("HTTP 401 from %s — token rejected", endpoint)
		base.Fix = "azd auth login"
		base.Reason = "token expired or scope mismatch; see check 7"
		return base
	case statusCode == http.StatusForbidden:
		base.Status = doctorFail
		base.Detail = fmt.Sprintf("HTTP 403 from %s — caller lacks role at this scope", endpoint)
		base.Reason = "wrong tenant OR insufficient RBAC; see check 10"
		return base
	case statusCode == http.StatusNotFound:
		base.Status = doctorFail
		base.Detail = fmt.Sprintf(
			"HTTP 404 from %s — endpoint URL wrong or project deleted",
			endpoint,
		)
		base.Fix = "azd provision"
		base.Reason = "verify AZURE_AI_PROJECT_ENDPOINT or re-provision the Foundry project"
		return base
	case statusCode >= 500:
		base.Status = doctorWarn
		base.Detail = fmt.Sprintf("HTTP %d from %s — service responded with an error", statusCode, endpoint)
		base.Reason = "retry in a few minutes; if persists, check https://status.azure.com"
		return base
	default:
		base.Status = doctorWarn
		base.Detail = fmt.Sprintf("unexpected HTTP %d from %s", statusCode, endpoint)
		base.Reason = "share the response with azd maintainers if reproducible"
		return base
	}
}

// extractUPNFromJWT decodes the payload segment of a JWT and returns
// the `upn`, `unique_name`, or `preferred_username` claim — the first
// non-empty value in that order. Returns an empty string on any parse
// failure; callers must treat the UPN as best-effort and never depend
// on it for correctness.
//
// We deliberately do not validate the signature or the issuer — doctor
// just wants the identity hint to surface in the rendered "Pass"
// detail. Identity verification happens server-side on every API call.
func extractUPNFromJWT(token string) string {
	if token == "" {
		return ""
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}
	// JWT spec uses base64url with no padding. Some emitters pad
	// anyway, so try the strict form first then fall back.
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		payload, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return ""
		}
	}
	var claims struct {
		UPN               string `json:"upn"`
		UniqueName        string `json:"unique_name"`
		PreferredUsername string `json:"preferred_username"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	if claims.UPN != "" {
		return claims.UPN
	}
	if claims.UniqueName != "" {
		return claims.UniqueName
	}
	return claims.PreferredUsername
}

// checkModelDeployments performs doctor check 9. For each agent service
// in pre.agentServices, the helper parses the service's agent.yaml,
// extracts referenced model deployments, and verifies each is present
// in the Foundry account via the ARM cognitive-services Deployments
// API. Emits one row per service.
//
// Dependency matrix:
//
//   - skip when check 8 (reachability) is Fail/Skip — we cannot trust
//     the project endpoint enough to bother with ARM either
//   - skip per-service when the service's agent.yaml is unreadable
//     (check 6 already flagged this; do not duplicate the fail)
//   - skip the whole check when AZURE_SUBSCRIPTION_ID /
//     AZURE_RESOURCE_GROUP / AZURE_AI_ACCOUNT_NAME are missing — those
//     are written by `azd provision`, so the right action is to provision
//
// The ARM Deployments list is fetched once and shared across services
// to avoid N round-trips when multiple services reference the same
// model deployments.
func (a *doctorAction) checkModelDeployments(
	ctx context.Context,
	pre remotePreconditions,
	reachabilityStatus doctorStatus,
) []doctorResult {
	// Reachability gate. We treat Warn (5xx) as "good enough to try"
	// because ARM and the Foundry data plane fail independently.
	if reachabilityStatus != doctorOK && reachabilityStatus != doctorWarn {
		return []doctorResult{{
			ID:     "remote.models",
			Title:  "Model deployments",
			Status: doctorSkip,
			Detail: "skipped: reachability check did not pass",
		}}
	}
	if len(pre.agentServices) == 0 {
		return []doctorResult{{
			ID:     "remote.models",
			Title:  "Model deployments",
			Status: doctorSkip,
			Detail: "skipped: no agent services detected",
		}}
	}

	// Resolve sub / rg / account from azd env. These are written by
	// `azd provision`. If any is missing, the user needs to provision
	// before this check can run.
	sub := a.getEnvValue(ctx, pre.envName, "AZURE_SUBSCRIPTION_ID")
	rg := a.getEnvValue(ctx, pre.envName, "AZURE_RESOURCE_GROUP")
	account := a.getEnvValue(ctx, pre.envName, "AZURE_AI_ACCOUNT_NAME")
	if sub == "" || rg == "" || account == "" {
		return []doctorResult{{
			ID:     "remote.models",
			Title:  "Model deployments",
			Status: doctorSkip,
			Detail: "skipped: missing AZURE_SUBSCRIPTION_ID / AZURE_RESOURCE_GROUP / AZURE_AI_ACCOUNT_NAME",
			Fix:    "azd provision",
			Reason: "provision the Foundry project to populate the required environment variables",
		}}
	}

	cred, err := newAgentCredential()
	if err != nil {
		return []doctorResult{{
			ID:     "remote.models",
			Title:  "Model deployments",
			Status: doctorFail,
			Detail: fmt.Sprintf("failed to create Azure credential: %v", err),
			Fix:    "azd auth login",
		}}
	}

	listCtx, cancel := context.WithTimeout(ctx, doctorRemoteTimeout)
	defer cancel()
	deployments, err := listProjectDeployments(listCtx, cred, sub, rg, account)
	if err != nil {
		return []doctorResult{{
			ID:     "remote.models",
			Title:  "Model deployments",
			Status: doctorFail,
			Detail: fmt.Sprintf("failed to list deployments for %s/%s/%s: %v", sub, rg, account, err),
			Reason: "verify the Foundry account exists and that you have Reader access",
		}}
	}
	deployed := make(map[string]bool, len(deployments))
	for _, d := range deployments {
		if d.Name != "" {
			deployed[d.Name] = true
		}
	}

	out := make([]doctorResult, 0, len(pre.agentServices))
	for _, svc := range pre.agentServices {
		id := fmt.Sprintf("remote.models.%s", svc.Name)
		title := fmt.Sprintf("Model deployments for %q", svc.Name)

		manifestPath := filepath.Join(pre.projectPath, svc.RelativePath, "agent.yaml")
		data, err := os.ReadFile(manifestPath) //nolint:gosec // G304: path constructed from azd project root
		if err != nil {
			out = append(out, doctorResult{
				ID:     id,
				Title:  title,
				Status: doctorSkip,
				Detail: fmt.Sprintf("skipped: failed to read %s: %v", manifestPath, err),
			})
			continue
		}
		resources, err := agent_yaml.ExtractResourceDefinitions(data)
		if err != nil {
			out = append(out, doctorResult{
				ID:     id,
				Title:  title,
				Status: doctorSkip,
				Detail: fmt.Sprintf("skipped: failed to parse %s: %v", manifestPath, err),
			})
			continue
		}

		var modelRefs []string
		for _, r := range resources {
			if m, ok := r.(agent_yaml.ModelResource); ok && m.Id != "" {
				modelRefs = append(modelRefs, m.Id)
			}
		}

		if len(modelRefs) == 0 {
			out = append(out, doctorResult{
				ID:     id,
				Title:  title,
				Status: doctorOK,
				Detail: "no model references in agent.yaml",
			})
			continue
		}

		var missing []string
		for _, m := range modelRefs {
			if !deployed[m] {
				missing = append(missing, m)
			}
		}
		if len(missing) > 0 {
			out = append(out, doctorResult{
				ID:     id,
				Title:  title,
				Status: doctorFail,
				Detail: fmt.Sprintf("missing: %s", strings.Join(missing, ", ")),
				Fix:    "azd provision",
				Reason: "re-provision to create the missing model deployment(s)",
			})
			continue
		}
		out = append(out, doctorResult{
			ID:     id,
			Title:  title,
			Status: doctorOK,
			Detail: fmt.Sprintf("all %d referenced model(s) present", len(modelRefs)),
		})
	}
	return out
}

// checkAgentStatus performs doctor check 11. For each agent service in
// pre.agentServices, the helper resolves AGENT_<SVC>_NAME from the azd
// environment, calls GET /agents/<name> on the Foundry endpoint, and
// classifies the returned status string.
//
// Dependency matrix:
//
//   - skip when check 7 (auth) is Fail/Skip
//   - skip when check 8 (reachability) is Fail/Skip
//   - skip per-service when AGENT_<SVC>_NAME is empty (i.e., service
//     has not been deployed yet)
//
// Status string classification is intentionally lenient: Foundry can
// emit free-form status strings, so we do a case-insensitive prefix /
// substring match against the small set of values we know about.
func (a *doctorAction) checkAgentStatus(
	ctx context.Context,
	pre remotePreconditions,
	authStatus, reachabilityStatus doctorStatus,
	bearerToken string,
) []doctorResult {
	if authStatus != doctorOK && authStatus != doctorWarn {
		return []doctorResult{{
			ID:     "remote.agent-status",
			Title:  "Agent status",
			Status: doctorSkip,
			Detail: "skipped: authentication check did not pass",
		}}
	}
	if reachabilityStatus != doctorOK && reachabilityStatus != doctorWarn {
		return []doctorResult{{
			ID:     "remote.agent-status",
			Title:  "Agent status",
			Status: doctorSkip,
			Detail: "skipped: reachability check did not pass",
		}}
	}
	if !pre.endpointSet {
		return []doctorResult{{
			ID:     "remote.agent-status",
			Title:  "Agent status",
			Status: doctorSkip,
			Detail: "skipped: AZURE_AI_PROJECT_ENDPOINT not set",
		}}
	}
	if len(pre.agentServices) == 0 {
		return []doctorResult{{
			ID:     "remote.agent-status",
			Title:  "Agent status",
			Status: doctorSkip,
			Detail: "skipped: no agent services detected",
		}}
	}

	cred, err := newAgentCredential()
	if err != nil {
		return []doctorResult{{
			ID:     "remote.agent-status",
			Title:  "Agent status",
			Status: doctorFail,
			Detail: fmt.Sprintf("failed to create Azure credential: %v", err),
			Fix:    "azd auth login",
		}}
	}
	_ = bearerToken // reserved for future reuse; AgentClient handles its own auth pipeline
	client := agent_api.NewAgentClient(pre.endpoint, cred)

	out := make([]doctorResult, 0, len(pre.agentServices))
	for _, svc := range pre.agentServices {
		id := fmt.Sprintf("remote.agent-status.%s", svc.Name)
		title := fmt.Sprintf("Agent status for %q", svc.Name)

		serviceKey := toServiceKey(svc.Name)
		agentName := a.getEnvValue(ctx, pre.envName, fmt.Sprintf("AGENT_%s_NAME", serviceKey))
		if agentName == "" {
			out = append(out, doctorResult{
				ID:     id,
				Title:  title,
				Status: doctorSkip,
				Detail: fmt.Sprintf("skipped: AGENT_%s_NAME not set (deploy this service first)", serviceKey),
				Fix:    "azd deploy",
			})
			continue
		}

		reqCtx, cancelReq := context.WithTimeout(ctx, doctorRemoteTimeout)
		agent, getErr := client.GetAgent(reqCtx, agentName, DefaultAgentAPIVersion)
		cancelReq()
		if getErr != nil {
			out = append(out, classifyAgentGetError(id, title, agentName, getErr))
			continue
		}

		out = append(out, classifyAgentStatus(id, title, agentName, agent))
	}
	return out
}

// classifyAgentGetError maps an error from AgentClient.GetAgent into a
// doctorResult. 404 → Fail with `azd deploy` fix; everything else is a
// generic Fail surfaced with the raw error so the user can diagnose.
func classifyAgentGetError(id, title, agentName string, err error) doctorResult {
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
		return doctorResult{
			ID:     id,
			Title:  title,
			Status: doctorFail,
			Detail: fmt.Sprintf("%s: not found (HTTP 404)", agentName),
			Fix:    "azd deploy",
			Reason: "the agent has not been deployed to this Foundry project yet",
		}
	}
	return doctorResult{
		ID:     id,
		Title:  title,
		Status: doctorFail,
		Detail: fmt.Sprintf("%s: GetAgent failed: %v", agentName, err),
	}
}

// classifyAgentStatus maps the free-form Status field on the latest
// agent version into a doctorResult. The Foundry contract is loose
// (status is `string,omitempty`), so we do case-insensitive comparisons
// against the small set of values seen in practice.
//
// Mapping:
//
//   - empty / "succeeded" / "active" / "ready" → Pass
//   - "deploying" / "provisioning" / "updating" / "creating" /
//     "inprogress" / contains "progress" → Warn
//   - anything else → Fail (status surfaced raw)
func classifyAgentStatus(id, title, agentName string, agent *agent_api.AgentObject) doctorResult {
	latest := agent.Versions.Latest
	status := strings.TrimSpace(strings.ToLower(latest.Status))
	versionLabel := latest.Version
	if versionLabel == "" {
		versionLabel = "(unknown version)"
	}

	switch {
	case status == "" || status == "succeeded" || status == "active" || status == "ready":
		return doctorResult{
			ID:     id,
			Title:  title,
			Status: doctorOK,
			Detail: fmt.Sprintf("%s: active (v%s)", agentName, versionLabel),
		}
	case status == "deploying" ||
		status == "provisioning" ||
		status == "updating" ||
		status == "creating" ||
		status == "inprogress" ||
		strings.Contains(status, "progress"):
		return doctorResult{
			ID:     id,
			Title:  title,
			Status: doctorWarn,
			Detail: fmt.Sprintf("%s: %s (v%s)", agentName, latest.Status, versionLabel),
			Fix:    fmt.Sprintf("azd ai agent monitor %s --follow", agentName),
			Reason: "deployment in progress; monitor until it completes",
		}
	default:
		return doctorResult{
			ID:     id,
			Title:  title,
			Status: doctorFail,
			Detail: fmt.Sprintf("%s: %s (v%s)", agentName, latest.Status, versionLabel),
			Fix:    fmt.Sprintf("azd ai agent monitor %s", agentName),
			Reason: "the latest version is not in a healthy state; inspect logs and redeploy",
		}
	}
}

// getEnvValue fetches a single value from the active azd environment.
// Returns the empty string for any error or empty value so callers can
// use plain `if v == "" { … }` skip semantics.
func (a *doctorAction) getEnvValue(ctx context.Context, envName, key string) string {
	if a.azdClient == nil {
		return ""
	}
	v, err := a.azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: envName,
		Key:     key,
	})
	if err != nil || v == nil {
		return ""
	}
	return v.Value
}
