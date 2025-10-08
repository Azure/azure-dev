// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.extensions/internal"
	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.extensions/internal/models"
	"github.com/azure/azure-dev/cli/azd/pkg/common"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/spf13/cobra"
)

type buildFlags struct {
	outputPath   string
	allPlatforms bool
	skipInstall  bool
}

func newBuildCommand() *cobra.Command {
	flags := &buildFlags{}

	buildCmd := &cobra.Command{
		Use:   "build",
		Short: "Build the azd extension project",
		RunE: func(cmd *cobra.Command, args []string) error {
			internal.WriteCommandHeader(
				"Build and azd extension (azd x build)",
				"Builds the azd extension project for one or more platforms",
			)

			defaultBuildFlags(flags)
			err := runBuildAction(cmd.Context(), flags)
			if err != nil {
				return err
			}

			internal.WriteCommandSuccess("Build completed successfully!")
			return nil
		},
	}

	buildCmd.Flags().StringVarP(
		&flags.outputPath,
		"output", "o", "./bin",
		"Path to the output directory. Defaults to ./bin folder.",
	)
	buildCmd.Flags().BoolVar(
		&flags.allPlatforms, "all", false,
		"When set builds for all os/platforms. Defaults to the current os/platform only.",
	)
	buildCmd.Flags().BoolVar(
		&flags.skipInstall,
		"skip-install", false,
		"When set skips reinstalling extension after successful build.",
	)

	return buildCmd
}

func runBuildAction(ctx context.Context, flags *buildFlags) error {
	absOutputPath, err := filepath.Abs(flags.outputPath)
	if err != nil {
		return fmt.Errorf("failed to get absolute path for output directory: %w", err)
	}

	absExtensionPath, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get absolute path for extension directory: %w", err)
	}

	// Load metadata
	schema, err := models.LoadExtension(absExtensionPath)
	if err != nil {
		return fmt.Errorf("failed to load extension metadata: %w", err)
	}

	fmt.Println()
	fmt.Printf("%s: %s\n", output.WithBold("Output Path"), output.WithHyperlink(absOutputPath, absOutputPath))

	taskList := ux.NewTaskList(nil).
		AddTask(ux.TaskOptions{
			Title: "Validating extension metadata",
			Action: func(progress ux.SetProgressFunc) (ux.TaskState, error) {
				progress("Checking required fields...")

				var errors []string
				var warnings []string

				// Check required fields per schema - these are errors
				if schema.Id == "" {
					errors = append(errors, "Missing required field: id")
				}
				if schema.Version == "" {
					errors = append(errors, "Missing required field: version")
				}
				if len(schema.Capabilities) == 0 {
					errors = append(errors, "Missing required field: capabilities")
				}
				if schema.DisplayName == "" {
					errors = append(errors, "Missing required field: displayName")
				}
				if schema.Description == "" {
					errors = append(errors, "Missing required field: description")
				}

				progress("Validating capability-specific requirements...")

				// Capability-specific validations - these are warnings
				hasCustomCommands := slices.Contains(schema.Capabilities, extensions.CustomCommandCapability)
				hasServiceTarget := slices.Contains(schema.Capabilities, extensions.ServiceTargetProviderCapability)

				// Only validate namespace if custom-commands capability is defined
				if hasCustomCommands && schema.Namespace == "" {
					warnings = append(warnings, "Missing namespace - recommended when using custom-commands capability")
				}

				// Only validate providers if service-target-provider capability is defined
				if hasServiceTarget && len(schema.Providers) == 0 {
					warnings = append(warnings, "Missing providers - recommended when using custom providers capability")
				}

				// Check for missing optional but generally recommended fields
				if schema.Usage == "" {
					warnings = append(warnings, "Missing usage information")
				}

				progress("Validation complete")

				// If we have errors, this is a failure
				if len(errors) > 0 {
					// Create aggregated error
					aggregatedError := fmt.Errorf(
						"Extension contains validation failures: %s",
						strings.Join(errors, "; "),
					)
					return ux.Error, common.NewDetailedError("Validation failed", aggregatedError)
				}

				// If we have warnings, return warning state but no error
				if len(warnings) > 0 {
					aggregatedWarning := fmt.Errorf(
						"Extension contains validation warnings: %s",
						strings.Join(warnings, "\n - "),
					)
					return ux.Warning, common.NewDetailedError("Validation warnings", aggregatedWarning)
				}

				return ux.Success, nil
			},
		}).
		AddTask(ux.TaskOptions{
			Title: "Building extension artifacts",
			Action: func(progress ux.SetProgressFunc) (ux.TaskState, error) {
				// Create output directory if it doesn't exist
				if _, err := os.Stat(absOutputPath); os.IsNotExist(err) {
					if err := os.MkdirAll(absOutputPath, os.ModePerm); err != nil {
						return ux.Error, common.NewDetailedError("Failed to create output directory", err)
					}
				}

				var command string
				var scriptFile string
				if runtime.GOOS == "windows" {
					command = "pwsh"
					scriptFile = "build.ps1"
				} else {
					command = "bash"
					scriptFile = "build.sh"
				}

				// Build the binaries
				buildScript := filepath.Join(absExtensionPath, scriptFile)
				if _, err := os.Stat(buildScript); err == nil {
					/* #nosec G204 - Subprocess launched with variable */
					cmd := exec.Command(command, scriptFile)
					cmd.Dir = absExtensionPath

					envVars := map[string]string{
						"OUTPUT_DIR":         absOutputPath,
						"EXTENSION_DIR":      absExtensionPath,
						"EXTENSION_ID":       schema.Id,
						"EXTENSION_VERSION":  schema.Version,
						"EXTENSION_LANGUAGE": schema.Language,
					}

					// By default builds for current os/arch
					if !flags.allPlatforms {
						envVars["EXTENSION_PLATFORM"] = fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)
					}

					cmd.Env = os.Environ()

					for key, value := range envVars {
						cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", key, value))
					}

					if result, err := cmd.CombinedOutput(); err != nil {
						flags.skipInstall = true

						return ux.Error, common.NewDetailedError(
							"Build Failed",
							fmt.Errorf("failed to build artifacts: %s, %w", string(result), err),
						)
					}
				}

				return ux.Success, nil
			},
		}).
		AddTask(ux.TaskOptions{
			Title: "Installing extension",
			Action: func(progress ux.SetProgressFunc) (ux.TaskState, error) {
				if flags.skipInstall {
					return ux.Skipped, nil
				}

				azdConfigDir, err := internal.AzdConfigDir()
				if err != nil {
					return ux.Error, common.NewDetailedError(
						"Failed to get azd config directory",
						fmt.Errorf("failed to get azd config directory: %w", err),
					)
				}

				extensionInstallDir := filepath.Join(azdConfigDir, "extensions", schema.Id)
				extensionBinaryPrefix := strings.ReplaceAll(schema.Id, ".", "-")

				if err := copyBinaryFiles(extensionBinaryPrefix, absOutputPath, extensionInstallDir); err != nil {
					return ux.Error, common.NewDetailedError(
						"Install failed",
						fmt.Errorf("failed to copy files to install directory: %w", err),
					)
				}

				return ux.Success, nil
			},
		})

	return taskList.Run()
}

func copyBinaryFiles(extensionId, sourcePath, destPath string) error {
	if _, err := os.Stat(destPath); os.IsNotExist(err) {
		if err := os.MkdirAll(destPath, os.ModePerm); err != nil {
			return fmt.Errorf("failed to create install directory: %w", err)
		}
	}

	return filepath.Walk(sourcePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Only process files in the root of the source path
		if filepath.Dir(path) != sourcePath {
			return nil
		}

		// Check if the file name starts with the extensionId and is either .exe or has no extension
		if info.Mode().IsRegular() {
			fileName := info.Name()
			if strings.HasPrefix(fileName, extensionId) {
				ext := filepath.Ext(fileName)
				if ext == ".exe" || ext == "" {
					destFilePath := filepath.Join(destPath, fileName)
					if err := internal.CopyFile(path, destFilePath); err != nil {
						return fmt.Errorf("failed to copy file %s to %s: %w", path, destFilePath, err)
					}
				}
			}
		}

		return nil
	})
}

func defaultBuildFlags(flags *buildFlags) {
	if flags.outputPath == "" {
		flags.outputPath = "bin"
	}
}
