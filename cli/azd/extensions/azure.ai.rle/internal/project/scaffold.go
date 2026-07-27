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
	openEnvRepoUrl        = "https://github.com/huggingface/OpenEnv.git"
	openEnvRepoRef        = "main"
	openEnvEchoSamplePath = "envs/echo_env"
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

func CheckoutOpenEnvEchoSample(name string, dest string, force bool) (string, error) {
	name, err := ValidateEnvironmentName(name)
	if err != nil {
		return "", err
	}
	sessionDir, err := createRleSessionDir(name, dest, force)
	if err != nil {
		return "", err
	}
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
	if err := runGitCheckout("-C", tempDir, "sparse-checkout", "set", openEnvEchoSamplePath); err != nil {
		return "", err
	}

	sourceDir := filepath.Join(tempDir, filepath.FromSlash(openEnvEchoSamplePath))
	if err := copyDirectory(sourceDir, sessionDir); err != nil {
		return "", err
	}
	return sessionDir, nil
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
	process := exec.Command("git", args...) //nolint:gosec // args are fixed by init's OpenEnv sample checkout flow.
	process.Env = os.Environ()
	output, err := process.CombinedOutput()
	if err != nil {
		return &azdext.LocalError{
			Message:    fmt.Sprintf("Failed to checkout OpenEnv echo sample: %v", err),
			Code:       "rle_open_env_checkout_failed",
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
		return os.WriteFile(targetPath, data, info.Mode().Perm()) //nolint:gosec // target path is derived from walking the trusted source sample and preserves source file mode.
	})
}
