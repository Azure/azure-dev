// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	_ "embed"
	"os"
	"path/filepath"
)

const (
	rleManagedDir          = ".azd-rle"
	rleDepsDir             = "deps"
	bundledRleSdkWheelName = "rle_sdk-0.1.3-py3-none-any.whl"
	defaultLoomRecipeRepo  = "https://msdata.visualstudio.com/DefaultCollection/Vienna/_git/loom"
	defaultLoomRecipeRef   = "code_rl_with_rle"
)

//go:embed assets/rle_sdk-0.1.3-py3-none-any.whl
var bundledRleSdkWheel []byte

func materializeBundledRleSdk(sessionDir string) (string, error) {
	depsDir := filepath.Join(sessionDir, rleManagedDir, rleDepsDir)
	if err := os.MkdirAll(depsDir, 0700); err != nil {
		return "", err
	}

	targetPath := filepath.Join(depsDir, bundledRleSdkWheelName)
	existing, err := os.ReadFile(targetPath)
	if err == nil && bytes.Equal(existing, bundledRleSdkWheel) {
		return targetPath, nil
	}
	if err := os.WriteFile(targetPath, bundledRleSdkWheel, 0600); err != nil {
		return "", err
	}
	return targetPath, nil
}

func bundledRleSdkPath(sessionDir string) string {
	return filepath.Join(sessionDir, rleManagedDir, rleDepsDir, bundledRleSdkWheelName)
}
