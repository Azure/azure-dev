// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package doctor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// makeFakeJWT builds a minimally valid JWT-shaped token whose middle
// segment decodes to the given claims map. Used by the UPN-extraction
// tests so we don't need to mint a real Azure AD token. The header /
// signature segments are placeholder bytes; the auth check only reads
// the payload.
func makeFakeJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	require.NoError(t, err)
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	body := base64.RawURLEncoding.EncodeToString(payload)
	return header + "." + body + ".sig"
}

// authProbeStub builds a doctor.Dependencies whose probeAuth seam
// returns the supplied authProbeResult. Centralizing this here keeps
// each test focused on its branch of the check's logic.
func authProbeStub(res authProbeResult) Dependencies {
	return Dependencies{
		probeAuth: func(_ context.Context) authProbeResult { return res },
	}
}

func TestCheckAuth_SkipsWhenEnvironmentSelectedFailed(t *testing.T) {
	t.Parallel()

	check := newCheckAuth(authProbeStub(authProbeResult{
		err: errors.New("probe should not have been called"),
	}))
	prior := []Result{{
		ID:     "local.environment-selected",
		Status: StatusFail,
	}}

	got := check.Fn(t.Context(), Options{}, prior)

	require.Equal(t, StatusSkip, got.Status)
	require.Contains(t, got.Message, "select an azd environment")
	require.Contains(t, got.Message, "local.environment-selected")
}

func TestCheckAuth_SkipsWhenEnvironmentSelectedSkipped(t *testing.T) {
	t.Parallel()

	check := newCheckAuth(authProbeStub(authProbeResult{
		err: errors.New("probe should not have been called"),
	}))
	prior := []Result{{
		ID:     "local.environment-selected",
		Status: StatusSkip,
	}}

	got := check.Fn(t.Context(), Options{}, prior)

	require.Equal(t, StatusSkip, got.Status,
		"priorBlocked treats Skip the same as Fail for cascade purposes")
}

func TestCheckAuth_RunsWhenEnvironmentSelectedPassed(t *testing.T) {
	t.Parallel()

	check := newCheckAuth(authProbeStub(authProbeResult{
		upn:      "user@contoso.com",
		validFor: 47 * time.Minute,
	}))
	prior := []Result{{
		ID:     "local.environment-selected",
		Status: StatusPass,
	}}

	got := check.Fn(t.Context(), Options{Unredacted: true}, prior)

	require.Equal(t, StatusPass, got.Status)
	require.Equal(t, "user@contoso.com · token valid for 47 minutes", got.Message)
	require.Equal(t, 47, got.Details["validForMinutes"])
	require.Equal(t, "user@contoso.com", got.Details["upn"])
}

func TestCheckAuth_FailsOnTokenAcquisitionError(t *testing.T) {
	t.Parallel()

	check := newCheckAuth(authProbeStub(authProbeResult{
		err: errors.New("DefaultAzureCredential: failed to acquire a token"),
	}))

	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusFail, got.Status)
	require.Contains(t, got.Message, "token acquisition failed")
	require.Contains(t, got.Message, "failed to acquire a token")
	require.Equal(t, "Run `azd auth login` to authenticate.", got.Suggestion)
	require.NotEmpty(t, got.Links)
}

func TestCheckAuth_FailErrorMessageStripsTrailingLines(t *testing.T) {
	t.Parallel()

	multi := "primary cause\nstack frame 1\nstack frame 2"
	check := newCheckAuth(authProbeStub(authProbeResult{err: errors.New(multi)}))

	got := check.Fn(t.Context(), Options{}, nil)

	require.Contains(t, got.Message, "primary cause")
	require.NotContains(t, got.Message, "stack frame 1",
		"firstLine should elide multi-line stack traces from the report")
}

func TestCheckAuth_FailsOnExpiredToken(t *testing.T) {
	t.Parallel()

	check := newCheckAuth(authProbeStub(authProbeResult{
		upn:      "user@contoso.com",
		validFor: -2 * time.Minute,
	}))

	got := check.Fn(t.Context(), Options{Unredacted: true}, nil)

	require.Equal(t, StatusFail, got.Status)
	require.Contains(t, got.Message, "user@contoso.com")
	require.Contains(t, got.Message, "expired")
	require.Equal(t, "Run `azd auth login` to refresh the token.", got.Suggestion)
	require.NotEmpty(t, got.Links,
		"every Fail branch that suggests `azd auth login` must include the reference link")
}

func TestCheckAuth_WarnsWhenTokenExpiresSoon(t *testing.T) {
	t.Parallel()

	check := newCheckAuth(authProbeStub(authProbeResult{
		upn:      "user@contoso.com",
		validFor: 2 * time.Minute,
	}))

	got := check.Fn(t.Context(), Options{Unredacted: true}, nil)

	require.Equal(t, StatusWarn, got.Status)
	require.Contains(t, got.Message, "token expires in 2 minutes")
	require.Contains(t, got.Suggestion, "Run `azd auth login`")
	require.Equal(t, 2, got.Details["validForMinutes"])
	require.Equal(t, "user@contoso.com", got.Details["upn"])
}

func TestCheckAuth_WarnsAtExactlyOneMinute(t *testing.T) {
	t.Parallel()

	// Exercise the singular-unit branch in formatMinutes alongside
	// the < 5 minute warn threshold.
	check := newCheckAuth(authProbeStub(authProbeResult{
		upn:      "user@contoso.com",
		validFor: 90 * time.Second, // int(Minutes()) == 1
	}))

	got := check.Fn(t.Context(), Options{Unredacted: true}, nil)

	require.Equal(t, StatusWarn, got.Status)
	require.Contains(t, got.Message, "token expires in 1 minute")
	require.NotContains(t, got.Message, "1 minutes")
}

// TestCheckAuth_WarnSubMinuteRendersLessThanOneMinute guards against
// the rendering bug where `int((30s).Minutes()) == 0` would surface
// as "token expires in 0 minutes" — indistinguishable from expiry to
// a reader scanning the report quickly. formatTokenWindow substitutes
// "less than 1 minute" for any sub-minute positive window so the Warn
// severity stays legible.
func TestCheckAuth_WarnSubMinuteRendersLessThanOneMinute(t *testing.T) {
	t.Parallel()

	check := newCheckAuth(authProbeStub(authProbeResult{
		upn:      "user@contoso.com",
		validFor: 30 * time.Second,
	}))

	got := check.Fn(t.Context(), Options{Unredacted: true}, nil)

	require.Equal(t, StatusWarn, got.Status,
		"30s of validity is positive — must be Warn, not Fail")
	require.Contains(t, got.Message, "less than 1 minute")
	require.NotContains(t, got.Message, "0 minutes",
		"sub-minute windows must not render as `0 minutes` (ambiguous with expiry)")
}

// TestCheckAuth_WarnPassBoundaryAtFiveMinutes pins the < / >= split
// at the authWarnThreshold so a future refactor from `<` to `<=`
// can't silently demote a Pass into a Warn.
func TestCheckAuth_WarnPassBoundaryAtFiveMinutes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		validFor time.Duration
		want     Status
	}{
		{"just under 5m is Warn", 5*time.Minute - 1, StatusWarn},
		{"exactly 5m is Pass", 5 * time.Minute, StatusPass},
		{"just over 5m is Pass", 5*time.Minute + 1, StatusPass},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			check := newCheckAuth(authProbeStub(authProbeResult{
				upn:      "user@contoso.com",
				validFor: tc.validFor,
			}))
			got := check.Fn(t.Context(), Options{Unredacted: true}, nil)
			require.Equal(t, tc.want, got.Status)
		})
	}
}

// TestCheckAuth_SkipsOnUserCancellation proves that a cancelled outer
// context maps the probe error to a Skip (not a Fail with
// `azd auth login`). Without this branch, hitting Ctrl-C during the
// doctor command would leave the user with a misleading "auth broken"
// diagnosis.
func TestCheckAuth_SkipsOnUserCancellation(t *testing.T) {
	t.Parallel()

	check := newCheckAuth(Dependencies{
		probeAuth: func(ctx context.Context) authProbeResult {
			<-ctx.Done()
			return authProbeResult{err: ctx.Err()}
		},
	})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	got := check.Fn(ctx, Options{}, nil)

	require.Equal(t, StatusSkip, got.Status)
	require.Contains(t, got.Message, "cancelled")
	require.NotContains(t, got.Suggestion, "azd auth login",
		"cancellation must NOT recommend `azd auth login`")
}

// TestCheckAuth_FailsOnProbeTimeoutWithDistinctMessage proves that
// deadline-exceeded errors surface as a Fail with a timeout-specific
// message (not "token acquisition failed: context deadline exceeded"
// which falsely implies an auth problem).
func TestCheckAuth_FailsOnProbeTimeoutWithDistinctMessage(t *testing.T) {
	t.Parallel()

	check := newCheckAuth(Dependencies{
		probeAuth: func(ctx context.Context) authProbeResult {
			// Simulate the probe noticing the parent deadline.
			return authProbeResult{err: context.DeadlineExceeded}
		},
	})

	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusFail, got.Status)
	require.Contains(t, got.Message, "timed out")
	require.NotContains(t, got.Message, "token acquisition failed",
		"timeout must not be reported as a generic acquisition failure")
	require.Contains(t, got.Suggestion, "Retry `azd ai agent doctor`")
}

func TestCheckAuth_PassesWithoutUPN(t *testing.T) {
	t.Parallel()

	check := newCheckAuth(authProbeStub(authProbeResult{
		validFor: 60 * time.Minute,
	}))

	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusPass, got.Status)
	require.Equal(t, "token valid for 60 minutes", got.Message,
		"with no UPN the message should not have the ` · ` separator")
}

// TestCheckAuth_RedactsUPNByDefault pins the doctor redaction contract for
// the auth check: with --unredacted absent (Options{Unredacted: false}) the
// raw UPN must not appear in Message or Details on any of the PASS / WARN /
// expired-FAIL branches that surface it. The placeholder substitutes so
// readers can still see that a UPN was discovered.
func TestCheckAuth_RedactsUPNByDefault(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		validFor   time.Duration
		wantStatus Status
	}{
		{"pass branch", 60 * time.Minute, StatusPass},
		{"warn branch", 2 * time.Minute, StatusWarn},
		{"expired fail branch", -2 * time.Minute, StatusFail},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			check := newCheckAuth(authProbeStub(authProbeResult{
				upn:      "user@contoso.com",
				validFor: tc.validFor,
			}))

			got := check.Fn(t.Context(), Options{}, nil)

			require.Equal(t, tc.wantStatus, got.Status)
			require.NotContains(t, got.Message, "user@contoso.com",
				"raw UPN must not appear in Message without --unredacted")
			require.Contains(t, got.Message, redactedPlaceholder,
				"redacted placeholder must signal that a UPN was found")
			if tc.wantStatus != StatusFail {
				require.NotContains(t, got.Details, "upn",
					"raw UPN must not appear in Details without --unredacted")
			}
		})
	}
}

// TestCheckAuth_UnredactedKeepsUPN confirms the --unredacted flag still
// surfaces the raw UPN in both Message and Details on the same branches.
func TestCheckAuth_UnredactedKeepsUPN(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		validFor time.Duration
		wantStat Status
	}{
		{"pass branch", 60 * time.Minute, StatusPass},
		{"warn branch", 2 * time.Minute, StatusWarn},
		{"expired fail branch", -2 * time.Minute, StatusFail},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			check := newCheckAuth(authProbeStub(authProbeResult{
				upn:      "user@contoso.com",
				validFor: tc.validFor,
			}))

			got := check.Fn(t.Context(), Options{Unredacted: true}, nil)

			require.Equal(t, tc.wantStat, got.Status)
			require.Contains(t, got.Message, "user@contoso.com")
			require.NotContains(t, got.Message, redactedPlaceholder)
			if tc.wantStat != StatusFail {
				require.Equal(t, "user@contoso.com", got.Details["upn"])
			}
		})
	}
}

// TestCheckAuth_RedactionWithoutUPNDropsPrefix ensures the redacted
// placeholder is not added when no UPN was found at all — the message
// must remain "token valid for 60 minutes" without any " · " separator.
func TestCheckAuth_RedactionWithoutUPNDropsPrefix(t *testing.T) {
	t.Parallel()

	check := newCheckAuth(authProbeStub(authProbeResult{
		validFor: 60 * time.Minute,
	}))

	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusPass, got.Status)
	require.Equal(t, "token valid for 60 minutes", got.Message)
	require.NotContains(t, got.Message, redactedPlaceholder,
		"no placeholder should appear when no UPN was found")
	require.NotContains(t, got.Details, "upn")
}

func TestCheckAuth_UsesDefaultProbeWhenSeamNotInjected(t *testing.T) {
	t.Parallel()

	// When deps.probeAuth is nil the check must fall back to
	// realProbeAuth — i.e. the closure must not panic on the nil
	// function value. We feed an already-cancelled ctx so the call
	// returns quickly regardless of the host's auth state, and we
	// assert the cancellation classification kicks in (StatusSkip)
	// rather than relying on whatever azd-login state the test host
	// happens to be in.
	check := newCheckAuth(Dependencies{})
	require.NotNil(t, check.Fn)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	got := check.Fn(ctx, Options{}, nil)

	require.Equal(t, StatusSkip, got.Status,
		"cancelled ctx must classify as Skip even via the default probe")
	require.Contains(t, got.Message, "cancelled")
}

// ---- extractUPN ----

func TestExtractUPN_PrefersUpnClaim(t *testing.T) {
	t.Parallel()

	tok := makeFakeJWT(t, map[string]any{
		"upn":                "alice@contoso.com",
		"unique_name":        "alice.unique",
		"preferred_username": "alice.preferred",
		"email":              "alice.email",
	})

	require.Equal(t, "alice@contoso.com", extractUPN(tok))
}

func TestExtractUPN_FallsThroughClaims(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		claims map[string]any
		want   string
	}{
		{
			name:   "unique_name when upn missing",
			claims: map[string]any{"unique_name": "u1@contoso.com", "email": "x@contoso.com"},
			want:   "u1@contoso.com",
		},
		{
			name:   "preferred_username when upn and unique_name missing",
			claims: map[string]any{"preferred_username": "u2@contoso.com", "email": "x@contoso.com"},
			want:   "u2@contoso.com",
		},
		{
			name:   "email as last resort",
			claims: map[string]any{"email": "u3@contoso.com"},
			want:   "u3@contoso.com",
		},
		{
			name:   "empty upn skipped, falls to next claim",
			claims: map[string]any{"upn": "", "unique_name": "u4@contoso.com"},
			want:   "u4@contoso.com",
		},
		{
			name:   "whitespace upn skipped, falls to next claim",
			claims: map[string]any{"upn": "   ", "email": "u5@contoso.com"},
			want:   "u5@contoso.com",
		},
		{
			name:   "no UPN-like claims",
			claims: map[string]any{"sub": "abc", "aud": "xyz"},
			want:   "",
		},
		{
			name:   "non-string upn claim is ignored",
			claims: map[string]any{"upn": 12345, "email": "u6@contoso.com"},
			want:   "u6@contoso.com",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, extractUPN(makeFakeJWT(t, tc.claims)))
		})
	}
}

func TestExtractUPN_HandlesMalformedTokens(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		tok  string
	}{
		{name: "empty token", tok: ""},
		{name: "one segment", tok: "abc"},
		{name: "two segments", tok: "abc.def"},
		{name: "four segments", tok: "a.b.c.d"},
		{name: "invalid base64 payload", tok: "header.!!!notbase64!!!.sig"},
		{name: "valid base64 but not JSON", tok: "header." +
			base64.RawURLEncoding.EncodeToString([]byte("not json")) + ".sig"},
		{name: "JSON array instead of object", tok: "header." +
			base64.RawURLEncoding.EncodeToString([]byte(`["array"]`)) + ".sig"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Empty(t, extractUPN(tc.tok),
				"malformed token must return empty UPN, never panic")
		})
	}
}

// ---- formatMinutes & helpers ----

func TestFormatMinutes(t *testing.T) {
	t.Parallel()

	require.Equal(t, "0 minutes", formatMinutes(0))
	require.Equal(t, "1 minute", formatMinutes(1))
	require.Equal(t, "2 minutes", formatMinutes(2))
	require.Equal(t, "60 minutes", formatMinutes(60))
}

// TestFormatTokenWindow pins the sub-minute substitution that keeps
// the Warn message legible. Anything `>= 1 minute` falls through to
// formatMinutes' regular rendering; anything `< 1 minute` (assumed
// positive — callers must classify `<= 0` as Fail before this) maps
// to a fixed "less than 1 minute" string. The Pass / Warn branches in
// newCheckAuth both rely on this contract.
func TestFormatTokenWindow(t *testing.T) {
	t.Parallel()

	require.Equal(t, "less than 1 minute", formatTokenWindow(time.Second))
	require.Equal(t, "less than 1 minute", formatTokenWindow(59*time.Second))
	require.Equal(t, "1 minute", formatTokenWindow(time.Minute))
	require.Equal(t, "1 minute", formatTokenWindow(90*time.Second))
	require.Equal(t, "47 minutes", formatTokenWindow(47*time.Minute))
}

func TestComposeAuthMessage(t *testing.T) {
	t.Parallel()

	require.Equal(t, "token valid for 5 minutes",
		composeAuthMessage("", "token valid for 5 minutes"))
	require.Equal(t, "alice@contoso.com · token valid for 5 minutes",
		composeAuthMessage("alice@contoso.com", "token valid for 5 minutes"))
}

func TestRedactUPN(t *testing.T) {
	t.Parallel()

	require.Equal(t, "", redactUPN("", false))
	require.Equal(t, "", redactUPN("", true))
	require.Equal(t, redactedPlaceholder, redactUPN("alice@contoso.com", false))
	require.Equal(t, "alice@contoso.com", redactUPN("alice@contoso.com", true))
}

func TestAuthDetails(t *testing.T) {
	t.Parallel()

	redacted := authDetails("alice@contoso.com", 42, false)
	require.Equal(t, 42, redacted["validForMinutes"])
	require.NotContains(t, redacted, "upn",
		"upn key must be omitted when --unredacted is false")

	unredacted := authDetails("alice@contoso.com", 42, true)
	require.Equal(t, 42, unredacted["validForMinutes"])
	require.Equal(t, "alice@contoso.com", unredacted["upn"])

	missing := authDetails("", 42, true)
	require.Equal(t, 42, missing["validForMinutes"])
	require.NotContains(t, missing, "upn",
		"upn key must be omitted when no UPN was found, even with --unredacted")
}

func TestFirstLine(t *testing.T) {
	t.Parallel()

	require.Equal(t, "single line", firstLine("single line"))
	require.Equal(t, "first", firstLine("first\nsecond\nthird"))
	// Windows / CRLF case: azidentity invokes `azd` via os/exec and
	// stderr output on Windows commonly arrives with \r\n line endings.
	// firstLine must strip the trailing \r so terminal renderers don't
	// drop or garble the doctor message.
	require.Equal(t, "first", firstLine("first\r\nsecond"))
	require.Equal(t, "trailing \\r alone\r", firstLine("trailing \\r alone\r"),
		"a lone trailing \\r without a following \\n is left in place — "+
			"only newline-stripped lines get the \\r trimmed")
	require.Equal(t, "", firstLine("\ntrailing"),
		"empty-first-line is preserved — caller's responsibility")
	require.Equal(t, "", firstLine(""))
	require.Equal(t, "no newline at end",
		firstLine(strings.TrimRight("no newline at end\n", "\n")))
}
