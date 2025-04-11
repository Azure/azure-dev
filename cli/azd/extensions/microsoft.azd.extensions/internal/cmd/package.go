// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"dario.cat/mergo"
	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.extensions/internal"
	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.extensions/internal/models"
	"github.com/azure/azure-dev/cli/azd/pkg/common"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func newPackageCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "package",
		Short: "Build, package and update the extension registry",
		RunE:  buildRegistry,
	}

	rootCmd.Flags().StringP("path", "p", ".", "Paths to the extension directory. Defaults to the current directory.")
	rootCmd.Flags().StringP("registry", "r", "", "Path to the registry.json file. If not provided, will use a local registry.")
	rootCmd.Flags().StringP("output", "o", "", "Path to the artifacts output directory. If not provided, will use local registry")
	rootCmd.Flags().StringP("base-path", "b", "", "Base path for artifact paths. If not provided, will use local relative paths.")

	return rootCmd
}

func buildRegistry(cmd *cobra.Command, args []string) error {
	extensionPath, _ := cmd.Flags().GetString("path")
	registryPath, _ := cmd.Flags().GetString("registry")
	outputPath, _ := cmd.Flags().GetString("output")
	basePath, _ := cmd.Flags().GetString("base-path")

	azdConfigDir := os.Getenv("AZD_CONFIG_DIR")
	if azdConfigDir == "" {
		userHomeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get user home directory: %w", err)
		}
		azdConfigDir = filepath.Join(userHomeDir, ".azd")
	}

	if registryPath == "" {
		registryPath = filepath.Join(azdConfigDir, "registry.json")
	}

	if outputPath == "" {
		outputPath = filepath.Join(azdConfigDir, "registry")

		if _, err := os.Stat(outputPath); os.IsNotExist(err) {
			if err := os.MkdirAll(outputPath, internal.PermissionDirectory); err != nil {
				return fmt.Errorf("failed to create output directory: %w", err)
			}
		}
	}

	if basePath == "" {
		basePath = "registry"
	}

	internal.WriteCommandHeader(
		"Package azd extension (azd x package)",
		"Packages the azd extension project and updates the registry",
	)

	extensionMetadata, err := models.LoadExtension(extensionPath)
	if err != nil {
		return fmt.Errorf("failed to load extension metadata: %w", err)
	}

	var registry extensions.Registry

	taskList := ux.NewTaskList(nil)
	taskList.AddTask(ux.TaskOptions{
		Title: "Find extension registry",
		Action: func(spf ux.SetProgressFunc) (ux.TaskState, error) {
			// Load or create the registry
			if _, err := os.Stat(registryPath); err == nil {
				data, err := os.ReadFile(registryPath)
				if err != nil {
					return ux.Error, common.NewDetailedError(
						"Cannot read registry",
						fmt.Errorf("failed to read registry file: %w", err),
					)
				}
				if err := json.Unmarshal(data, &registry); err != nil {
					return ux.Error, common.NewDetailedError(
						"Invalid registry file",
						fmt.Errorf("failed to parse registry file: %w", err),
					)
				}
			} else {
				registry = extensions.Registry{}
			}

			return ux.Success, nil
		},
	})

	absArtifactsOutputPath, err := filepath.Abs(outputPath)
	if err != nil {
		return fmt.Errorf("failed to get absolute path for output directory: %w", err)
	}

	// Load metadata
	metadataPath := filepath.Join(extensionPath, "extension.yaml")
	metadataBytes, err := os.ReadFile(metadataPath)
	if err != nil {
		return fmt.Errorf("failed to read metadata: %w", err)
	}

	var schema models.ExtensionSchema
	if err := yaml.Unmarshal(metadataBytes, &schema); err != nil {
		return fmt.Errorf("failed to parse metadata: %w", err)
	}

	taskList.AddTask(ux.TaskOptions{
		Title: fmt.Sprintf("Packaging extension %s (%s)", extensionMetadata.Id, extensionMetadata.Version),
		Action: func(spf ux.SetProgressFunc) (ux.TaskState, error) {
			if err := processExtension(extensionMetadata, absArtifactsOutputPath, basePath, &registry); err != nil {
				return ux.Error, common.NewDetailedError(
					"Packaging failed",
					fmt.Errorf("failed to package extension: %w", err),
				)
			}

			return ux.Success, nil
		},
	})

	taskList.AddTask(ux.TaskOptions{
		Title: "Updating extension registry",
		Action: func(spf ux.SetProgressFunc) (ux.TaskState, error) {
			// Save the updated registry without a signature
			if err := saveRegistry(registryPath, &registry); err != nil {
				return ux.Error, common.NewDetailedError(
					"Registry update failed",
					fmt.Errorf("failed to save registry: %w", err),
				)
			}

			return ux.Success, nil
		},
	})

	if err := taskList.Run(); err != nil {
		return fmt.Errorf("failed to package tasks: %w", err)
	}

	internal.WriteCommandSuccess("Extension packaged successfully")
	return nil
}

func processExtension(extensionMetadata *models.ExtensionSchema, outputPath string, baseURL string, registry *extensions.Registry) error {
	// Prepare artifacts for registry
	artifactsPath := filepath.Join(extensionMetadata.Path, "bin")
	artifacts, err := os.ReadDir(artifactsPath)
	artifactMap := map[string]extensions.ExtensionArtifact{}
	if err == nil {
		targetPath := filepath.Join(outputPath, extensionMetadata.Id, extensionMetadata.Version)

		// Ensure target directory exists
		if err := os.MkdirAll(targetPath, osutil.PermissionDirectory); err != nil {
			return fmt.Errorf("failed to create target directory: %w", err)
		}

		// Map and copy artifacts
		for _, artifact := range artifacts {
			extensionYamlSourcePath := filepath.Join(extensionMetadata.Path, "extension.yaml")
			artifactSourcePath := filepath.Join(artifactsPath, artifact.Name())

			fileWithoutExt := getFileNameWithoutExt(artifact.Name())
			zipFileName := fmt.Sprintf("%s.zip", fileWithoutExt)
			targetFilePath := filepath.Join(targetPath, zipFileName)

			// Create a ZIP archive for the artifact
			zipFiles := []string{extensionYamlSourcePath, artifactSourcePath}

			if err := zipSource(zipFiles, targetFilePath); err != nil {
				return fmt.Errorf("failed to create archive for %s: %w", artifact.Name(), err)
			}

			// Generate checksum
			checksum, err := internal.ComputeChecksum(targetFilePath)
			if err != nil {
				return fmt.Errorf("failed to compute checksum for %s: %w", targetFilePath, err)
			}

			// Parse artifact filename to infer OS/ARCH
			osArch, err := inferOSArch(artifact.Name())
			if err != nil {
				return fmt.Errorf("failed to infer OS/ARCH for artifact %s: %w", artifact.Name(), err)
			}

			// Generate URL for the artifact using the base URL
			url := fmt.Sprintf("%s/%s/%s/%s", baseURL, extensionMetadata.Id, extensionMetadata.Version, filepath.Base(targetFilePath))

			platformMetadata := map[string]any{
				"entryPoint": artifact.Name(),
			}

			operatingSystems := []string{"windows", "linux", "darwin"}
			architectures := []string{"amd64", "arm64"}

			for _, os := range operatingSystems {
				if err := mergo.Merge(&platformMetadata, extensionMetadata.Platforms[os]); err != nil {
					return fmt.Errorf("failed to merge os metadata: %w", err)
				}
			}

			for _, arch := range architectures {
				if err := mergo.Merge(&platformMetadata, extensionMetadata.Platforms[arch]); err != nil {
					return fmt.Errorf("failed to merge architecture metadata: %w", err)
				}
			}

			if err := mergo.Merge(&platformMetadata, extensionMetadata.Platforms[osArch]); err != nil {
				return fmt.Errorf("failed to merge os/arch metadata: %w", err)
			}

			// Add artifact to the map with OS/ARCH key
			artifactMap[osArch] = extensions.ExtensionArtifact{
				URL: url,
				Checksum: struct {
					Algorithm string `json:"algorithm"`
					Value     string `json:"value"`
				}{
					Algorithm: "sha256",
					Value:     checksum,
				},
				AdditionalMetadata: platformMetadata,
			}
		}
	}

	// Add or update the extension in the registry
	addOrUpdateExtension(extensionMetadata, artifactMap, registry)
	return nil
}

func inferOSArch(filename string) (string, error) {
	// Example filename: azd-ext-ai-windows-amd64.exe
	parts := strings.Split(filename, "-")
	if len(parts) < 3 {
		return "", fmt.Errorf("invalid artifact filename format: %s", filename)
	}

	// Extract OS and ARCH from the filename
	osPart := parts[len(parts)-2]                                   // Second-to-last part is the OS
	archPart := parts[len(parts)-1]                                 // Last part is the ARCH (with optional extension)
	archPart = strings.TrimSuffix(archPart, filepath.Ext(archPart)) // Remove extension

	return fmt.Sprintf("%s/%s", osPart, archPart), nil
}

func addOrUpdateExtension(
	extensionMetadata *models.ExtensionSchema,
	artifacts map[string]extensions.ExtensionArtifact,
	registry *extensions.Registry,
) {
	// Find or create the extension in the registry
	var ext *extensions.ExtensionMetadata
	for i := range registry.Extensions {
		if registry.Extensions[i].Id == extensionMetadata.Id {
			ext = registry.Extensions[i]
			break
		}
	}

	// If the extension doesn't exist, add it
	if ext == nil {
		ext = &extensions.ExtensionMetadata{
			Versions: []extensions.ExtensionVersion{},
		}

		registry.Extensions = append(registry.Extensions, ext)
	}

	ext.Id = extensionMetadata.Id
	ext.Namespace = extensionMetadata.Namespace
	ext.DisplayName = extensionMetadata.DisplayName
	ext.Description = extensionMetadata.Description
	ext.Tags = extensionMetadata.Tags

	// Check if the version already exists and update it if found
	for i, v := range ext.Versions {
		if v.Version == extensionMetadata.Version {
			ext.Versions[i] = extensions.ExtensionVersion{
				Version:      extensionMetadata.Version,
				Capabilities: extensionMetadata.Capabilities,
				EntryPoint:   extensionMetadata.EntryPoint,
				Usage:        extensionMetadata.Usage,
				Examples:     extensionMetadata.Examples,
				Dependencies: extensionMetadata.Dependencies,
				Artifacts:    artifacts,
			}

			return
		}
	}

	// If the version does not exist, add it as a new entry
	ext.Versions = append(ext.Versions, extensions.ExtensionVersion{
		Version:      extensionMetadata.Version,
		Capabilities: extensionMetadata.Capabilities,
		EntryPoint:   extensionMetadata.EntryPoint,
		Usage:        extensionMetadata.Usage,
		Examples:     extensionMetadata.Examples,
		Dependencies: extensionMetadata.Dependencies,
		Artifacts:    artifacts,
	})
}

func saveRegistry(path string, registry *extensions.Registry) error {
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, osutil.PermissionFile)
}

func zipSource(files []string, target string) error {
	outputFile, err := os.Create(target)
	if err != nil {
		return err
	}

	defer outputFile.Close()

	zipWriter := zip.NewWriter(outputFile)
	defer zipWriter.Close()

	for _, file := range files {
		fileInfo, err := os.Stat(file)
		if err != nil {
			return err
		}

		header := &zip.FileHeader{
			Name:     filepath.Base(file),
			Modified: fileInfo.ModTime(),
			Method:   zip.Deflate,
		}

		headerWriter, err := zipWriter.CreateHeader(header)
		if err != nil {
			return err
		}

		file, err := os.Open(file)
		if err != nil {
			return err
		}

		_, err = io.Copy(headerWriter, file)
		if err != nil {
			return err
		}

	}

	return nil
}

// getFileNameWithoutExt extracts the filename without its extension
func getFileNameWithoutExt(filePath string) string {
	// Get the base filename
	fileName := filepath.Base(filePath)

	// Remove the extension
	return strings.TrimSuffix(fileName, filepath.Ext(fileName))
}
