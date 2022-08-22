// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/executil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/blang/semver/v4"
)

type GitHubCli interface {
	tools.ExternalTool
	CheckAuth(ctx context.Context, hostname string) (bool, error)
	SetSecret(ctx context.Context, repo string, name string, value string) error
	Login(ctx context.Context, hostname string) error
	ListRepositories(ctx context.Context) ([]GhCliRepository, error)
	ViewRepository(ctx context.Context, name string) (GhCliRepository, error)
	CreatePrivateRepository(ctx context.Context, name string) error
	GetGitProtocolType(ctx context.Context) (string, error)
	GitHubActionsExists(ctx context.Context, repoSlug string) (bool, error)
}

func NewGitHubCli() GitHubCli {
	return &ghCli{}
}

var (
	ErrGitHubCliNotLoggedIn = errors.New("gh cli is not logged in")
	ErrRepositoryNameInUse  = errors.New("repository name already in use")
	// The hostname of the public GitHub service.
	GitHubHostName = "github.com"
)

type ghCli struct{}

func (cli *ghCli) versionInfo() tools.VersionInfo {
	return tools.VersionInfo{
		MinimumVersion: semver.Version{
			Major: 2,
			Minor: 4,
			Patch: 0},
		UpdateCommand: "Visit https://github.com/cli/cli/releases to upgrade",
	}
}

func (cli *ghCli) CheckInstalled(ctx context.Context) (bool, error) {
	found, err := tools.ToolInPath("gh")
	if !found {
		return false, err
	}
	ghRes, err := tools.ExecuteCommand(ctx, "gh", "--version")
	if err != nil {
		return false, fmt.Errorf("checking %s version: %w", cli.Name(), err)
	}
	ghSemver, err := tools.ExtractSemver(ghRes)
	if err != nil {
		return false, fmt.Errorf("converting to semver version fails: %w", err)
	}
	updateDetail := cli.versionInfo()
	if ghSemver.LT(updateDetail.MinimumVersion) {
		return false, &tools.ErrSemver{ToolName: cli.Name(), VersionInfo: updateDetail}
	}

	return true, nil
}

func (cli *ghCli) Name() string {
	return "GitHub CLI"
}

func (cli *ghCli) InstallUrl() string {
	return "https://aka.ms/azure-dev/github-cli-install"
}

func (cli *ghCli) CheckAuth(ctx context.Context, hostname string) (bool, error) {
	res, err := executil.RunCommand(ctx, "gh", "auth", "status", "--hostname", hostname)
	if res.ExitCode == 0 {
		return true, nil
	} else if isGhCliNotLoggedInMessageRegex.MatchString(res.Stderr) {
		return false, nil
	} else if notLoggedIntoAnyGitHubHostsMessageRegex.MatchString(res.Stderr) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("failed running gh auth status %s: %w", res.String(), err)
	}

	return false, errors.New("could not determine auth status")
}

func (cli *ghCli) Login(ctx context.Context, hostname string) error {
	res, err := executil.RunCommandWithCurrentStdio(ctx, "gh", "auth", "login", "--hostname", hostname)
	if err != nil {
		return fmt.Errorf("failed running gh auth login %s: %w", res.String(), err)
	}

	return nil
}

func (cli *ghCli) SetSecret(ctx context.Context, repoSlug string, name string, value string) error {
	res, err := executil.RunCommand(ctx, "gh", "-R", repoSlug, "secret", "set", name, "--body", value)
	if isGhCliNotLoggedInMessageRegex.MatchString(res.Stderr) {
		return ErrGitHubCliNotLoggedIn
	} else if err != nil {
		return fmt.Errorf("failed running gh secret set %s: %w", res.String(), err)
	}
	return nil
}

type GhCliRepository struct {
	// The slug for a repository (formatted as "<owner>/<name>")
	NameWithOwner string
	// The Url for the HTTPS endpoint for the repository
	HttpsUrl string `json:"url"`
	// The Url for the SSH endpoint for the repository
	SshUrl string
}

func (cli *ghCli) ListRepositories(ctx context.Context) ([]GhCliRepository, error) {
	res, err := executil.RunCommand(ctx, "gh", "repo", "list", "--no-archived", "--json", "nameWithOwner,url,sshUrl")
	if isGhCliNotLoggedInMessageRegex.MatchString(res.Stderr) {
		return nil, ErrGitHubCliNotLoggedIn
	} else if err != nil {
		return nil, fmt.Errorf("failed running gh repo list %s: %w", res.String(), err)
	}

	var repos []GhCliRepository

	if err := json.Unmarshal([]byte(res.Stdout), &repos); err != nil {
		return nil, fmt.Errorf("could not unmarshal output %s as a []GhCliRepository: %w", res.Stdout, err)
	}

	return repos, nil
}

func (cli *ghCli) ViewRepository(ctx context.Context, name string) (GhCliRepository, error) {
	res, err := executil.RunCommand(ctx, "gh", "repo", "view", name, "--json", "nameWithOwner,url,sshUrl")
	if isGhCliNotLoggedInMessageRegex.MatchString(res.Stderr) {
		return GhCliRepository{}, ErrGitHubCliNotLoggedIn
	} else if err != nil {
		return GhCliRepository{}, fmt.Errorf("failed running gh repo list %s: %w", res.String(), err)
	}

	var repo GhCliRepository

	if err := json.Unmarshal([]byte(res.Stdout), &repo); err != nil {
		return GhCliRepository{}, fmt.Errorf("could not unmarshal output %s as a GhCliRepository: %w", res.Stdout, err)
	}

	return repo, nil
}

func (cli *ghCli) CreatePrivateRepository(ctx context.Context, name string) error {
	res, err := executil.RunCommand(ctx, "gh", "repo", "create", name, "--private")
	if isGhCliNotLoggedInMessageRegex.MatchString(res.Stderr) {
		return ErrGitHubCliNotLoggedIn
	} else if repositoryNameInUseRegex.MatchString(res.Stderr) {
		return ErrRepositoryNameInUse
	} else if err != nil {
		return fmt.Errorf("failed running gh repo create %s: %w", res.String(), err)
	}

	return nil
}

const (
	GitSshProtocolType   = "ssh"
	GitHttpsProtocolType = "https"
)

func (cli *ghCli) GetGitProtocolType(ctx context.Context) (string, error) {
	res, err := executil.RunCommand(ctx, "gh", "config", "get", "git_protocol")
	if isGhCliNotLoggedInMessageRegex.MatchString(res.Stderr) {
		return "", ErrGitHubCliNotLoggedIn
	} else if err != nil {
		return "", fmt.Errorf("failed running gh config get git_protocol %s: %w", res.String(), err)
	}

	return strings.TrimSpace(res.Stdout), nil
}

type GitHubActionsResponse struct {
	TotalCount int `json:"total_count"`
}

// GitHubActionsExists gets the information from upstream about the workflows and
// return true if there is at least one workflow in the repo.
func (cli *ghCli) GitHubActionsExists(ctx context.Context, repoSlug string) (bool, error) {
	res, err := executil.RunCommand(ctx, "gh", "api", "/repos/"+repoSlug+"/actions/workflows")
	if err != nil {
		return false, fmt.Errorf("getting github actions %s: %w", res.String(), err)
	}
	var jsonResponse GitHubActionsResponse
	if err := json.Unmarshal([]byte(res.Stdout), &jsonResponse); err != nil {
		return false, fmt.Errorf("could not unmarshal output %s as a GhActionsResponse: %w", res.Stdout, err)
	}
	if jsonResponse.TotalCount == 0 {
		return false, nil
	}
	return true, nil
}

var isGhCliNotLoggedInMessageRegex = regexp.MustCompile("(To authenticate, please run `gh auth login`\\.)|(Try authenticating with:  gh auth login)|(To re-authenticate, run: gh auth login)")
var repositoryNameInUseRegex = regexp.MustCompile("GraphQL: Name already exists on this account (createRepository)")
var notLoggedIntoAnyGitHubHostsMessageRegex = regexp.MustCompile("You are not logged into any GitHub hosts. Run gh auth login to authenticate.")
