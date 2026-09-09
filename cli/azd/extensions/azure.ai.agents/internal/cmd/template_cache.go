// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"crypto/sha256"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

const templateCacheDirEnv = "AZURE_AI_AGENTS_E2E_TEMPLATE_CACHE_DIR"

func templateCachePath(pointer string) (string, bool) {
	root := strings.TrimSpace(os.Getenv(templateCacheDirEnv))
	if root == "" {
		return "", false
	}

	key := fmt.Sprintf("%x", sha256.Sum256([]byte(pointer)))
	return filepath.Join(root, key), true
}

func readCachedTemplateManifest(pointer string) ([]byte, bool) {
	cachePath, ok := templateCachePath(pointer)
	if !ok {
		return nil, false
	}

	//nolint:gosec // cachePath is rooted in a CI-controlled directory and keyed by a SHA-256 digest.
	content, err := os.ReadFile(filepath.Join(cachePath, "azure.yaml"))
	return content, err == nil
}

func restoreCachedTemplate(pointer, staging string) (bool, error) {
	cachePath, ok := templateCachePath(pointer)
	if !ok || !fileExists(filepath.Join(cachePath, "azure.yaml")) {
		return false, nil
	}

	if err := clearStagingDirectory(staging); err != nil {
		return false, fmt.Errorf("prepare staging directory for cached sample: %w", err)
	}
	if err := copyDirectory(cachePath, staging); err != nil {
		return false, fmt.Errorf("restore cached sample: %w", err)
	}
	return true, nil
}

func refreshTemplateCache(pointer, staging string) error {
	cachePath, ok := templateCachePath(pointer)
	if !ok {
		return nil
	}

	root := filepath.Dir(cachePath)
	if err := os.MkdirAll(root, osutil.PermissionDirectory); err != nil {
		return fmt.Errorf("create template cache directory: %w", err)
	}
	tempPath, err := os.MkdirTemp(root, ".refresh-*")
	if err != nil {
		return fmt.Errorf("create temporary template cache: %w", err)
	}
	defer func() { _ = os.RemoveAll(tempPath) }()

	if err := copyDirectory(staging, tempPath); err != nil {
		return fmt.Errorf("copy sample into template cache: %w", err)
	}
	if !fileExists(filepath.Join(tempPath, "azure.yaml")) {
		return fmt.Errorf("downloaded sample cache does not contain azure.yaml")
	}
	backupPath := cachePath + ".previous"
	if err := os.RemoveAll(backupPath); err != nil {
		return fmt.Errorf("prepare template cache backup: %w", err)
	}
	hadCache := fileExists(cachePath)
	if hadCache {
		if err := os.Rename(cachePath, backupPath); err != nil {
			return fmt.Errorf("back up template cache: %w", err)
		}
	}
	if err := os.Rename(tempPath, cachePath); err != nil {
		if hadCache {
			_ = os.Rename(backupPath, cachePath)
		}
		return fmt.Errorf("activate template cache: %w", err)
	}
	if err := os.RemoveAll(backupPath); err != nil {
		return fmt.Errorf("remove previous template cache: %w", err)
	}
	return nil
}

func useCachedTemplateOnDownloadError(pointer, staging string, downloadErr error) error {
	restored, cacheErr := restoreCachedTemplate(pointer, staging)
	if cacheErr != nil {
		return fmt.Errorf("%w; cached sample fallback also failed: %v", downloadErr, cacheErr)
	}
	if !restored {
		return downloadErr
	}

	emitTemplateCacheWarning(templateCacheFallbackMessage(pointer, downloadErr))
	return nil
}

func templateCacheFallbackMessage(pointer string, downloadErr error) string {
	detail := downloadErr.Error()
	if redacted := redactTemplatePointer(pointer); redacted != pointer {
		detail = strings.ReplaceAll(detail, pointer, redacted)
	}
	return fmt.Sprintf("GitHub sample download failed; using the last cached sample. Details: %s", detail)
}

func redactTemplatePointer(pointer string) string {
	parsed, err := url.Parse(pointer)
	if err != nil {
		return pointer
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	parsed.RawFragment = ""
	return parsed.String()
}

func emitTemplateCacheWarning(message string) {
	if os.Getenv("TF_BUILD") != "" {
		fmt.Printf("##vso[task.logissue type=warning]%s\n", escapeAzurePipelinesMessage(message))
		return
	}
	fmt.Println(output.WithWarningFormat("WARNING: %s", message))
}

func escapeAzurePipelinesMessage(message string) string {
	replacer := strings.NewReplacer(
		"%", "%AZP25",
		"\r", "%0D",
		"\n", "%0A",
		"]", "%5D",
	)
	return replacer.Replace(message)
}
