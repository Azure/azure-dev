// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package doctor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// authProbeTimeout caps the per-probe network call. The design's
// Performance Budget section (`.tmp/pr-8057/azd-ai-agent-doctor-
// remote-checks.md`) allows 2s for cached-token reads and one token-
// refresh round trip; 10s gives generous headroom for slow shells
// without making the user wait through a stuck `azd auth token`
// invocation.
const authProbeTimeout = 10 * time.Second

// authLoginLink is the MS Learn URL for the `azd auth login` command
// reference, reused across every Fail/Warn branch whose suggestion
// is "run `azd auth login`". Keeping it as a package constant ensures
// every branch points to the same canonical doc and prevents drift
// between the four `Result` returns below.
const authLoginLink = "https://learn.microsoft.com/azure/developer/" +
	"azure-developer-cli/reference#azd-auth-login"

// authWarnThreshold is the validity floor below which the check warns
// the user to re-login proactively. Set to 5 minutes so a long-running
// deploy or invoke started immediately after the check has a fresh
// token instead of 401'ing mid-flight.
const authWarnThreshold = 5 * time.Minute

// authScope is the Azure resource scope requested for the probe token.
// It matches the production scope used by `agent_api/operations.go`
// (`https://ai.azure.com/.default`), so a Pass here exactly mirrors
// what the agent invoke flow needs at runtime — not a different scope
// that might succeed when the runtime scope would fail.
const authScope = "https://ai.azure.com/.default"

// authProbeResult is the structured outcome of one auth probe. The
// shape is dictated by the testability split: the production probe
// (realProbeAuth) talks to azidentity and returns the same fields a
// test seam fills synthetically.
//
// upn is the User Principal Name extracted from the access token's
// `upn` / `unique_name` / `preferred_username` / `email` claim
// (whichever is present first). Empty when none of those claims are
// readable — token is still valid, the check just renders without
// the identifier.
//
// validFor is the duration from probe time to token expiry. Zero or
// negative means the token is expired (defensive — GetToken normally
// refreshes before returning, but we surface this honestly if it
// happens).
//
// err captures token acquisition failure. When non-nil the other
// fields are zero-valued; callers must branch on err first.
type authProbeResult struct {
	upn      string
	validFor time.Duration
	err      error
}

// newCheckAuth produces Check `remote.auth`. It runs after the local
// chain because its skip-cascade reads `local.environment-selected`'s
// result from `prior` — without an active env there is no project to
// reason about and the check Skips with "select an env first" rather
// than running unconditionally.
//
// The check itself is intentionally narrow: it answers "does
// `azd auth token` succeed and how long until the token expires?" and
// nothing else. Wrong-tenant detection is the job of check
// `remote.foundry-endpoint` (C12) where a 403 maps to the precise
// "wrong tenant or insufficient RBAC" suggestion. Conflating the two
// here would produce false positives — flagging auth as broken when
// the user IS authenticated, just against the wrong tenant.
//
// Skip-cascade: only on `local.environment-selected`. Per the design
// dependency matrix, an env must be selected before remote checks
// have a project to test against. Other local checks (e.g.,
// `local.grpc-extension`) do not gate auth — the probe uses
// `azidentity.NewAzureDeveloperCLICredential` directly and does not
// require an AzdClient.
func newCheckAuth(deps Dependencies) Check {
	return Check{
		ID:     "remote.auth",
		Name:   "authentication",
		Remote: true,
		Fn: func(ctx context.Context, opts Options, prior []Result) Result {
			if priorBlocked(prior, "local.environment-selected") {
				return Result{
					Status: StatusSkip,
					Message: "skipped: select an azd environment first " +
						"(see check `local.environment-selected`).",
				}
			}

			probe := deps.probeAuth
			if probe == nil {
				probe = realProbeAuth
			}
			probeCtx, cancel := context.WithTimeout(ctx, authProbeTimeout)
			defer cancel()
			res := probe(probeCtx)

			displayUPN := redactUPN(res.upn, opts.Unredacted)

			if res.err != nil {
				// Classify cancellation / timeout separately so we
				// don't tell the user to run `azd auth login` when
				// the real cause is a cancelled doctor command or a
				// probe timeout. `errors.Is` correctly walks the
				// wrap chain that azidentity returns. We check the
				// outer ctx first so user-initiated cancellation
				// (Ctrl-C) shadows the timeout that would also fire.
				if errors.Is(ctx.Err(), context.Canceled) ||
					errors.Is(res.err, context.Canceled) {
					return Result{
						Status:  StatusSkip,
						Message: "skipped: auth probe was cancelled before completion.",
					}
				}
				if errors.Is(probeCtx.Err(), context.DeadlineExceeded) ||
					errors.Is(res.err, context.DeadlineExceeded) {
					return Result{
						Status: StatusFail,
						Message: fmt.Sprintf(
							"token acquisition timed out after %s.",
							authProbeTimeout),
						Suggestion: "Retry `azd ai agent doctor`; if the timeout " +
							"persists, check your network or run " +
							"`azd auth login` to refresh the credential cache.",
					}
				}
				return Result{
					Status:     StatusFail,
					Message:    "token acquisition failed: " + firstLine(res.err.Error()),
					Suggestion: "Run `azd auth login` to authenticate.",
					Links:      []string{authLoginLink},
				}
			}

			if res.validFor <= 0 {
				return Result{
					Status:     StatusFail,
					Message:    composeAuthMessage(displayUPN, "token has expired"),
					Suggestion: "Run `azd auth login` to refresh the token.",
					Links:      []string{authLoginLink},
				}
			}
			minutes := int(res.validFor.Minutes())
			if res.validFor < authWarnThreshold {
				return Result{
					Status: StatusWarn,
					Message: composeAuthMessage(displayUPN,
						"token expires in "+formatTokenWindow(res.validFor)),
					Suggestion: "Run `azd auth login` to refresh the token " +
						"before it expires.",
					Links:   []string{authLoginLink},
					Details: authDetails(res.upn, minutes, opts.Unredacted),
				}
			}
			return Result{
				Status: StatusPass,
				Message: composeAuthMessage(displayUPN,
					"token valid for "+formatTokenWindow(res.validFor)),
				Details: authDetails(res.upn, minutes, opts.Unredacted),
			}
		},
	}
}

// realProbeAuth is the production implementation of the auth probe.
// It constructs the same AzureDeveloperCLICredential the extension
// uses at runtime (`internal/cmd/agent_context.go:103` —
// `newAgentCredential`), requests a token for the production scope,
// and decodes the access token's JWT payload for a UPN claim.
//
// We intentionally use an empty AzureDeveloperCLICredentialOptions
// (no TenantID override) so the probe targets the user's home tenant.
// Wrong-tenant scenarios are detected by check `remote.foundry-endpoint`
// (C12) where a 403 against the project endpoint maps to a precise
// "wrong tenant or insufficient RBAC" suggestion — surfacing the same
// failure mode here would double-report and dilute the diagnosis.
//
// The function never logs the raw token. JWT parsing is delegated to
// `extractUPN`, which returns empty on any decode failure (the token
// is still valid; the check just renders without the identifier).
func realProbeAuth(ctx context.Context) authProbeResult {
	cred, err := azidentity.NewAzureDeveloperCLICredential(
		&azidentity.AzureDeveloperCLICredentialOptions{},
	)
	if err != nil {
		return authProbeResult{err: fmt.Errorf("create credential: %w", err)}
	}
	tok, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{authScope},
	})
	if err != nil {
		return authProbeResult{err: err}
	}
	return authProbeResult{
		upn:      extractUPN(tok.Token),
		validFor: time.Until(tok.ExpiresOn),
	}
}

// extractUPN best-effort decodes a JWT's payload and returns the first
// non-empty UPN-like claim. Order: `upn`, `unique_name`,
// `preferred_username`, `email`. Returns "" on any parse error or
// when none of the claims are present — never an error: the auth
// check cares about the token's validity, not how readable its
// claims are. The raw token is never returned, logged, or otherwise
// exposed by this function.
func extractUPN(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	for _, key := range []string{"upn", "unique_name", "preferred_username", "email"} {
		if v, ok := claims[key].(string); ok {
			if s := strings.TrimSpace(v); s != "" {
				return s
			}
		}
	}
	return ""
}

// composeAuthMessage formats the user-visible Message for the auth
// check, prepending the UPN (when known) so the report identifies the
// authenticated identity at a glance — matching the design's example
// "<UPN> · token valid for <N> minutes".
func composeAuthMessage(upn, body string) string {
	if upn == "" {
		return body
	}
	return upn + " · " + body
}

// redactUPN returns the value to surface in user-facing messages: the
// raw UPN when --unredacted, the shared <redacted> placeholder when a
// UPN was discovered but should be scrubbed, and empty when none was
// found (drops the prefix in composeAuthMessage).
func redactUPN(upn string, unredacted bool) string {
	if upn == "" {
		return ""
	}
	if unredacted {
		return upn
	}
	return redactedPlaceholder
}

// authDetails builds the structured Details map for the auth check.
// The raw UPN only appears when --unredacted is set; otherwise the
// key is omitted entirely so machine consumers do not see the value.
func authDetails(upn string, minutes int, unredacted bool) map[string]any {
	details := map[string]any{"validForMinutes": minutes}
	if unredacted && upn != "" {
		details["upn"] = upn
	}
	return details
}

// formatMinutes renders a minute count with the correct singular/plural unit.
func formatMinutes(n int) string {
	if n == 1 {
		return "1 minute"
	}
	return fmt.Sprintf("%d minutes", n)
}

// formatTokenWindow renders a positive validity duration for the
// user-visible Pass / Warn messages. For sub-minute windows we
// substitute "less than 1 minute" so the message can never read
// "0 minutes" — that wording is indistinguishable from expiry to a
// reader scanning the report quickly and would obscure the Warn
// severity. Sub-second windows are rounded up to the same bucket.
// Callers must have already classified `<= 0` as Fail before calling
// this function.
func formatTokenWindow(d time.Duration) string {
	if d < time.Minute {
		return "less than 1 minute"
	}
	return formatMinutes(int(d.Minutes()))
}

// firstLine returns s up to the first newline (exclusive) with any
// trailing carriage return stripped. Used to elide multi-line stack
// traces returned by azidentity (which on Windows commonly uses CRLF
// because `azd` is invoked via `os/exec`); the doctor report should
// be one line per failure, and the trailing suggestion already tells
// the user what to do.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimRight(s[:i], "\r")
	}
	return s
}
