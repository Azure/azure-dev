// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var FetchCodeNotFoundError = errors.New("Repository was not found.")

type gitRepo struct {
	host   string
	slug   string
	branch string
}

func (repoInfo *gitRepo) DownloadZipUrl() string {
	return fmt.Sprintf(
		"https://%s/%s/archive/%s.zip",
		repoInfo.host,
		repoInfo.slug,
		repoInfo.branch,
	)
}

func parseAsGit(url string) (gitRepo, error) {
	hostAndSlug := strings.Split(strings.Split(url, "@")[1], ":")

	return gitRepo{
		host: hostAndSlug[0],
		slug: strings.Split(hostAndSlug[1], ".git")[0],
	}, nil
}

func parseAsHttp(url string) (gitRepo, error) {
	hostAndSlug := strings.Split(strings.Split(url, "://")[1], "/")

	return gitRepo{
		host: hostAndSlug[0],
		slug: strings.Split(strings.Join(hostAndSlug[1:], "/"), ".git")[0],
	}, nil
}

func parseRepoUrl(url string, branch string) (gitRepo, error) {
	var result gitRepo
	var err error

	if strings.HasPrefix(url, "git") {
		result, err = parseAsGit(url)
	} else if strings.HasPrefix(url, "http") {
		result, err = parseAsHttp(url)
	}

	if err != nil {
		return result, fmt.Errorf("parsing repo url: %w", err)
	}
	result.branch = resolveBranchName(branch)
	return result, nil
}

// moveFolderContentToParentFolder gets the name of a folder inside the
// parentFolder and moves its content to the parent folder.
func moveFolderContentToParentFolder(ctx context.Context, parentFolder string) error {
	parentDirectory, err := os.Open(parentFolder)
	if err != nil {
		return fmt.Errorf("failed renaming folder: %w", err)
	}
	parentDirectoryFiles, err := parentDirectory.ReadDir(0)
	if err != nil {
		return fmt.Errorf("failed renaming folder: %w", err)
	}

	var folders []string
	for _, file := range parentDirectoryFiles {
		if file.IsDir() {
			folders = append(folders, file.Name())
		}
	}

	for _, folderName := range folders {
		tmpFolder := parentFolder + "tmp"
		folderPath := filepath.Join(tmpFolder, folderName)

		if err := os.Rename(parentFolder, tmpFolder); err != nil {
			return fmt.Errorf("failed renaming folder: %w", err)
		}
		if err := os.Rename(folderPath, parentFolder); err != nil {
			return fmt.Errorf("failed renaming folder: %w", err)
		}
	}
	return nil
}

func resolveBranchName(branch string) string {
	defaultBranch := "main"
	if branch != "" {
		defaultBranch = branch
	}
	return defaultBranch
}
