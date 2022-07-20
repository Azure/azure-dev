// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/executil"
)

type FetchCodeCli interface {
	ExternalTool
	FetchCode(ctx context.Context, repositoryPath string, branch string, target string) error
}

type fetchCodeCli struct {
}

func NewFetchCodeCli() FetchCodeCli {
	return &fetchCodeCli{}
}

func (cli *fetchCodeCli) CheckInstalled(ctx context.Context) (bool, error) {
	for _, powershellBinaryName := range []string{"wget", "unzip"} {
		found, err := toolInPath(powershellBinaryName)
		if !found {
			return false, err
		}
	}

	return true, nil
}

func (cli *fetchCodeCli) InstallUrl() string {
	return "how to install these tools for your Linux version, for example run: 'apt install wget unzip'"
}

func (cli *fetchCodeCli) Name() string {
	return "[wget, unzip]"
}

// FetchCode uses wget and unzip to download the template code from the repository path
func (cli *fetchCodeCli) FetchCode(ctx context.Context, repositoryPath string, branch string, target string) error {
	fetchUrl, err := parseRepoUrl(repositoryPath, branch)
	if err != nil {
		return err
	}

	zipFile := filepath.Join(target, fetchUrl.branch+".zip")
	res, err := executil.RunCommand(ctx, "wget", fetchUrl.DownloadZipUrl(), "-P", target)
	if err != nil {
		return fmt.Errorf("failed to fetch repository %s, %s: %w", repositoryPath, res.String(), err)
	}

	// unzip
	res, err = executil.RunCommand(ctx, "unzip", "-d", target, zipFile)
	if err != nil {
		return fmt.Errorf("failed to unzip repository %s, %s: %w", repositoryPath, res.String(), err)
	}
	// remove zip
	_, _ = executil.RunCommand(ctx, "rm", "-rf", zipFile)

	// move content one level up
	if err = moveFolderContentToParentFolder(ctx, target); err != nil {
		return err
	}

	if err := os.RemoveAll(filepath.Join(target, ".git")); err != nil {
		return fmt.Errorf("removing .git folder after clone: %w", err)
	}

	return nil
}
