// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
package project

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

const (
	openEnvRepoUrl = "https://github.com/huggingface/OpenEnv.git"
	openEnvRepoRef = "main"
)

func createRleSessionDir(name string, dest string, force bool) (string, error) {
	sessionDir := filepath.Join(dest, name)
	if entries, err := os.ReadDir(sessionDir); err == nil && len(entries) > 0 && !force {
		return "", &azdext.LocalError{
			Message:    fmt.Sprintf("Directory %q already exists and is not empty.", sessionDir),
			Code:       "rle_session_exists",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Use --force to overwrite generated files, or choose a different environment name.",
		}
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if force {
		if err := os.RemoveAll(sessionDir); err != nil {
			return "", err
		}
	}
	if err := os.MkdirAll(sessionDir, 0750); err != nil {
		return "", err
	}
	return sessionDir, nil
}

func CheckoutOpenEnvEnvironment(name string, dest string, force bool) (string, error) {
	name, err := ValidateEnvironmentName(name)
	if err != nil {
		return "", err
	}
	sourcePath := openEnvEnvironmentPath(name)
	tempDir, err := os.MkdirTemp("", "azd-rle-open-env-*")
	if err != nil {
		return "", err
	}
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()

	if err := runGitCheckout(
		"clone",
		"--depth", "1",
		"--filter=blob:none",
		"--sparse",
		"--branch", openEnvRepoRef,
		openEnvRepoUrl,
		tempDir,
	); err != nil {
		return "", err
	}
	environmentNames, err := listOpenEnvEnvironments(tempDir)
	if err != nil {
		return "", err
	}
	if !containsString(environmentNames, name) {
		return "", openEnvEnvironmentNotFoundError(name, environmentNames)
	}
	if err := runGitCheckout("-C", tempDir, "sparse-checkout", "set", sourcePath); err != nil {
		return "", err
	}

	sourceDir := filepath.Join(tempDir, filepath.FromSlash(sourcePath))
	return copyOpenEnvEnvironment(sourceDir, name, dest, force)
}

func copyOpenEnvEnvironment(sourceDir string, name string, dest string, force bool) (string, error) {
	if _, err := os.Stat(sourceDir); os.IsNotExist(err) {
		return "", openEnvEnvironmentNotFoundError(name, nil)
	} else if err != nil {
		return "", err
	}
	sessionDir, err := createRleSessionDir(name, dest, force)
	if err != nil {
		return "", err
	}
	if err := copyDirectory(sourceDir, sessionDir); err != nil {
		return "", err
	}
	return sessionDir, nil
}

func openEnvEnvironmentPath(name string) string {
	return "envs/" + name
}

func listOpenEnvEnvironments(repoDir string) ([]string, error) {
	output, err := runGitCommand("-C", repoDir, "ls-tree", "--name-only", openEnvRepoRef+":envs")
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(output)), nil
}

func openEnvEnvironmentNotFoundError(name string, environmentNames []string) error {
	catalogURL := strings.TrimSuffix(openEnvRepoUrl, ".git")
	suggestion := fmt.Sprintf(
		"Choose an environment from %s/tree/%s/envs.",
		catalogURL,
		openEnvRepoRef,
	)
	if closest := closestEnvironmentName(name, environmentNames); closest != "" {
		suggestion = fmt.Sprintf("Did you mean %q? %s", closest, suggestion)
	}
	return &azdext.LocalError{
		Message:    fmt.Sprintf("OpenEnv environment %q was not found.", name),
		Code:       "rle_open_env_environment_not_found",
		Category:   azdext.LocalErrorCategoryUser,
		Suggestion: suggestion,
	}
}

func closestEnvironmentName(name string, environmentNames []string) string {
	closest := ""
	closestDistance := len(name) + 1
	for _, candidate := range environmentNames {
		distance := editDistance(name, candidate)
		if distance < closestDistance {
			closest = candidate
			closestDistance = distance
		}
	}
	maxDistance := max(2, len(name)/3)
	if closestDistance > maxDistance {
		return ""
	}
	return closest
}

func editDistance(left string, right string) int {
	previous := make([]int, len(right)+1)
	for index := range previous {
		previous[index] = index
	}
	for leftIndex, leftRune := range left {
		current := make([]int, len(right)+1)
		current[0] = leftIndex + 1
		for rightIndex, rightRune := range right {
			substitutionCost := 0
			if leftRune != rightRune {
				substitutionCost = 1
			}
			current[rightIndex+1] = min(
				current[rightIndex]+1,
				previous[rightIndex+1]+1,
				previous[rightIndex]+substitutionCost,
			)
		}
		previous = current
	}
	return previous[len(right)]
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func runGitCheckout(args ...string) error {
	_, err := runGitCommand(args...)
	return err
}

func runGitCommand(args ...string) ([]byte, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, &azdext.LocalError{
			Message:    "Could not find \"git\" on PATH.",
			Code:       "rle_git_not_found",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Install Git, then retry azd ai rle init.",
		}
	}
	// The user-provided sparse path is validated as an environment name before reaching this command.
	process := exec.Command("git", args...) //nolint:gosec
	process.Env = os.Environ()
	output, err := process.CombinedOutput()
	if err != nil {
		return nil, &azdext.LocalError{
			Message:    fmt.Sprintf("Failed to checkout OpenEnv environment: %v", err),
			Code:       "rle_open_env_checkout_failed",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: strings.TrimSpace(string(output)),
		}
	}
	return output, nil
}

func copyDirectory(sourceDir string, destDir string) error {
	sourceInfo, err := os.Stat(sourceDir)
	if err != nil {
		return err
	}
	if !sourceInfo.IsDir() {
		return &azdext.LocalError{
			Message:    fmt.Sprintf("RLE source path %q is not a directory.", sourceDir),
			Code:       "rle_source_path_not_directory",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Use a source directory when initializing an RLE environment.",
		}
	}
	return filepath.WalkDir(sourceDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		relativePath, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		if relativePath == "." {
			return nil
		}
		targetPath := filepath.Join(destDir, relativePath)
		if entry.IsDir() {
			return os.MkdirAll(targetPath, 0750)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path) //nolint:gosec // path comes from the checked-out sample directory walk.
		if err != nil {
			return err
		}
		// targetPath is derived from the trusted source sample walk.
		return os.WriteFile(targetPath, data, info.Mode().Perm()) //nolint:gosec
	})
}
