// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.extensions/internal"
	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.extensions/internal/models"
	"github.com/azure/azure-dev/cli/azd/pkg/common"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/spf13/cobra"
)

type packageFlags struct {
	inputPath  string
	outputPath string
	rebuild    bool
}

func newPackCommand() *cobra.Command {
	flags := &packageFlags{}

	packageCmd := &cobra.Command{
		Use:   "pack",
		Short: "Build and pack extension artifacts",
		RunE: func(cmd *cobra.Command, args []string) error {
			internal.WriteCommandHeader(
				"Package azd extension (azd x pack)",
				"Packages the azd extension project and updates the registry",
			)

			defaultPackageFlags(flags)
			err := runPackageAction(cmd.Context(), flags)
			if err != nil {
				return err
			}

			internal.WriteCommandSuccess("Extension packaged successfully")
			return nil
		},
	}

	packageCmd.Flags().StringVarP(
		&flags.outputPath,
		"output", "o", "",
		"Path to the artifacts output directory. If not provided, will use local registry artifacts path.",
	)

	packageCmd.Flags().StringVarP(
		&flags.inputPath,
		"input", "i", "./bin",
		"Path to the input directory.",
	)

	packageCmd.Flags().BoolVar(
		&flags.rebuild,
		"rebuild", false,
		"Rebuild the extension before packaging.",
	)

	return packageCmd
}

func runPackageAction(ctx context.Context, flags *packageFlags) error {
	absExtensionPath, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get absolute path for extension directory: %w", err)
	}

	extensionMetadata, err := models.LoadExtension(absExtensionPath)
	if err != nil {
		return fmt.Errorf("failed to load extension metadata: %w", err)
	}

	if flags.outputPath == "" {
		localRegistryArtifactsPath, err := internal.LocalRegistryArtifactsPath()
		if err != nil {
			return err
		}

		flags.outputPath = filepath.Join(localRegistryArtifactsPath, extensionMetadata.Id, extensionMetadata.Version)
	}

	absInputPath := filepath.Join(extensionMetadata.Path, flags.inputPath)
	absOutputPath, err := filepath.Abs(flags.outputPath)
	if err != nil {
		return fmt.Errorf("failed to get absolute path for output directory: %w", err)
	}

	fmt.Println()
	fmt.Printf("%s: %s\n", output.WithBold("Input Path"), output.WithHyperlink(absInputPath, absInputPath))
	fmt.Printf("%s: %s\n", output.WithBold("Output Path"), output.WithHyperlink(absOutputPath, absOutputPath))

	taskList := ux.NewTaskList(nil).
		AddTask(ux.TaskOptions{
			Title: "Building extension",
			Action: func(spf ux.SetProgressFunc) (ux.TaskState, error) {
				// Verify if we have any existing binaries
				if !flags.rebuild {
					entires, err := os.ReadDir(absInputPath)
					if err == nil {
						binaries := []string{}

						for _, entry := range entires {
							if entry.IsDir() {
								continue
							}

							// Only process files that match the extension ID
							artifactName := entry.Name()
							if !strings.HasPrefix(artifactName, extensionMetadata.SafeDashId()) {
								continue
							}

							ext := filepath.Ext(artifactName)
							if ext != ".exe" && ext != "" {
								continue
							}

							binaries = append(binaries, entry.Name())
						}

						if len(binaries) > 0 {
							return ux.Skipped, nil
						}
					}
				}

				buildCmd := exec.Command("azd", "x", "build", "--all")
				buildCmd.Dir = extensionMetadata.Path

				resultBytes, err := buildCmd.CombinedOutput()
				if err != nil {
					return ux.Error, common.NewDetailedError(
						"Build failed",
						fmt.Errorf("failed to run command: %w, Command output: %s", err, string(resultBytes)),
					)
				}

				return ux.Success, nil
			},
		}).
		AddTask(ux.TaskOptions{
			Title: "Packaging extension",
			Action: func(spf ux.SetProgressFunc) (ux.TaskState, error) {
				if err := packExtensionBinaries(extensionMetadata, flags.outputPath); err != nil {
					return ux.Error, common.NewDetailedError(
						"Packaging failed",
						fmt.Errorf("failed to package extension: %w", err),
					)
				}

				return ux.Success, nil
			},
		})

	return taskList.Run()
}

func packExtensionBinaries(
	extensionMetadata *models.ExtensionSchema,
	outputPath string,
) error {
	// Prepare artifacts for registry
	buildPath := filepath.Join(extensionMetadata.Path, "bin")
	entries, err := os.ReadDir(buildPath)
	if err != nil {
		return fmt.Errorf("failed to read artifacts directory: %w", err)
	}

	extensionYamlSourcePath := filepath.Join(extensionMetadata.Path, "extension.yaml")

	// Ensure target directory exists
	if err := os.MkdirAll(outputPath, osutil.PermissionDirectory); err != nil {
		return fmt.Errorf("failed to create target directory: %w", err)
	}

	// Map and copy artifacts
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		// Only process files that match the extension ID
		artifactName := entry.Name()
		if !strings.HasPrefix(artifactName, extensionMetadata.SafeDashId()) {
			continue
		}

		ext := filepath.Ext(artifactName)
		if ext != ".exe" && ext != "" {
			continue
		}

		fileWithoutExt := getFileNameWithoutExt(artifactName)
		artifactSourcePath := filepath.Join(buildPath, entry.Name())
		zipFiles := []string{extensionYamlSourcePath, artifactSourcePath}

		// Determine if this is a Linux binary by checking if the filename contains "linux"
		isLinuxBinary := strings.Contains(artifactName, "linux")

		var targetFilePath string
		var archiveErr error

		if isLinuxBinary {
			// Create a tar.gz archive for Linux binaries
			tarGzFileName := fmt.Sprintf("%s.tar.gz", fileWithoutExt)
			targetFilePath = filepath.Join(outputPath, tarGzFileName)
			archiveErr = internal.TarGzSource(zipFiles, targetFilePath)
		} else {
			// Create a ZIP archive for non-Linux binaries (Windows, macOS)
			zipFileName := fmt.Sprintf("%s.zip", fileWithoutExt)
			targetFilePath = filepath.Join(outputPath, zipFileName)
			archiveErr = internal.ZipSource(zipFiles, targetFilePath)
		}

		if archiveErr != nil {
			return fmt.Errorf("failed to create archive for %s: %w", entry.Name(), archiveErr)
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

func defaultPackageFlags(flags *packageFlags) {
	if flags.inputPath == "" {
		flags.inputPath = "bin"
	}
}
