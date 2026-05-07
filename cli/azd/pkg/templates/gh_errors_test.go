// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package templates

import (
	"errors"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/github"
	"github.com/stretchr/testify/require"
)

// TestSuggestionForApiError_PerKind verifies that every classified
// ApiErrorKind that is meant to surface user guidance produces a non-nil
// *internal.ErrorWithSuggestion containing the substrings users rely on
// (the suggestion text and the relevant doc link). Kinds that intentionally
// return nil (NotFound, Other, Unknown) are also asserted to ensure we
// don't silently start emitting suggestions where none are expected.
func TestSuggestionForApiError_PerKind(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		kind        github.ApiErrorKind
		wantNil     bool
		wantSnippet string // substring required in the rendered suggestion text
		wantLink    string // substring required in at least one Links[].URL (empty = skip)
	}{
		{"SAMLBlocked", github.KindSAMLBlocked, false,
			"SAML SSO", "authenticating-with-single-sign-on"},
		{"RateLimited", github.KindRateLimited, false,
			"rate limit", "rate-limits-for-the-rest-api"},
		{"Unauthorized", github.KindUnauthorized, false,
			"gh auth login", ""},
		{"Forbidden", github.KindForbidden, false,
			"gh auth status", ""},
		{"ServerError", github.KindServerError, false,
			"server error", "githubstatus.com"},
		{"NotFound returns nil — RepoNotAccessibleError handles that path", github.KindNotFound, true, "", ""},
		{"Other returns nil — falls through to typed *ApiError.Error()", github.KindOther, true, "", ""},
		{"Unknown returns nil — surfaces underlying error", github.KindUnknown, true, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			apiErr := &github.ApiError{
				URL:        "https://api.github.com/repos/o/r",
				Kind:       tc.kind,
				StatusCode: 500, // arbitrary, not asserted
			}
			got := suggestionForApiError(apiErr)
			if tc.wantNil {
				require.Nil(t, got, "expected nil suggestion for %s", tc.kind)
				return
			}
			require.NotNil(t, got, "expected non-nil suggestion for %s", tc.kind)
			combined := got.Message + " " + got.Suggestion
			require.Contains(t, combined, tc.wantSnippet)
			if tc.wantLink != "" {
				var found bool
				for _, l := range got.Links {
					if strings.Contains(l.URL, tc.wantLink) {
						found = true
						break
					}
				}
				require.True(t, found, "expected a Links[] URL containing %q, got %+v", tc.wantLink, got.Links)
			}
		})
	}
}

// TestSuggestionForRepoNotAccessible verifies the dedicated
// RepoNotAccessibleError suggestion is emitted with EMU/private-repo guidance
// and a non-empty Suggestion field — Message is intentionally empty so the
// renderer falls back to RepoNotAccessibleError.Error() (which already
// contains the repo slug) and we don't duplicate the same text twice.
func TestSuggestionForRepoNotAccessible(t *testing.T) {
	t.Parallel()
	got := suggestionForRepoNotAccessible(&RepoNotAccessibleError{
		Hostname: "github.com",
		RepoSlug: "owner/repo",
	})
	require.NotNil(t, got)
	require.Empty(t, got.Message, "Message must be empty so renderer uses RepoNotAccessibleError.Error()")
	require.Contains(t, got.Suggestion, "gh auth status")
	require.Contains(t, got.Suggestion, "EMU")
}

// TestWithGitHubSuggestion_Dispatch verifies the wrapper:
//   - returns nil for nil input
//   - wraps *github.ApiError with a classified suggestion (preserving the
//     original error in ErrorWithSuggestion.Err so the chain still unwraps
//     to the typed *ApiError)
//   - wraps *RepoNotAccessibleError into a suggestion (always — never nil,
//     because RepoNotAccessibleError is itself a strong signal)
//   - returns the original error unchanged for unrecognized types
//   - returns the original error unchanged when the typed error has a kind
//     that intentionally has no suggestion (e.g., KindNotFound on its own —
//     RepoNotAccessibleError is the surface for the "repo invisible" case)
func TestWithGitHubSuggestion_Dispatch(t *testing.T) {
	t.Parallel()

	t.Run("nil in nil out", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, withGitHubSuggestion(nil))
	})

	t.Run("ApiError with suggestion-bearing kind is wrapped", func(t *testing.T) {
		t.Parallel()
		original := &github.ApiError{
			URL:        "https://api.github.com/repos/o/r/branches/main",
			Kind:       github.KindSAMLBlocked,
			StatusCode: 403,
		}
		wrapped := withGitHubSuggestion(original)
		ews, ok := errors.AsType[*internal.ErrorWithSuggestion](wrapped)
		require.True(t, ok, "expected *internal.ErrorWithSuggestion, got %T", wrapped)
		// The ErrorWithSuggestion wraps the original so error chain still
		// unwraps to the typed ApiError for downstream consumers.
		got, ok := errors.AsType[*github.ApiError](ews)
		require.True(t, ok, "ErrorWithSuggestion must preserve *ApiError in chain")
		require.Same(t, original, got)
	})

	t.Run("ApiError without suggestion (KindNotFound) returned unchanged", func(t *testing.T) {
		t.Parallel()
		original := &github.ApiError{
			URL:        "https://api.github.com/repos/o/r/branches/main",
			Kind:       github.KindNotFound,
			StatusCode: 404,
		}
		got := withGitHubSuggestion(original)
		require.Same(t, original, got, "no suggestion → return original error untouched")
	})

	t.Run("RepoNotAccessibleError is always wrapped", func(t *testing.T) {
		t.Parallel()
		original := &RepoNotAccessibleError{Hostname: "github.com", RepoSlug: "owner/repo"}
		wrapped := withGitHubSuggestion(original)
		ews, ok := errors.AsType[*internal.ErrorWithSuggestion](wrapped)
		require.True(t, ok)
		// Chain still unwraps to the typed RepoNotAccessibleError.
		got, ok := errors.AsType[*RepoNotAccessibleError](ews)
		require.True(t, ok)
		require.Same(t, original, got)
	})

	t.Run("unrecognized error returned unchanged", func(t *testing.T) {
		t.Parallel()
		original := errors.New("some random error")
		got := withGitHubSuggestion(original)
		require.Same(t, original, got)
	})
}
