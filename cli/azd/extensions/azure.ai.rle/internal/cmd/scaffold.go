// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

var envNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func validateEnvName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !envNamePattern.MatchString(name) {
		return "", fmt.Errorf("invalid environment name %q", name)
	}
	return name, nil
}

func slug(name string) string {
	var builder strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			builder.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && builder.Len() > 0 {
			builder.WriteRune('-')
			lastDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}

const (
	openEnvRepoUrl        = "https://github.com/huggingface/OpenEnv.git"
	openEnvRepoRef        = "main"
	openEnvEchoSamplePath = "envs/echo_env"
)

var checkoutOpenEnvEchoSampleFunc = checkoutOpenEnvEchoSample

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
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		return "", err
	}
	return sessionDir, nil
}

func checkoutOpenEnvEchoSample(name string, dest string, force bool) (string, error) {
	sessionDir, err := createRleSessionDir(name, dest, force)
	if err != nil {
		return "", err
	}
	tempDir, err := os.MkdirTemp("", "azd-rle-openenv-*")
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
	if err := runGitCheckout("-C", tempDir, "sparse-checkout", "set", openEnvEchoSamplePath); err != nil {
		return "", err
	}

	sourceDir := filepath.Join(tempDir, filepath.FromSlash(openEnvEchoSamplePath))
	if err := copyDirectory(sourceDir, sessionDir); err != nil {
		return "", err
	}
	return sessionDir, nil
}

func isSafeRelativePath(path string) bool {
	return path != ".." &&
		!filepath.IsAbs(path) &&
		!strings.HasPrefix(path, ".."+string(os.PathSeparator))
}

func runGitCheckout(args ...string) error {
	if _, err := exec.LookPath("git"); err != nil {
		return &azdext.LocalError{
			Message:    "Could not find \"git\" on PATH.",
			Code:       "rle_git_not_found",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Install Git, then retry azd ai rle init.",
		}
	}
	process := exec.Command("git", args...)
	process.Env = os.Environ()
	output, err := process.CombinedOutput()
	if err != nil {
		return &azdext.LocalError{
			Message:    fmt.Sprintf("Failed to checkout OpenEnv echo sample: %v", err),
			Code:       "rle_openenv_checkout_failed",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: strings.TrimSpace(string(output)),
		}
	}
	return nil
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
			return os.MkdirAll(targetPath, 0755)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path) //nolint:gosec // path comes from the checked-out sample directory walk.
		if err != nil {
			return err
		}
		return os.WriteFile(targetPath, data, info.Mode().Perm())
	})
}
