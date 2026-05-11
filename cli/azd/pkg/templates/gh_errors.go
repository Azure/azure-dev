// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package templates

import (
	"errors"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/github"
)

// withGitHubSuggestion wraps the supplied error in an *internal.ErrorWithSuggestion
// when the error is a recognized GitHub failure (typed *github.ApiError or
// *RepoNotAccessibleError). The wrapping carries actionable guidance inline
// in the error chain, which means:
//
//   - The core CLI ErrorMiddleware renders it directly without consulting the
//     YAML rules pipeline (ErrorMiddleware short-circuits on
//     *ErrorWithSuggestion).
//   - Callers that don't have access to the YAML pipeline (e.g., extensions
//     receiving stringified errors over gRPC) still see the suggestion as part
//     of the wrapped error, because *ApiError keeps formatting itself with
//     status + message.
//
// Returns the original error unchanged when no specific suggestion applies, so
// other typed-error pipelines (auth, unknown gh failures) behave as before.
func withGitHubSuggestion(err error) error {
	if err == nil {
		return nil
	}

	if apiErr, ok := errors.AsType[*github.ApiError](err); ok {
		if s := suggestionForApiError(apiErr); s != nil {
			s.Err = err
			return s
		}
	}

	if repoErr, ok := errors.AsType[*RepoNotAccessibleError](err); ok {
		s := suggestionForRepoNotAccessible(repoErr)
		s.Err = err
		return s
	}

	return err
}

func suggestionForApiError(apiErr *github.ApiError) *internal.ErrorWithSuggestion {
	switch apiErr.Kind {
	case github.KindSAMLBlocked:
		return &internal.ErrorWithSuggestion{
			Message: "The GitHub organization that owns this repository requires SAML SSO " +
				"authorization for your token before it can be used.",
			Suggestion: "If you signed in with `gh auth login`, run `gh auth refresh` and " +
				"complete the SSO authorization in the browser when prompted (use " +
				"`gh auth refresh -h <host>` for non-default hosts). If you're using a " +
				"personal access token, open your GitHub token settings, locate the " +
				"token, click 'Configure SSO', and authorize the organization that owns " +
				"this repository.",
			Links: []errorhandler.ErrorLink{
				{
					URL: "https://docs.github.com/enterprise-cloud@latest/authentication/" +
						"authenticating-with-single-sign-on/" +
						"about-authentication-with-single-sign-on",
					Title: "About authentication with single sign-on",
				},
				{
					URL: "https://docs.github.com/enterprise-cloud@latest/authentication/" +
						"authenticating-with-single-sign-on/" +
						"authorizing-a-personal-access-token-for-use-with-single-sign-on",
					Title: "Authorizing a personal access token for use with single sign-on",
				},
			},
		}
	case github.KindRateLimited:
		return &internal.ErrorWithSuggestion{
			Message: "GitHub API rate limit exceeded.",
			Suggestion: "Authenticated requests have a much higher limit than anonymous ones. " +
				"Run `gh auth login` (or set GITHUB_TOKEN / GH_TOKEN) and retry. " +
				"If you're already authenticated, wait for the rate-limit window to reset " +
				"(typically up to one hour).",
			Links: []errorhandler.ErrorLink{
				{
					URL:   "https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api",
					Title: "Rate limits for the REST API",
				},
			},
		}
	case github.KindUnauthorized:
		return &internal.ErrorWithSuggestion{
			Message: "GitHub rejected the request as unauthenticated (HTTP 401).",
			Suggestion: "Run `gh auth login` to sign in, or refresh an expired token with " +
				"`gh auth refresh`. If you're using GITHUB_TOKEN / GH_TOKEN, regenerate the " +
				"token and ensure it has the required scopes.",
		}
	case github.KindForbidden:
		return &internal.ErrorWithSuggestion{
			Message: "GitHub denied access to the requested resource (HTTP 403). The " +
				"repository may be private, your token may be missing required scopes, or " +
				"your account may not have permission.",
			Suggestion: "Verify you can access the repository in a browser while signed in " +
				"as the same GitHub account. If you're using a personal access token, ensure " +
				"it includes the 'repo' scope. Run `gh auth status` to confirm which account " +
				"gh is using.",
		}
	case github.KindServerError:
		return &internal.ErrorWithSuggestion{
			Message: "GitHub returned a server error (HTTP 5xx). This usually indicates a " +
				"transient issue on GitHub's side rather than a problem with your request.",
			Suggestion: "Wait a few minutes and try again. If the problem persists, check " +
				"https://www.githubstatus.com/ for ongoing incidents.",
			Links: []errorhandler.ErrorLink{
				{
					URL:   "https://www.githubstatus.com/",
					Title: "GitHub Status",
				},
			},
		}
	}
	return nil
}

func suggestionForRepoNotAccessible(_ *RepoNotAccessibleError) *internal.ErrorWithSuggestion {
	// Leave Message empty so the renderer falls back to RepoNotAccessibleError.Error(),
	// which is already user-friendly and includes the repo slug — avoids duplication.
	return &internal.ErrorWithSuggestion{
		Suggestion: "Confirm the repository URL is correct and that the active gh account " +
			"can see it. Run `gh auth status` to check which account is active. For Enterprise " +
			"Managed Users (EMU), make sure the active account is the EMU account that owns " +
			"this repository — a github.com URL may need to target a different host.",
	}
}
