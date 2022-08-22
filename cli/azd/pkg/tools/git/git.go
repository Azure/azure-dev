// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/executil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/blang/semver/v4"
)

type GitCli interface {
	tools.ExternalTool
	GetRemoteUrl(ctx context.Context, string, remoteName string) (string, error)
	FetchCode(ctx context.Context, repositoryPath string, branch string, target string) error
	InitRepo(ctx context.Context, repositoryPath string) error
	AddRemote(ctx context.Context, repositoryPath string, remoteName string, remoteUrl string) error
	GetCurrentBranch(ctx context.Context, repositoryPath string) (string, error)
	AddFile(ctx context.Context, repositoryPath string, filespec string) error
	Commit(ctx context.Context, repositoryPath string, message string) error
	PushUpstream(ctx context.Context, repositoryPath string, origin string, branch string) error
	IsUntrackedFile(ctx context.Context, repositoryPath string, filePath string) (bool, error)
}

type gitCli struct {
}

func NewGitCli() GitCli {
	return &gitCli{}
}

func (cli *gitCli) versionInfo() tools.VersionInfo {
	return tools.VersionInfo{
		// Support version from 09-Dec-2018 08:40
		// https://mirrors.edge.kernel.org/pub/software/scm/git/
		// 4 years should cover most Linux out of the box version
		MinimumVersion: semver.Version{
			Major: 2,
			Minor: 20,
			Patch: 0},
		UpdateCommand: "Visit https://git-scm.com/downloads to upgrade",
	}
}

func (cli *gitCli) CheckInstalled(ctx context.Context) (bool, error) {
	found, err := tools.ToolInPath("git")
	if !found {
		return false, err
	}
	gitRes, err := tools.ExecuteCommand(ctx, "git", "--version")
	if err != nil {
		return false, fmt.Errorf("checking %s version: %w", cli.Name(), err)
	}
	gitSemver, err := tools.ExtractSemver(gitRes)
	if err != nil {
		return false, fmt.Errorf("converting to semver version fails: %w", err)
	}
	updateDetail := cli.versionInfo()
	if gitSemver.LT(updateDetail.MinimumVersion) {
		return false, &tools.ErrSemver{ToolName: cli.Name(), VersionInfo: updateDetail}
	}
	return true, nil
}

func (cli *gitCli) InstallUrl() string {
	return "https://git-scm.com/downloads"
}

func (cli *gitCli) Name() string {
	return "git CLI"
}

func (cli *gitCli) FetchCode(ctx context.Context, repositoryPath string, branch string, target string) error {
	args := []string{"clone", "--depth", "1", repositoryPath}
	if branch != "" {
		args = append(args, "--branch", branch)
	}
	args = append(args, target)

	res, err := executil.RunCommand(ctx, "git", args...)
	if err != nil {
		return fmt.Errorf("failed to clone repository %s, %s: %w", repositoryPath, res.String(), err)
	}

	if err := os.RemoveAll(filepath.Join(target, ".git")); err != nil {
		return fmt.Errorf("removing .git folder after clone: %w", err)
	}

	return nil
}

var noSuchRemoteRegex = regexp.MustCompile("(fatal|error): No such remote")
var notGitRepositoryRegex = regexp.MustCompile("(fatal|error): not a git repository")
var ErrNoSuchRemote = errors.New("no such remote")
var ErrNotRepository = errors.New("not a git repository")
var gitUntrackedFileRegex = regexp.MustCompile("untracked files present|new file")

func (cli *gitCli) GetRemoteUrl(ctx context.Context, repositoryPath string, remoteName string) (string, error) {
	res, err := executil.RunCommand(ctx, "git", "-C", repositoryPath, "remote", "get-url", remoteName)
	if noSuchRemoteRegex.MatchString(res.Stderr) {
		return "", ErrNoSuchRemote
	} else if notGitRepositoryRegex.MatchString(res.Stderr) {
		return "", ErrNotRepository
	} else if err != nil {
		return "", fmt.Errorf("failed to get remote url: %s: %w", res.String(), err)
	}

	return strings.TrimSpace(res.Stdout), nil
}

func (cli *gitCli) GetCurrentBranch(ctx context.Context, repositoryPath string) (string, error) {
	res, err := executil.RunCommand(ctx, "git", "-C", repositoryPath, "branch", "--show-current")
	if notGitRepositoryRegex.MatchString(res.Stderr) {
		return "", ErrNotRepository
	} else if err != nil {
		return "", fmt.Errorf("failed to get current branch: %s: %w", res.String(), err)
	}

	return strings.TrimSpace(res.Stdout), nil
}

func (cli *gitCli) InitRepo(ctx context.Context, repositoryPath string) error {
	res, err := executil.RunCommand(ctx, "git", "-C", repositoryPath, "init")
	if err != nil {
		return fmt.Errorf("failed to init repository: %s: %w", res.String(), err)
	}

	return nil
}

func (cli *gitCli) AddRemote(ctx context.Context, repositoryPath string, remoteName string, remoteUrl string) error {
	res, err := executil.RunCommand(ctx, "git", "-C", repositoryPath, "remote", "add", remoteName, remoteUrl)
	if err != nil {
		return fmt.Errorf("failed to add remote: %s: %w", res.String(), err)
	}

	return nil
}

func (cli *gitCli) AddFile(ctx context.Context, repositoryPath string, filespec string) error {
	res, err := executil.RunCommand(ctx, "git", "-C", repositoryPath, "add", filespec)
	if err != nil {
		return fmt.Errorf("failed to add files: %s: %w", res.String(), err)
	}

	return nil
}

func (cli *gitCli) Commit(ctx context.Context, repositoryPath string, message string) error {
	res, err := executil.RunCommand(ctx, "git", "-C", repositoryPath, "commit", "--allow-empty", "-m", message)
	if err != nil {
		return fmt.Errorf("failed to commit: %s: %w", res.String(), err)
	}

	return nil
}

func (cli *gitCli) PushUpstream(ctx context.Context, repositoryPath string, origin string, branch string) error {
	res, err := executil.RunCommandWithCurrentStdio(ctx, "git", "-C", repositoryPath, "push", "--set-upstream", origin, branch)
	if err != nil {
		return fmt.Errorf("failed to push: %s: %w", res.String(), err)
	}

	return nil
}

func (cli *gitCli) IsUntrackedFile(ctx context.Context, repositoryPath string, filePath string) (bool, error) {
	res, err := executil.RunCommand(ctx, "git", "-C", repositoryPath, "status", filePath)
	if err != nil {
		return false, fmt.Errorf("failed to check status file: %s: %w", res.String(), err)
	}

	if gitUntrackedFileRegex.MatchString(res.Stdout) {
		return true, nil
	}

	return false, nil
}
