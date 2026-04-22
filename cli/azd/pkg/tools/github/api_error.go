// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package github

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ApiErrorKind classifies the cause of a `gh api` failure. A single error
// has exactly one Kind. Use the predicate methods on *ApiError (IsAuthError,
// IsNotFound) for boolean checks; switch on Kind when you need to distinguish
// SAML vs. plain auth vs. rate-limit.
type ApiErrorKind int

const (
	// KindUnknown indicates the failure couldn't be classified — typically
	// because gh failed before issuing the request (network error, missing
	// binary, ctx cancelled) and there's no HTTP status to inspect.
	KindUnknown ApiErrorKind = iota
	// KindSAMLBlocked indicates the owning organization enforces SAML SSO
	// and the caller's token has not been authorized for it. Resolution
	// requires an out-of-band step in the GitHub UI; `gh auth login` alone
	// does not fix it.
	KindSAMLBlocked
	// KindRateLimited indicates GitHub rejected the request because rate
	// limits were exceeded. Authenticated requests have higher limits.
	KindRateLimited
	// KindUnauthorized indicates HTTP 401 — the request was unauthenticated
	// or the token is invalid/expired.
	KindUnauthorized
	// KindForbidden indicates HTTP 403 that is NOT SAML or rate-limit
	// related — typically missing token scopes or a permission denial.
	KindForbidden
	// KindNotFound indicates HTTP 404 — the resource doesn't exist OR is
	// private and the caller isn't authorized to know it exists.
	KindNotFound
	// KindServerError indicates a 5xx response from GitHub.
	KindServerError
	// KindOther indicates a non-2xx response that doesn't fit any of the
	// categories above (e.g., 4xx codes other than 401/403/404).
	KindOther
)

// String returns a human-readable name for the kind, used in error messages.
func (k ApiErrorKind) String() string {
	switch k {
	case KindSAMLBlocked:
		return "SAMLBlocked"
	case KindRateLimited:
		return "RateLimited"
	case KindUnauthorized:
		return "Unauthorized"
	case KindForbidden:
		return "Forbidden"
	case KindNotFound:
		return "NotFound"
	case KindServerError:
		return "ServerError"
	case KindOther:
		return "Other"
	default:
		return "Unknown"
	}
}

// ApiError represents a structured error returned from a `gh api` invocation.
//
// It is built from the underlying gh CLI's output (the JSON body that GitHub's
// REST API writes to stdout on errors, plus the human-readable stderr) so
// callers can branch on the failure mode without doing brittle substring
// matching against opaque error strings.
//
// Use Kind for switch-based dispatch and the IsAuthError / IsNotFound
// predicates for common boolean checks.
type ApiError struct {
	// URL is the API URL that was requested (e.g., "https://api.github.com/repos/o/r/branches/main").
	URL string
	// Kind classifies the failure (SAML vs. rate-limit vs. plain 4xx etc.).
	Kind ApiErrorKind
	// StatusCode is the HTTP status code returned by the GitHub API (e.g., 401, 403, 404).
	// It is 0 when no status code could be parsed (e.g., gh failed before issuing the request).
	StatusCode int
	// Message is the human-readable message GitHub returned in the JSON
	// error body (e.g., "Resource protected by organization SAML enforcement.").
	// Empty when the failure was not an HTTP-level error (e.g., gh not installed).
	Message string
	// Stderr is the raw stderr output from `gh api`, kept for diagnostics.
	Stderr string
	// Underlying is the original error from the command runner (typically *exec.ExitError).
	Underlying error
}

// Error implements the error interface.
func (e *ApiError) Error() string {
	switch e.Kind {
	case KindSAMLBlocked:
		return fmt.Sprintf("gh api %s: SAML SSO enforcement blocked the request (HTTP %d)", e.URL, e.StatusCode)
	case KindRateLimited:
		return fmt.Sprintf("gh api %s: GitHub API rate limit exceeded (HTTP %d)", e.URL, e.StatusCode)
	case KindUnknown:
		return fmt.Sprintf("gh api %s: %s", e.URL, e.Underlying)
	default:
		if e.Message != "" {
			return fmt.Sprintf("gh api %s: HTTP %d: %s", e.URL, e.StatusCode, e.Message)
		}
		return fmt.Sprintf("gh api %s: HTTP %d", e.URL, e.StatusCode)
	}
}

// Unwrap exposes the underlying command-runner error so errors.Is/errors.As
// continue to work against the original error chain.
func (e *ApiError) Unwrap() error {
	return e.Underlying
}

// IsAuthError reports whether the error indicates an authentication or
// authorization failure (401, 403, or SAML enforcement).
func (e *ApiError) IsAuthError() bool {
	switch e.Kind {
	case KindUnauthorized, KindForbidden, KindSAMLBlocked:
		return true
	default:
		return false
	}
}

// IsNotFound reports whether the API returned 404.
func (e *ApiError) IsNotFound() bool {
	return e.Kind == KindNotFound
}

// httpStatusRe matches gh CLI's standard "(HTTP <code>)" suffix that appears
// at the end of error messages from `gh api` when the request was issued and
// returned a non-2xx response. Example:
//
//	gh: Resource protected by organization SAML enforcement. ... (HTTP 403)
var httpStatusRe = regexp.MustCompile(`\(HTTP (\d{3})\)`)

// githubErrorBody mirrors the structured error envelope GitHub's REST API
// writes to stdout for non-2xx responses. Only fields we care about are
// captured; unknown fields are ignored.
type githubErrorBody struct {
	Message string `json:"message"`
	// Status is a string in the response (e.g., "403"), not an int.
	Status string `json:"status"`
}

// parseApiError converts a failed `gh api` invocation into a structured
// *ApiError. stdout typically contains GitHub's JSON error envelope and
// stderr contains gh's human-readable rendering with the "(HTTP NNN)" suffix.
// Both are inspected: the JSON body is the primary signal (stable, structured),
// stderr is a fallback for cases where stdout isn't valid JSON (gh failed
// before issuing the request, network error, etc.).
//
// Returns nil if err is nil. Always returns a non-nil *ApiError otherwise so
// callers can rely on errors.AsType[*ApiError] succeeding for any failed
// `gh api` call.
func parseApiError(url, stdout, stderr string, err error) *ApiError {
	if err == nil {
		return nil
	}

	apiErr := &ApiError{URL: url, Stderr: stderr, Underlying: err}

	// Primary signal: GitHub's JSON error envelope on stdout.
	if body := parseGitHubErrorBody(stdout); body != nil {
		apiErr.Message = body.Message
		if code, convErr := strconv.Atoi(body.Status); convErr == nil {
			apiErr.StatusCode = code
		}
	}

	// Fallback: stderr "(HTTP NNN)" marker. Always check, in case the JSON
	// body was missing or malformed (e.g., proxy returning HTML).
	if apiErr.StatusCode == 0 {
		if m := httpStatusRe.FindStringSubmatch(stderr); len(m) == 2 {
			if code, convErr := strconv.Atoi(m[1]); convErr == nil {
				apiErr.StatusCode = code
			}
		}
	}

	apiErr.Kind = classifyKind(apiErr.StatusCode, apiErr.Message+"\n"+stderr)
	return apiErr
}

// parseGitHubErrorBody returns the GitHub JSON error envelope if stdout
// contains one, otherwise nil. We tolerate leading/trailing whitespace and
// require both Message and Status to be non-empty so we don't misinterpret
// arbitrary JSON success bodies as errors.
func parseGitHubErrorBody(stdout string) *githubErrorBody {
	stdout = strings.TrimSpace(stdout)
	if !strings.HasPrefix(stdout, "{") {
		return nil
	}
	var body githubErrorBody
	if err := json.Unmarshal([]byte(stdout), &body); err != nil {
		return nil
	}
	if body.Message == "" || body.Status == "" {
		return nil
	}
	return &body
}

// classifyKind picks an ApiErrorKind from the status code and the combined
// message + stderr text. SAML and rate-limit checks take precedence over the
// raw status code (both surface as 403 from GitHub) so the more specific
// classification wins. Phrase matching is intentionally narrow (e.g., requires
// "saml enforcement", not bare "saml") to avoid misclassifying repo names
// that happen to contain those substrings.
func classifyKind(statusCode int, text string) ApiErrorKind {
	lower := strings.ToLower(text)

	if strings.Contains(lower, "saml enforcement") ||
		strings.Contains(lower, "saml sso") ||
		strings.Contains(lower, "sso authorization") ||
		strings.Contains(lower, "sso required") {
		return KindSAMLBlocked
	}
	if strings.Contains(lower, "api rate limit exceeded") ||
		strings.Contains(lower, "secondary rate limit") {
		return KindRateLimited
	}

	switch {
	case statusCode == 0:
		return KindUnknown
	case statusCode == 401:
		return KindUnauthorized
	case statusCode == 403:
		return KindForbidden
	case statusCode == 404:
		return KindNotFound
	case statusCode >= 500:
		return KindServerError
	default:
		return KindOther
	}
}
