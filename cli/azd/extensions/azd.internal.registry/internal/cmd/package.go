// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"dario.cat/mergo"
	"github.com/azure/azure-dev/cli/azd/extensions/azd.internal.registry/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func newPackageCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "package",
		Short: "Build, package and update the extension registry",
		RunE:  buildRegistry,
	}

	rootCmd.Flags().StringP("path", "p", ".", "Paths to the extension directory.")
	rootCmd.Flags().StringP("registry", "r", "registry.json", "Path to the registry.json file.")
	rootCmd.Flags().StringP("output", "o", "artifacts", "Path to the artifacts output directory.")
	rootCmd.Flags().StringP("base-url", "b", "", "Base URL for artifact paths")

	return rootCmd
}

func buildRegistry(cmd *cobra.Command, args []string) error {
	extensionPath, _ := cmd.Flags().GetString("path")
	registryPath, _ := cmd.Flags().GetString("registry")
	outputPath, _ := cmd.Flags().GetString("output")
	baseURL, _ := cmd.Flags().GetString("base-url")

	if baseURL == "" {
		return fmt.Errorf("base URL is required")
	}

	extensionYamlPath := filepath.Join(extensionPath, "extension.yaml")
	if _, err := os.Stat(extensionYamlPath); err != nil {
		return fmt.Errorf("extension.yaml file not found in the specified path: %w", err)
	}

	// Load or create the registry
	var registry extensions.Registry
	if _, err := os.Stat(registryPath); err == nil {
		data, err := os.ReadFile(registryPath)
		if err != nil {
			return fmt.Errorf("failed to read registry file: %w", err)
		}
		if err := json.Unmarshal(data, &registry); err != nil {
			return fmt.Errorf("failed to parse registry file: %w", err)
		}
	} else {
		registry = extensions.Registry{}
	}

	absExtensionPath, err := filepath.Abs(extensionPath)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}

	absArtifactsOutputPath, err := filepath.Abs(outputPath)
	if err != nil {
		return fmt.Errorf("failed to get absolute path for output directory: %w", err)
	}

	if err := processExtension(absExtensionPath, absArtifactsOutputPath, baseURL, &registry); err != nil {
		return fmt.Errorf("failed to process extension: %w", err)
	}

	// Save the updated registry without a signature
	if err := saveRegistry(registryPath, &registry); err != nil {
		return fmt.Errorf("failed to save registry: %w", err)
	}

	fmt.Println("Registry updated successfully.")
	return nil
}

func processExtension(extensionPath string, outputPath string, baseURL string, registry *extensions.Registry) error {
	// Load metadata
	metadataPath := filepath.Join(extensionPath, "extension.yaml")
	metadataData, err := os.ReadFile(metadataPath)
	if err != nil {
		return fmt.Errorf("failed to read metadata: %w", err)
	}

	var schema internal.ExtensionSchema
	if err := yaml.Unmarshal(metadataData, &schema); err != nil {
		return fmt.Errorf("failed to parse metadata: %w", err)
	}

	if schema.Id == "" {
		return fmt.Errorf("id is required in the metadata")
	}

	if schema.Version == "" {
		return fmt.Errorf("version is required in the metadata")
	}

	// Build the artifacts
	buildScript := filepath.Join(extensionPath, "build.sh")
	if _, err := os.Stat(buildScript); err == nil {
		// nolint:gosec // G204
		cmd := exec.Command("bash", "build.sh", schema.Version)
		cmd.Dir = extensionPath
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to build artifacts: %s", string(output))
		}
	}

	// Prepare artifacts for registry
	artifactsPath := filepath.Join(extensionPath, "bin")
	artifacts, err := os.ReadDir(artifactsPath)
	artifactMap := map[string]extensions.ExtensionArtifact{}
	if err == nil {
		targetPath := filepath.Join(outputPath, schema.Id, schema.Version)

		// Ensure target directory exists
		if err := os.MkdirAll(targetPath, osutil.PermissionDirectory); err != nil {
			return fmt.Errorf("failed to create target directory: %w", err)
		}

		// Map and copy artifacts
		for _, artifact := range artifacts {
			extensionYamlSourcePath := filepath.Join(extensionPath, "extension.yaml")
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
			url := fmt.Sprintf("%s/%s/%s/%s", baseURL, schema.Id, schema.Version, filepath.Base(targetFilePath))

			platformMetadata := map[string]any{
				"entryPoint": artifact.Name(),
			}

			operatingSystems := []string{"windows", "linux", "darwin"}
			architectures := []string{"amd64", "arm64"}

			for _, os := range operatingSystems {
				if err := mergo.Merge(&platformMetadata, schema.Platforms[os]); err != nil {
					return fmt.Errorf("failed to merge os metadata: %w", err)
				}
			}

			for _, arch := range architectures {
				if err := mergo.Merge(&platformMetadata, schema.Platforms[arch]); err != nil {
					return fmt.Errorf("failed to merge architecture metadata: %w", err)
				}
			}

			if err := mergo.Merge(&platformMetadata, schema.Platforms[osArch]); err != nil {
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
	addOrUpdateExtension(schema, schema.Version, artifactMap, registry)
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
	schema internal.ExtensionSchema,
	version string,
	artifacts map[string]extensions.ExtensionArtifact,
	registry *extensions.Registry,
) {
	// Find or create the extension in the registry
	var ext *extensions.ExtensionMetadata
	for i := range registry.Extensions {
		if registry.Extensions[i].Id == schema.Id {
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

	ext.Id = schema.Id
	ext.Namespace = schema.Namespace
	ext.DisplayName = schema.DisplayName
	ext.Description = schema.Description
	ext.Tags = schema.Tags

	// Check if the version already exists and update it if found
	for i, v := range ext.Versions {
		if v.Version == version {
			ext.Versions[i] = extensions.ExtensionVersion{
				Version:      version,
				EntryPoint:   schema.EntryPoint,
				Usage:        schema.Usage,
				Examples:     schema.Examples,
				Dependencies: schema.Dependencies,
				Artifacts:    artifacts,
			}
			fmt.Printf("Updated version %s for extension %s\n", version, schema.Id)
			return
		}
	}

	// If the version does not exist, add it as a new entry
	ext.Versions = append(ext.Versions, extensions.ExtensionVersion{
		Version:      version,
		EntryPoint:   schema.EntryPoint,
		Usage:        schema.Usage,
		Examples:     schema.Examples,
		Dependencies: schema.Dependencies,
		Artifacts:    artifacts,
	})
	fmt.Printf("Added new version %s for extension %s\n", version, schema.Id)
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
