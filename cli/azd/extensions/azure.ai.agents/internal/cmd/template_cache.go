// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
// cspell:ignore logissue

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

const templateCacheDirEnv = "AZURE_AI_AGENTS_E2E_TEMPLATE_CACHE_DIR"

func templateCacheDir() string {
	return strings.TrimSpace(os.Getenv(templateCacheDirEnv))
}

func readCachedTemplateManifest(_ string) ([]byte, bool) {
	cacheDir := templateCacheDir()
	if cacheDir == "" {
		return nil, false
	}
	//nolint:gosec // cacheDir is set by the live-test pipeline.
	content, err := os.ReadFile(filepath.Join(cacheDir, "azure.yaml"))
	return content, err == nil
}

func restoreCachedTemplate(_ string, staging string) (bool, error) {
	cacheDir := templateCacheDir()
	if cacheDir == "" || !fileExists(filepath.Join(cacheDir, "azure.yaml")) {
		return false, nil
	}
	if err := clearStagingDirectory(staging); err != nil {
		return false, err
	}
	if err := copyDirectory(cacheDir, staging); err != nil {
		return false, fmt.Errorf("restore cached sample: %w", err)
	}
	return true, nil
}

func refreshTemplateCache(_ string, staging string) error {
	cacheDir := templateCacheDir()
	if cacheDir == "" {
		return nil
	}

	tempDir := cacheDir + ".new"
	if err := os.RemoveAll(tempDir); err != nil {
		return err
	}
	if err := copyDirectory(staging, tempDir); err != nil {
		return fmt.Errorf("stage sample cache: %w", err)
	}
	if err := os.RemoveAll(cacheDir); err != nil {
		return fmt.Errorf("replace sample cache: %w", err)
	}
	if err := os.Rename(tempDir, cacheDir); err != nil {
		return fmt.Errorf("activate sample cache: %w", err)
	}
	return nil
}

func useCachedTemplateOnDownloadError(pointer, staging string, downloadErr error) error {
	restored, err := restoreCachedTemplate(pointer, staging)
	if err != nil {
		return fmt.Errorf("%w; cached sample fallback also failed: %v", downloadErr, err)
	}
	if !restored {
		return downloadErr
	}
	emitTemplateCacheWarning(
		fmt.Sprintf("GitHub sample download failed; using the cached sample. Details: %s", downloadErr),
	)
	return nil
}

func emitTemplateCacheWarning(message string) {
	if os.Getenv("TF_BUILD") != "" {
		message = strings.NewReplacer("%", "%AZP25", "\r", "%0D", "\n", "%0A", "]", "%5D").Replace(message)
		fmt.Printf("##vso[task.logissue type=warning]%s\n", message)
		return
	}
	fmt.Println(output.WithWarningFormat("WARNING: %s", message))
}
