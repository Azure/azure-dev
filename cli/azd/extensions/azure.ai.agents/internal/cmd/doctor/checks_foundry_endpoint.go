// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package doctor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// foundryProbeTimeout caps the per-probe HTTP round trip. The design
// (`.tmp/pr-8057/azd-ai-agent-doctor-remote-checks.md`) calls for a
// 10 s ceiling with no retries — doctor is a one-shot diagnostic, not
// a resilient client. The context is the only retry strategy: callers
// can re-run the doctor command. Setting this any longer punishes
// users on stuck DNS / VPN with multi-minute hangs.
const foundryProbeTimeout = 10 * time.Second

// rbacLink is the canonical learn.microsoft.com link surfaced when
// the 403 branch suggests checking `remote.rbac`. Pinned here (rather
// than in checks_auth.go) so the 401 vs 403 link rationale is local
// to the file that owns the disambiguation.
const rbacLink = "https://learn.microsoft.com/azure/ai-foundry/concepts/rbac-azure-ai-foundry"

// foundryProbeResult is the structured outcome of one Foundry
// reachability probe. The production probe (`realProbeFoundryEndpoint`)
// makes one HTTP GET and reports either the HTTP status code or the
// transport error. statusCode is 0 when the request never reached the
// server (DNS failure, TLS error, network unreachable, context
// timeout); a non-zero statusCode means we got an HTTP response and
// err captures only fatal protocol issues unrelated to the status.
//
// requestedURL is the URL we actually GET'd, redacted of any query
// strings beyond the api-version and `limit` parameters. It is
// rendered in the Pass / Fail message so the user can verify the
// probe hit the expected endpoint, while still keeping the surface
// narrow.
type foundryProbeResult struct {
	statusCode   int
	requestedURL string
	err          error
}

// newCheckFoundryEndpoint produces Check `remote.foundry-endpoint`.
// It issues one authenticated GET against
// `<AZURE_AI_PROJECT_ENDPOINT>/agents?api-version=<DefaultAgentAPIVersion>&limit=1`
// and maps the HTTP status code to a precise fix:
//
//   - 200 — Pass: "endpoint reachable (HTTP 200)"
//   - 401 — Fail: "token expired or scope mismatch"; fix:
//     `azd auth login` (cross-references `remote.auth`)
//   - 403 — Fail: "wrong tenant or insufficient RBAC"; fix: confirm
//     active subscription/tenant matches the Foundry project, then
//     see check `remote.rbac` (C16) for role assignments
//   - 404 — Fail: "endpoint wrong or project gone"; fix: `azd
//     provision` or `azd env set AZURE_AI_PROJECT_ENDPOINT`
//   - 5xx — Fail: "Foundry returned <status>"; fix: retry doctor;
//     report to Foundry status if persistent
//   - other status / transport error — Fail: render the error;
//     fix: verify VPN / firewall / typo in the endpoint
//
// Skip-cascade (per design dependency matrix lines 112-117):
//
//  1. `local.project-endpoint-set` — without an endpoint there's no
//     URL to probe.
//  2. `remote.auth` — without a valid token the probe would always
//     401, duplicating `remote.auth`'s diagnosis.
//
// We do NOT additionally gate on `local.environment-selected` because
// `local.project-endpoint-set` already cascades from it. Double-gating
// would surface two skip messages for a single root cause.
func newCheckFoundryEndpoint(deps Dependencies) Check {
	apiVersion := deps.AgentAPIVersion
	return Check{
		ID:     "remote.foundry-endpoint",
		Name:   "Foundry project endpoint reachable",
		Remote: true,
		Fn: func(ctx context.Context, _ Options, prior []Result) Result {
			if priorBlocked(prior, "local.project-endpoint-set") {
				return Result{
					Status: StatusSkip,
					Message: "skipped: AZURE_AI_PROJECT_ENDPOINT is not set " +
						"(see check `local.project-endpoint-set`).",
				}
			}
			if priorBlocked(prior, "remote.auth") {
				return Result{
					Status: StatusSkip,
					Message: "skipped: auth probe did not succeed " +
						"(see check `remote.auth`).",
				}
			}
			endpoint := readProjectEndpoint(prior)
			if endpoint == "" {
				// Defensive: project-endpoint-set passed but its
				// Details didn't carry the value. Skip rather than
				// guess — the design forbids guessing endpoint values.
				return Result{
					Status: StatusSkip,
					Message: "skipped: upstream check passed but did not " +
						"surface AZURE_AI_PROJECT_ENDPOINT in its Details.",
				}
			}
			if apiVersion == "" {
				// Defensive: production wiring must populate this
				// from the package-level DefaultAgentAPIVersion
				// constant. If it didn't, surface a Skip with a
				// clear message instead of guessing or failing
				// with a confusing HTTP error.
				return Result{
					Status: StatusSkip,
					Message: "skipped: doctor wiring did not provide an " +
						"agent API version for the probe.",
				}
			}
			// Validate the endpoint shape BEFORE acquiring a token.
			// A non-HTTPS endpoint would leak the bearer token over
			// plaintext; a relative / malformed URL would either send
			// the token to the wrong host or fail at request build
			// time with a confusing transport error. Catch both cases
			// here with a precise, actionable Fail (no probe, no
			// token acquisition).
			if err := validateFoundryEndpoint(endpoint); err != nil {
				return Result{
					Status: StatusFail,
					Message: fmt.Sprintf(
						"AZURE_AI_PROJECT_ENDPOINT is invalid: %s.",
						err),
					Suggestion: fmt.Sprintf(
						"Set a valid absolute HTTPS endpoint with "+
							"`azd env set AZURE_AI_PROJECT_ENDPOINT "+
							"<https://...>` (currently %q).",
						endpoint),
					Details: map[string]any{
						"endpoint":        endpoint,
						"validationError": err.Error(),
					},
				}
			}

			probe := deps.probeFoundryEndpoint
			if probe == nil {
				probe = makeRealProbeFoundryEndpoint(apiVersion)
			}
			probeCtx, cancel := context.WithTimeout(ctx, foundryProbeTimeout)
			defer cancel()
			res := probe(probeCtx, endpoint)

			// Cancellation / timeout are diagnostic-side issues, not
			// Foundry problems — classify them separately so the user
			// gets the right next step.
			if errors.Is(ctx.Err(), context.Canceled) ||
				errors.Is(res.err, context.Canceled) {
				return Result{
					Status:  StatusSkip,
					Message: "skipped: Foundry reachability probe was cancelled.",
				}
			}
			if errors.Is(probeCtx.Err(), context.DeadlineExceeded) ||
				errors.Is(res.err, context.DeadlineExceeded) {
				return Result{
					Status: StatusFail,
					Message: fmt.Sprintf(
						"Foundry endpoint did not respond within %s.",
						foundryProbeTimeout),
					Suggestion: "Verify your network / VPN, confirm the URL " +
						"in `AZURE_AI_PROJECT_ENDPOINT`, then retry " +
						"`azd ai agent doctor`.",
					Details: foundryDetails(endpoint, res),
				}
			}

			return classifyFoundryResult(endpoint, res)
		},
	}
}

// classifyFoundryResult maps a foundryProbeResult onto a doctor
// Result, leaving the skip-cascade / cancellation / timeout branches
// to the caller. Pulled out as a free function so unit tests can
// pin the status-code → message/suggestion table directly without
// stubbing the probe.
func classifyFoundryResult(endpoint string, res foundryProbeResult) Result {
	details := foundryDetails(endpoint, res)

	if res.err != nil && res.statusCode == 0 {
		return Result{
			Status: StatusFail,
			Message: fmt.Sprintf("could not reach %s: %s",
				endpointHost(endpoint), firstLine(res.err.Error())),
			Suggestion: fmt.Sprintf(
				"Verify network / VPN / firewall reachability and the URL "+
					"in `AZURE_AI_PROJECT_ENDPOINT` (currently %q).",
				endpoint),
			Details: details,
		}
	}

	switch {
	case res.statusCode == http.StatusOK:
		return Result{
			Status:  StatusPass,
			Message: fmt.Sprintf("endpoint reachable (HTTP %d)", res.statusCode),
			Details: details,
		}
	case res.statusCode == http.StatusUnauthorized:
		return Result{
			Status:  StatusFail,
			Message: "Foundry returned HTTP 401 (token expired or scope mismatch).",
			Suggestion: "Run `azd auth login` to refresh credentials; " +
				"if the issue persists, see check `remote.auth`.",
			Links:   []string{authLoginLink},
			Details: details,
		}
	case res.statusCode == http.StatusForbidden:
		return Result{
			Status: StatusFail,
			Message: "Foundry returned HTTP 403 " +
				"(wrong tenant or insufficient RBAC).",
			Suggestion: "Confirm the active azd subscription/tenant matches " +
				"the Foundry project's tenant; if it does, see check " +
				"`remote.rbac` for the role-assignment fix.",
			Links:   []string{rbacLink},
			Details: details,
		}
	case res.statusCode == http.StatusNotFound:
		return Result{
			Status: StatusFail,
			Message: "Foundry returned HTTP 404 " +
				"(endpoint is wrong or the project no longer exists).",
			Suggestion: "Run `azd provision` to (re)create the Foundry " +
				"project, or `azd env set AZURE_AI_PROJECT_ENDPOINT " +
				"<https://...>` to point at an existing one.",
			Details: details,
		}
	case res.statusCode >= 500 && res.statusCode <= 599:
		return Result{
			Status: StatusFail,
			Message: fmt.Sprintf(
				"Foundry returned HTTP %d (service-side error).",
				res.statusCode),
			Suggestion: "Retry `azd ai agent doctor` after a moment; if the " +
				"failure persists, check the Azure AI Foundry status page.",
			Details: details,
		}
	default:
		return Result{
			Status: StatusFail,
			Message: fmt.Sprintf(
				"Foundry returned unexpected HTTP %d.", res.statusCode),
			Suggestion: "Inspect the response in `--verbose` mode and verify " +
				"`AZURE_AI_PROJECT_ENDPOINT` is correct.",
			Details: details,
		}
	}
}

// makeRealProbeFoundryEndpoint returns the production probe closure
// for the given api-version. The api-version is read from the
// package-level DefaultAgentAPIVersion constant by the doctor
// command's Cobra wiring and passed in via
// `Dependencies.AgentAPIVersion`, so drift between the diagnostic
// and the runtime invoke flow is impossible: both pin to the same
// constant.
//
// The closure issues a single GET — no retries. It uses the same
// credential + scope as the production runtime path
// (`internal/cmd/agent_context.go:newAgentCredential` and
// `internal/pkg/agents/agent_api/operations.go`'s
// `https://ai.azure.com/.default` scope) so the doctor's diagnosis
// applies directly to what the runtime invoke flow needs.
//
// The response body is drained but not parsed: we only need the
// status code. Draining lets the underlying HTTP/2 stream be
// returned to the connection pool. The body bytes are never
// surfaced to the user — only the status code and (for transport
// errors) the error message via `firstLine`.
func makeRealProbeFoundryEndpoint(apiVersion string) func(context.Context, string) foundryProbeResult {
	return func(ctx context.Context, endpoint string) foundryProbeResult {
		probeURL, err := buildFoundryProbeURL(endpoint, apiVersion)
		if err != nil {
			return foundryProbeResult{
				err:          fmt.Errorf("build probe URL: %w", err),
				requestedURL: endpoint,
			}
		}

		cred, err := azidentity.NewAzureDeveloperCLICredential(
			&azidentity.AzureDeveloperCLICredentialOptions{},
		)
		if err != nil {
			return foundryProbeResult{
				err:          fmt.Errorf("create credential: %w", err),
				requestedURL: probeURL,
			}
		}
		tok, err := cred.GetToken(ctx, policy.TokenRequestOptions{
			Scopes: []string{authScope},
		})
		if err != nil {
			return foundryProbeResult{err: err, requestedURL: probeURL}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
		if err != nil {
			return foundryProbeResult{err: err, requestedURL: probeURL}
		}
		req.Header.Set("Authorization", "Bearer "+tok.Token)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "azd-ai-agent-doctor")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return foundryProbeResult{err: err, requestedURL: probeURL}
		}
		defer func() {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}()
		return foundryProbeResult{
			statusCode:   resp.StatusCode,
			requestedURL: probeURL,
		}
	}
}

// validateFoundryEndpoint enforces the minimum-safe contract that
// callers (notably `newCheckFoundryEndpoint`) need before sending a
// bearer token: the endpoint must be a well-formed absolute HTTPS URL
// with a non-empty host. This blocks two classes of bug:
//
//  1. Token over plaintext — a stray `http://...` in the env var
//     would otherwise send the Foundry data-plane token over an
//     unencrypted channel.
//  2. Token to the wrong host — a relative URL (`/api/projects/x`)
//     or an opaque string would attach the Authorization header to
//     whatever default base the HTTP client resolves, which is not
//     necessarily Foundry.
//
// Returns nil if the endpoint is safe to probe.
func validateFoundryEndpoint(endpoint string) error {
	trimmed := strings.TrimSpace(endpoint)
	if trimmed == "" {
		return errors.New("endpoint is empty")
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return fmt.Errorf("not a valid URL: %w", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("scheme must be https (got %q)", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("URL has no host")
	}
	return nil
}

// buildFoundryProbeURL joins the user-supplied endpoint with the
// fixed `/agents` path and the canonical api-version / `limit=1`
// query parameters. It parses the endpoint FIRST and then mutates
// the parsed URL's Path / RawQuery / Fragment so user-supplied
// query strings or fragments cannot displace the `/agents` segment:
//
//   - Trailing slashes on the endpoint Path are tolerated (so users
//     who set `.../projects/example/` and `.../projects/example` see
//     the same probe URL).
//   - Any user-supplied `?api-version=foo` (or any other query
//     parameter) is dropped: we overwrite RawQuery wholesale with
//     the canonical pair.
//   - Any user-supplied `#fragment` is dropped: fragments are never
//     sent over the wire and would prevent `/agents` from landing on
//     the URL Path.
//   - The endpoint must be an absolute HTTPS URL with a host. The
//     `newCheckFoundryEndpoint` check already validates this via
//     `validateFoundryEndpoint`; the duplicate check here keeps the
//     builder self-contained so callers in future remote checks
//     cannot accidentally bypass the safety contract.
//
// We use `limit=1` (not `$top=1`) to match the production runtime
// client in `internal/pkg/agents/agent_api/operations.go`, so a Pass
// here proves the same query shape the runtime invoke flow uses.
func buildFoundryProbeURL(endpoint, apiVersion string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return "", err
	}
	if u.Scheme != "https" || u.Host == "" {
		return "", fmt.Errorf("endpoint must be an absolute https URL")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/agents"
	u.Fragment = ""
	u.RawFragment = ""
	q := url.Values{}
	q.Set("api-version", apiVersion)
	q.Set("limit", "1")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// endpointHost returns the host portion of an endpoint URL for use
// in user-visible messages where rendering the full URL (with query
// string) would be noisy. Returns the input verbatim if parsing
// yields an empty host (relative URL, opaque string, or — rarely —
// a genuinely malformed scheme). We'd rather surface a slightly
// ugly message than swallow information when the user is debugging.
func endpointHost(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return endpoint
	}
	return u.Host
}

// readProjectEndpoint pulls the AZURE_AI_PROJECT_ENDPOINT value out
// of the upstream `local.project-endpoint-set` check's Details map.
// Returns "" if not present or not a non-empty string — the caller
// is responsible for deciding whether that is a Skip or a hard fail.
func readProjectEndpoint(prior []Result) string {
	for _, p := range prior {
		if p.ID != "local.project-endpoint-set" {
			continue
		}
		v, ok := p.Details["projectEndpoint"].(string)
		if !ok {
			return ""
		}
		return strings.TrimSpace(v)
	}
	return ""
}

// foundryDetails builds the standard Details map for any Result the
// foundry-endpoint check emits. Centralizing this means a single
// place owns what is and isn't safe to surface in non-interactive
// mode (today: nothing here is secret; we never include the access
// token or the response body).
func foundryDetails(endpoint string, res foundryProbeResult) map[string]any {
	d := map[string]any{
		"endpoint": endpoint,
	}
	if res.requestedURL != "" {
		d["requestedURL"] = res.requestedURL
	}
	if res.statusCode != 0 {
		d["statusCode"] = res.statusCode
	}
	return d
}
