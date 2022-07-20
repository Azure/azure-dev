// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

func (cli *fetchCodeCli) FetchCode(ctx context.Context, repositoryPath string, branch string, target string) error {
	defaultBranch := "main"
	if branch != "" {
		defaultBranch = branch
	}
	fetchUrl, err := parseRepoUrl(repositoryPath)
	if err != nil {
		return err
	}
	fetchUrlPath := fmt.Sprintf(
		"https://%s/%s/archive/%s.zip",
		fetchUrl.host,
		fetchUrl.slug,
		defaultBranch,
	)
	zipFile := filepath.Join(target, defaultBranch+".zip")
	res, err := executil.RunCommand(ctx, "wget", fetchUrlPath, "-P", target)
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
	folderNameResult, _ := executil.RunCommand(ctx, "ls", target)
	folderName := strings.Replace(folderNameResult.Stdout, "\n", "", -1)
	tmpFolder := target + "tmp"
	folderPath := filepath.Join(tmpFolder, folderName)
	if err = os.Rename(target, tmpFolder); err != nil {
		return fmt.Errorf("failed renaming folder: %w", err)
	}
	if err = os.Rename(folderPath, target); err != nil {
		return fmt.Errorf("failed renaming folder: %w", err)
	}

	if err := os.RemoveAll(filepath.Join(target, ".git")); err != nil {
		return fmt.Errorf("removing .git folder after clone: %w", err)
	}

	return nil
}
