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
	"strings"
	"time"

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
//   - check 7 (auth) gates 8, 10, 11, 12
//   - check 8 (reachability) gates 9, 11
//   - check 11 (agent status) gates 12
//
// R2-C ships only checks 7 + 8. Later phases append 9 / 10 / 11 / 12 to
// the slice in dependency order; each check inspects the prior results
// to decide Pass vs Skip.
func (a *doctorAction) runRemoteChecks(ctx context.Context, pre remotePreconditions) []doctorResult {
	if a.flags != nil && a.flags.localOnly {
		return remoteSkipRows("skipped: --local-only")
	}

	out := make([]doctorResult, 0, 2)

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
	out = append(out, timed(func() doctorResult {
		return a.checkReachability(ctx, pre, authResult.Status, bearerToken)
	}))

	return out
}

// remoteSkipRows returns the design's six remote-check Skip placeholders
// pre-filled with `reason`. R2-C only owns 7 + 8; later phases will
// extend the slice as the matching checks land.
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
