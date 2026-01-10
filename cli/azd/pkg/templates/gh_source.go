// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package templates

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/github"
)

// GitHubUrlInfo contains parsed information from a GitHub URL.
type GitHubUrlInfo struct {
	// Hostname is the GitHub hostname (e.g., "github.com", "github.enterprise.com")
	Hostname string
	// RepoSlug is the repository in the format "owner/repo"
	RepoSlug string
	// Branch is the branch name, which may contain slashes
	Branch string
	// FilePath is the path to the file within the repository
	FilePath string
}

// ParseGitHubUrl parses various GitHub URL formats and extracts repository information.
// It supports the following URL formats:
//   - https://raw.<hostname>/<owner>/<repo>/<branch>/[...path]/<file>
//   - https://<hostname>/<owner>/<repo>/blob/<branch>/[...path]/<file>
//   - https://<hostname>/<owner>/<repo>/tree/<branch>/[...path]/<file>
//   - https://api.<hostname>/repos/<owner>/<repo>/contents/[...path]/<file>[?ref=<branch>]
//
// Note: Branch names may contain slashes (e.g., "feature/new-feature").
// For blob/tree/raw URLs with ambiguous branch/path separation, this function queries the GitHub API
// to deterministically find the longest valid branch name that exists in the repository.
func ParseGitHubUrl(ctx context.Context, urlArg string, ghCli *github.Cli) (*GitHubUrlInfo, error) {
	parsedURL, err := url.Parse(urlArg)
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL: %w", err)
	}

	hostname := parsedURL.Hostname()
	var repoSlug, filePath, branch string

	if strings.HasPrefix(hostname, "raw.") {
		// https://raw.<hostname>/<owner>/<repo>/<branch>/[path....]/<file>
		pathParts := strings.Split(parsedURL.Path, "/")
		if len(pathParts) < 5 {
			return nil, fmt.Errorf("invalid URL format using 'raw.'. Expected the form of " +
				"'https://raw.<hostname>/<owner>/<repo>/<branch>/[...path]/<fileName>'")
		}
		repoSlug = fmt.Sprintf("%s/%s", pathParts[1], pathParts[2])
		branchAndPath := strings.Join(pathParts[3:], "/")

		// Normalize raw.githubusercontent.com to github.com
		if hostname == "raw.githubusercontent.com" {
			hostname = "github.com"
		}

		// Ensure gh is authenticated before trying to resolve the branch
		if err := ensureGitHubAuthenticated(ctx, ghCli, hostname); err != nil {
			return nil, err
		}

		// Resolve the actual branch by checking with GitHub API
		branch, filePath, err = resolveBranchAndPath(ctx, ghCli, hostname, repoSlug, branchAndPath)
		if err != nil {
			return nil, err
		}
	} else if strings.HasPrefix(hostname, "api.") {
		// https://api.<hostname>/repos/<owner>/<repo>/contents/[...path]/<file>[?ref=<branch>]
		pathParts := strings.Split(parsedURL.Path, "/")
		if len(pathParts) < 6 {
			return nil, fmt.Errorf("invalid URL format using 'api.'. Expected the form of " +
				"'https://api.<hostname>/repos/<owner>/<repo>/contents/[...path]/<fileName>[?ref=<branch>]'")
		}
		repoSlug = fmt.Sprintf("%s/%s", pathParts[2], pathParts[3])
		filePath = strings.Join(pathParts[5:], "/")
		// For API URLs, branch comes from the 'ref' query parameter (already known, no need to resolve)
		branch = parsedURL.Query().Get("ref")

		// Normalize api.github.com to github.com
		if hostname == "api.github.com" {
			hostname = "github.com"
		}
	} else if strings.HasPrefix(urlArg, "https://") {
		// https://<hostname>/<owner>/<repo>/blob/<branch>/[...path]/<file>
		// https://<hostname>/<owner>/<repo>/tree/<branch>/[...path]/<file>
		pathParts := strings.Split(parsedURL.Path, "/")
		if len(pathParts) < 5 {
			return nil, fmt.Errorf("invalid URL format. Expected the form of " +
				"'https://<hostname>/<owner>/<repo>/blob/<branch>/[...path]/<fileName>'")
		}
		repoSlug = fmt.Sprintf("%s/%s", pathParts[1], pathParts[2])

		var branchAndPath string
		// Check if this is a blob or tree URL
		if len(pathParts) >= 4 && (pathParts[3] == "blob" || pathParts[3] == "tree") {
			// For blob/tree URLs: /<owner>/<repo>/blob/<branch-and-path...>
			branchAndPath = strings.Join(pathParts[4:], "/")
		} else {
			// Legacy format without blob/tree: /<owner>/<repo>/<branch>/[...path]/<file>
			branchAndPath = strings.Join(pathParts[3:], "/")
		}

		// Ensure gh is authenticated before trying to resolve the branch
		if err := ensureGitHubAuthenticated(ctx, ghCli, hostname); err != nil {
			return nil, err
		}

		// Resolve the actual branch by checking with GitHub API
		branch, filePath, err = resolveBranchAndPath(ctx, ghCli, hostname, repoSlug, branchAndPath)
		if err != nil {
			return nil, err
		}
	} else {
		return nil, fmt.Errorf(
			"invalid URL format. Expected formats are:\n" +
				"  - 'https://raw.<hostname>/<owner>/<repo>/<branch>/[...path]/<fileName>'\n" +
				"  - 'https://<hostname>/<owner>/<repo>/blob/<branch>/[...path]/<fileName>'\n" +
				"  - 'https://api.<hostname>/repos/<owner>/<repo>/contents/[...path]/<fileName>[?ref=<branch>]'",
		)
	}

	return &GitHubUrlInfo{
		Hostname: hostname,
		RepoSlug: repoSlug,
		Branch:   branch,
		FilePath: filePath,
	}, nil
}

// resolveBranchAndPath determines the actual branch name by querying the GitHub API.
// It tries progressively longer branch names (handling slashes) until it finds a valid branch.
// For example, for "uno/dos/tres/file.txt", it tries: "uno", "uno/dos", "uno/dos/tres"
// and returns the first valid branch found along with the remaining path.
//
// Note: Git does not allow both "foo/bar" and "foo/bar/longer" to exist as branches simultaneously
// because branch refs are stored as files. Therefore, we can stop at the first valid branch found.
func resolveBranchAndPath(
	ctx context.Context,
	ghCli *github.Cli,
	hostname string,
	repoSlug string,
	branchAndPath string,
) (branch string, filePath string, err error) {
	if branchAndPath == "" {
		return "", "", fmt.Errorf("branch and path cannot be empty")
	}

	parts := strings.Split(branchAndPath, "/")
	if len(parts) == 1 {
		// Only one segment - try it as a branch first
		if branchExists(ctx, ghCli, hostname, repoSlug, parts[0]) {
			return parts[0], "", nil
		}
		// If not a branch, assume it's a file in the default branch
		return "", parts[0], nil
	}

	// Try progressively longer branch names by combining more segments
	// Stop at the first valid branch since Git cannot have both "foo/bar" and "foo/bar/longer"
	for i := 1; i <= len(parts); i++ {
		candidateBranch := strings.Join(parts[:i], "/")
		candidatePath := strings.Join(parts[i:], "/")

		if branchExists(ctx, ghCli, hostname, repoSlug, candidateBranch) {
			return candidateBranch, candidatePath, nil
		}
	}

	// If no valid branch found, return error
	return "", "", fmt.Errorf("could not find a valid branch in the URL path. "+
		"Tried branch names from '%s' to '%s'", parts[0], strings.Join(parts, "/"))
}

// branchExists checks if a branch exists in the repository using the GitHub API.
func branchExists(ctx context.Context, ghCli *github.Cli, hostname string, repoSlug string, branchName string) bool {
	apiPath := fmt.Sprintf("/repos/%s/branches/%s", repoSlug, url.PathEscape(branchName))
	_, err := ghCli.ApiCall(ctx, hostname, apiPath, github.ApiCallOptions{})
	return err == nil
}

// ensureGitHubAuthenticated checks if the user is authenticated to GitHub and initiates login if not.
// This ensures that subsequent GitHub API calls will not fail due to authentication issues.
func ensureGitHubAuthenticated(ctx context.Context, ghCli *github.Cli, hostname string) error {
	authResult, err := ghCli.GetAuthStatus(ctx, hostname)
	if err != nil {
		return fmt.Errorf("failed to get auth status: %w", err)
	}
	if !authResult.LoggedIn {
		err := ghCli.Login(ctx, hostname)
		if err != nil {
			return fmt.Errorf("failed to login: %w", err)
		}
	}
	return nil
}

// newGhTemplateSource creates a new template source from a Github repository.
func newGhTemplateSource(
	ctx context.Context, name string, urlArg string, ghCli *github.Cli, console input.Console) (Source, error) {
	// Parse the GitHub URL to extract repository information
	urlInfo, err := ParseGitHubUrl(ctx, urlArg, ghCli)
	if err != nil {
		return nil, err
	}

	authResult, err := ghCli.GetAuthStatus(ctx, urlInfo.Hostname)
	if err != nil {
		return nil, fmt.Errorf("failed to get auth status: %w", err)
	}
	if !authResult.LoggedIn {
		// ensure no spinner is shown when logging in, as this is interactive operation
		console.StopSpinner(ctx, "", input.Step)
		err := ghCli.Login(ctx, urlInfo.Hostname)
		if err != nil {
			return nil, fmt.Errorf("failed to login: %w", err)
		}
		console.ShowSpinner(ctx, "Validating template source", input.Step)
	}

	// Fetch the file content from GitHub
	apiPath := fmt.Sprintf("/repos/%s/contents/%s", urlInfo.RepoSlug, urlInfo.FilePath)
	if urlInfo.Branch != "" {
		apiPath += "?ref=" + url.QueryEscape(urlInfo.Branch)
	}

	content, err := ghCli.ApiCall(ctx, urlInfo.Hostname, apiPath, github.ApiCallOptions{
		Headers: []string{"Accept: application/vnd.github.v3.raw"},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get content from GitHub API: %w", err)
	}

	return newJsonTemplateSource(name, content)
}
