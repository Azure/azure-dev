// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.extensions/internal"
	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.extensions/internal/models"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

// bundleArtifactsDir is the directory within a self-contained bundle that holds
// the per-platform artifact archives. Registry artifact URLs reference files
// relative to this directory.
const bundleArtifactsDir = "artifacts"

// resolveBundleOutputPath determines the destination .zip path for a
// self-contained bundle. When outputPath is empty the bundle is written to the
// current working directory using a derived file name. When outputPath is a
// directory the derived file name is placed within it; when it ends in .zip it
// is used verbatim.
func resolveBundleOutputPath(outputPath string, extensionMetadata *models.ExtensionSchema) (string, error) {
	defaultName := fmt.Sprintf("%s_%s.zip", extensionMetadata.SafeDashId(), extensionMetadata.Version)

	if outputPath == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("failed to get working directory: %w", err)
		}
		return filepath.Join(cwd, defaultName), nil
	}

	absOutputPath, err := filepath.Abs(outputPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve output path: %w", err)
	}

	if strings.EqualFold(filepath.Ext(absOutputPath), ".zip") {
		return absOutputPath, nil
	}

	return filepath.Join(absOutputPath, defaultName), nil
}

// packSelfContainedBundle builds a portable bundle zip at bundleOutputPath. The
// bundle contains a registry.json at its root (with artifact URLs relative to
// the bundle) alongside the per-platform artifact archives under artifacts/.
// Extension packs (which have no artifacts) produce a registry-only bundle.
func packSelfContainedBundle(extensionMetadata *models.ExtensionSchema, bundleOutputPath string) error {
	stagingDir, err := os.MkdirTemp("", "azd-ext-bundle-")
	if err != nil {
		return fmt.Errorf("failed to create staging directory: %w", err)
	}
	defer os.RemoveAll(stagingDir)

	artifactMap := map[string]extensions.ExtensionArtifact{}

	if !isExtensionPack(extensionMetadata) {
		artifactsDir := filepath.Join(stagingDir, bundleArtifactsDir)
		if err := packExtensionBinaries(extensionMetadata, artifactsDir); err != nil {
			return fmt.Errorf("failed to package extension binaries: %w", err)
		}

		entries, err := os.ReadDir(artifactsDir)
		if err != nil {
			return fmt.Errorf("failed to read packaged artifacts: %w", err)
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}

			artifactName := entry.Name()
			osArch, err := internal.InferOSArch(artifactName)
			if err != nil {
				return fmt.Errorf("failed to infer os/arch from %q: %w", artifactName, err)
			}

			checksum, err := internal.ComputeChecksum(filepath.Join(artifactsDir, artifactName))
			if err != nil {
				return fmt.Errorf("failed to compute checksum for %q: %w", artifactName, err)
			}

			artifactMetadata, err := createPlatformMetadata(extensionMetadata, osArch, artifactName)
			if err != nil {
				return fmt.Errorf("failed to create platform metadata for %q: %w", artifactName, err)
			}

			artifactMap[osArch] = extensions.ExtensionArtifact{
				// Forward slashes keep the URL portable across platforms.
				URL: bundleArtifactsDir + "/" + artifactName,
				Checksum: extensions.ExtensionChecksum{
					Algorithm: "sha256",
					Value:     checksum,
				},
				AdditionalMetadata: artifactMetadata,
			}
		}

		if len(artifactMap) == 0 {
			return fmt.Errorf("no artifacts were produced for the extension")
		}
	}

	registry := &extensions.Registry{
		SchemaVersion: extensions.CurrentRegistrySchemaVersion,
	}
	addOrUpdateExtension(registry, extensionMetadata, artifactMap)

	registryPath := filepath.Join(stagingDir, extensions.BundleRegistryFileName)
	if err := saveRegistry(registryPath, registry); err != nil {
		return fmt.Errorf("failed to write bundle registry: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(bundleOutputPath), osutil.PermissionDirectory); err != nil {
		return fmt.Errorf("failed to create bundle output directory: %w", err)
	}

	if err := zipDirectory(stagingDir, bundleOutputPath); err != nil {
		return fmt.Errorf("failed to create bundle archive: %w", err)
	}

	return nil
}

// zipDirectory writes all files under sourceDir into a zip archive at target,
// preserving the relative directory structure using forward-slash paths. It
// writes to a temporary file and renames it onto target only after a fully
// successful close, so a failed (re)pack never leaves a corrupt archive or
// clobbers a previously good bundle.
func zipDirectory(sourceDir string, target string) (err error) {
	tempFile, err := os.CreateTemp(filepath.Dir(target), ".bundle-*.zip.tmp")
	if err != nil {
		return err
	}
	tempPath := tempFile.Name()

	// Remove the temp file unless it is successfully committed (renamed) below.
	committed := false
	defer func() {
		if !committed {
			_ = tempFile.Close()
			_ = os.Remove(tempPath)
		}
	}()

	zipWriter := zip.NewWriter(tempFile)

	walkErr := filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		relPath, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}

		header := &zip.FileHeader{
			Name:     filepath.ToSlash(relPath),
			Modified: info.ModTime(),
			Method:   zip.Deflate,
		}

		headerWriter, err := zipWriter.CreateHeader(header)
		if err != nil {
			return err
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()

		_, err = io.Copy(headerWriter, file)
		return err
	})
	if walkErr != nil {
		return walkErr
	}

	// Close (flushing the central directory) and check both errors before the
	// rename, so a failed flush is never reported as success.
	if err := zipWriter.Close(); err != nil {
		return err
	}
	if err := tempFile.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tempPath, osutil.PermissionFile); err != nil {
		return err
	}
	if err := os.Rename(tempPath, target); err != nil {
		return err
	}

	committed = true
	return nil
}
