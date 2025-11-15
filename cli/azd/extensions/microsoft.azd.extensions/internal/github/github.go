// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package github

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/azure/azure-dev/extensions/microsoft.azd.extensions/internal"
	"github.com/azure/azure-dev/pkg/common"
)

var (
	ErrReleaseAlreadyExists = fmt.Errorf("release already exists")
	ErrReleaseNotFound      = fmt.Errorf("release not found")
)

// GitHubCli provides access to GitHub CLI functionality
type GitHubCli struct {
	// ExecutablePath is the path to the GitHub CLI executable
	ExecutablePath string

	// DefaultRepo is the default repository to use if none is specified
	DefaultRepo string
}

// Repository represents a GitHub repository
type Repository struct {
	Name string `json:"nameWithOwner"`
	Url  string `json:"url"`
}

// Release represents a GitHub release
type Release struct {
	Name    string          `json:"name"`
	TagName string          `json:"tagName"`
	Url     string          `json:"url"`
	Assets  []*ReleaseAsset `json:"assets"`
}

// ReleaseAsset represents an asset attached to a GitHub release
type ReleaseAsset struct {
	Id          string `json:"id"`
	ContentType string `json:"contentType"`
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	State       string `json:"state"`
	Url         string `json:"url"`
	Path        string `json:"path"` // Local path when downloaded
}

// NewGitHubCli creates a new GitHub CLI wrapper
func NewGitHubCli() (*GitHubCli, error) {
	// Try to find GitHub CLI in PATH
	ghPath, err := exec.LookPath("gh")
	if err != nil {
		return &GitHubCli{
			ExecutablePath: "gh", // Default to "gh" and let validation handle errors later
		}, nil
	}

	return &GitHubCli{
		ExecutablePath: ghPath,
	}, nil
}

// IsInstalled checks if the GitHub CLI is installed and available
func (gh *GitHubCli) IsInstalled() (bool, error) {
	/* #nosec G204 - Subprocess launched with a potential tainted input or cmd arguments */
	cmd := exec.Command(gh.ExecutablePath, "--version")
	err := cmd.Run()
	if err != nil {
		return false, nil
	}
	return true, nil
}

// CheckAndGetInstallError checks if GitHub CLI is installed and returns a UserFriendlyError if it's not
func (gh *GitHubCli) CheckAndGetInstallError() error {
	installed, err := gh.IsInstalled()
	if err != nil || !installed {
		// Create a user-friendly error with installation instructions in the user details
		return internal.NewUserFriendlyError(
			"GitHub CLI is required for this operation",
			gh.getInstallInstructions(),
		)
	}
	return nil
}

// IsAuthenticated checks if the user is authenticated with GitHub
func (gh *GitHubCli) IsAuthenticated() (bool, error) {
	/* #nosec G204 - Subprocess launched with a potential tainted input or cmd arguments */
	cmd := exec.Command(gh.ExecutablePath, "auth", "status")
	err := cmd.Run()
	if err != nil {
		return false, nil
	}
	return true, nil
}

// ViewRepository gets information about a GitHub repository
func (gh *GitHubCli) ViewRepository(cwd string, repo string) (*Repository, error) {
	args := []string{"repo", "view"}
	if repo != "" {
		args = append(args, repo)
	}

	args = append(args, "--json", "nameWithOwner,url")
	/* #nosec G204 - Subprocess launched with a potential tainted input or cmd arguments */
	cmd := exec.Command(gh.ExecutablePath, args...)
	cmd.Dir = cwd

	resultBytes, err := cmd.CombinedOutput()
	if err != nil {
		return nil, common.NewDetailedError(
			"Failed to get GitHub repository",
			fmt.Errorf("failed to run command: %w, Command output: %s", err, string(resultBytes)),
		)
	}

	var repoResult *Repository
	if err := json.Unmarshal(resultBytes, &repoResult); err != nil {
		return nil, fmt.Errorf("failed to deserialize command output: %w, Command output: %s", err, string(resultBytes))
	}

	return repoResult, nil
}

// ViewRelease gets information about a GitHub release
func (gh *GitHubCli) ViewRelease(cwd string, repo string, tagName string) (*Release, error) {
	args := []string{"release", "view", tagName}
	if repo != "" {
		args = append(args, "--repo", repo)
	}

	args = append(args, "--json", "name,tagName,url,assets")

	/* #nosec G204 - Subprocess launched with a potential tainted input or cmd arguments */
	cmd := exec.Command(gh.ExecutablePath, args...)
	cmd.Dir = cwd

	resultBytes, err := cmd.CombinedOutput()
	if err != nil {
		errorMessage := string(resultBytes)
		if strings.Contains(errorMessage, "release not found") {
			return nil, fmt.Errorf("%s, %w", errorMessage, ErrReleaseNotFound)
		}

		return nil, fmt.Errorf("failed to run command: %w, Command output: %s", err, errorMessage)
	}

	var releaseResult *Release
	if err := json.Unmarshal(resultBytes, &releaseResult); err != nil {
		return nil, fmt.Errorf("failed to deserialize command output: %w, Command output: %s", err, string(resultBytes))
	}

	return releaseResult, nil
}

// CreateRelease creates a new GitHub release
func (gh *GitHubCli) CreateRelease(cwd string, tagName string, opts map[string]string, assets []string) (*Release, error) {
	args := []string{"release", "create", tagName}

	// Add optional arguments
	for key, value := range opts {
		if value != "" {
			args = append(args, fmt.Sprintf("--%s", key), value)
		}
	}

	// Add boolean flags
	for _, flag := range []string{"prerelease", "draft"} {
		if value, ok := opts[flag]; ok && value == "true" {
			args = append(args, fmt.Sprintf("--%s", flag))
		}
	}

	// Add assets
	args = append(args, assets...)

	// First create the release
	/* #nosec G204 - Subprocess launched with a potential tainted input or cmd arguments */
	cmd := exec.Command(gh.ExecutablePath, args...)
	cmd.Dir = cwd

	resultBytes, err := cmd.CombinedOutput()
	if err != nil {
		errorMessage := string(resultBytes)
		if strings.Contains(errorMessage, "a release with the same tag name already exists") {
			return nil, fmt.Errorf("%s, %w", errorMessage, ErrReleaseAlreadyExists)
		}

		return nil, fmt.Errorf("failed to run command: %w, Command output: %s", err, errorMessage)
	}

	// Then fetch the created release details to return a full Release object
	return gh.ViewRelease(cwd, opts["repo"], tagName)
}

// GetInstallInstructions returns OS-specific instructions for installing GitHub CLI (legacy method)
func (gh *GitHubCli) GetInstallInstructions() string {
	return gh.getInstallInstructions()
}

// getInstallInstructions returns OS-specific instructions for installing GitHub CLI (internal method)
func (gh *GitHubCli) getInstallInstructions() string {
	var installCommand string
	var additionalInstructions string

	switch runtime.GOOS {
	case "windows":
		installCommand = "winget install -e --id GitHub.cli"
		//nolint:lll
		additionalInstructions = "Alternatively, you can download the installer from: https://github.com/cli/cli/releases/latest"
	case "darwin":
		installCommand = "brew install gh"
		additionalInstructions = `If you don't have Homebrew installed, install it with:
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"`
	case "linux":
		// For Linux, instructions vary by distribution
		installCommand = `# Debian/Ubuntu:
sudo apt install gh`

		additionalInstructions = "For other distributions, see: https://github.com/cli/cli/blob/trunk/docs/install_linux.md"
	default:
		installCommand = "See https://cli.github.com/manual/installation for installation instructions"
	}

	return fmt.Sprintf(`GitHub CLI (gh) is required for this operation but was not found.

Installation instructions for %s:
%s

%s

After installing, authenticate using:
gh auth login

For more information, visit: https://cli.github.com/
`, runtime.GOOS, installCommand, additionalInstructions)
}
