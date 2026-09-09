// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
// cspell:ignore logissue

package cmd

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

const templateCacheDirEnv = "AZURE_AI_AGENTS_E2E_TEMPLATE_CACHE_DIR"
const templateCacheRefreshedMarker = ".refreshed"

func templateCacheRoot() string {
	return strings.TrimSpace(os.Getenv(templateCacheDirEnv))
}

func templateCacheDir(pointer string) string {
	root := templateCacheRoot()
	if root == "" {
		return ""
	}
	return filepath.Join(root, fmt.Sprintf("%x", sha256.Sum256([]byte(pointer))))
}

func readCachedTemplateManifest(pointer string) ([]byte, bool) {
	cacheDir := templateCacheDir(pointer)
	if cacheDir == "" {
		return nil, false
	}
	//nolint:gosec // cacheDir is set by the live-test pipeline.
	content, err := os.ReadFile(filepath.Join(cacheDir, "azure.yaml"))
	return content, err == nil
}

func restoreCachedTemplate(pointer, staging string) (bool, error) {
	cacheDir := templateCacheDir(pointer)
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

func refreshTemplateCache(pointer, staging string) error {
	cacheDir := templateCacheDir(pointer)
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
	if err := os.WriteFile(
		filepath.Join(templateCacheRoot(), templateCacheRefreshedMarker),
		[]byte("refreshed\n"),
		0600,
	); err != nil {
		return fmt.Errorf("mark refreshed sample cache: %w", err)
	}
	return nil
}

func useCachedTemplateOnDownloadError(pointer, staging string, downloadErr error) error {
	restored, err := restoreCachedTemplate(pointer, staging)
	if err != nil {
		var localErr *azdext.LocalError
		if errors.As(downloadErr, &localErr) {
			combinedErr := *localErr
			combinedErr.Message = fmt.Sprintf("%s; cached sample fallback also failed: %s", localErr.Message, err)
			return &combinedErr
		}
		return fmt.Errorf("%w; cached sample fallback also failed: %w", downloadErr, err)
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
