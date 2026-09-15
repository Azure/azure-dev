// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
// cspell:ignore logissue

package cmd

import (
	"context"
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
const templateCacheWarningsFileEnv = "AZURE_AI_AGENTS_E2E_TEMPLATE_CACHE_WARNINGS_FILE"
const templateCacheRefreshedMarker = ".refreshed"

var renameTemplateCachePath = os.Rename

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

	previousDir := cacheDir + ".previous"
	if err := os.RemoveAll(previousDir); err != nil {
		return fmt.Errorf("clear previous sample cache: %w", err)
	}
	hadPrevious := fileExists(filepath.Join(cacheDir, "azure.yaml"))
	if hadPrevious {
		if err := renameTemplateCachePath(cacheDir, previousDir); err != nil {
			return fmt.Errorf("preserve previous sample cache: %w", err)
		}
	}
	if err := renameTemplateCachePath(tempDir, cacheDir); err != nil {
		if hadPrevious {
			if restoreErr := renameTemplateCachePath(previousDir, cacheDir); restoreErr != nil {
				return fmt.Errorf(
					"activate sample cache and restore previous cache: %w",
					errors.Join(err, restoreErr),
				)
			}
		}
		return fmt.Errorf("activate sample cache: %w", err)
	}
	if err := os.RemoveAll(previousDir); err != nil {
		return fmt.Errorf("remove previous sample cache: %w", err)
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
	if errors.Is(downloadErr, context.Canceled) || errors.Is(downloadErr, context.DeadlineExceeded) {
		return downloadErr
	}
	restored, err := restoreCachedTemplate(pointer, staging)
	if err != nil {
		if localErr, ok := errors.AsType[*azdext.LocalError](downloadErr); ok {
			combinedErr := *localErr
			combinedErr.Message = fmt.Sprintf("%s; cached sample fallback also failed: %s", localErr.Message, err)
			return &combinedErr
		}
		return fmt.Errorf("%w; cached sample fallback also failed: %w", downloadErr, err)
	}
	if !restored {
		return downloadErr
	}
	message := fmt.Sprintf("GitHub sample download failed; using the cached sample. Details: %s", downloadErr)
	emitTemplateCacheWarning(message)
	if path := os.Getenv(templateCacheWarningsFileEnv); path != "" && os.Getenv("TF_BUILD") != "" {
		if err := appendTemplateCacheWarning(path, templateCacheWarningCommand(message)); err != nil {
			fmt.Println(output.WithWarningFormat("Unable to persist sample cache fallback warning: %s", err))
		}
	}
	return nil
}

func emitTemplateCacheWarning(message string) {
	if os.Getenv("TF_BUILD") != "" {
		fmt.Print(templateCacheWarningCommand(message))
		return
	}

	fmt.Println(output.WithWarningFormat("WARNING: %s", message))
}

func templateCacheWarningCommand(message string) string {
	message = strings.NewReplacer("%", "%AZP25", "\r", "%0D", "\n", "%0A", "]", "%5D").Replace(message)
	return fmt.Sprintf("##vso[task.logissue type=warning]%s\n", message)
}

func appendTemplateCacheWarning(path, line string) error {
	//nolint:gosec // The live-test pipeline supplies this per-job diagnostic path.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString(line)
	return errors.Join(writeErr, file.Close())
}
