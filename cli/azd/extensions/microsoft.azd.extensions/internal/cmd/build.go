// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.extensions/internal"
	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.extensions/internal/models"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
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

func newBuildCommand(outputPath *string) *cobra.Command {
	flags := &buildFlags{}

	buildCmd := &cobra.Command{
		Use:   "build",
		Short: "Build the azd extension project",
		RunE: func(cmd *cobra.Command, args []string) error {
			internal.WriteCommandHeader(
				"Build and azd extension (azd x build)",
				"Builds the azd extension project for one or more platforms",
			)

			if outputPath != nil {
				flags.outputPath = *outputPath
			}
			defaultBuildFlags(flags)
			err := runBuildAction(cmd.Context(), flags)
			if err != nil {
				return err
			}

			internal.WriteCommandSuccess("Build completed successfully!")
			return nil
		},
	}

	azdext.RegisterFlagOptions(buildCmd, azdext.FlagOptions{
		Name:    "output",
		Default: "./bin",
		Usage:   "Path to the output directory.",
	})
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
	// Create a new context that includes the AZD access token
	ctx = azdext.WithAccessToken(ctx)

	// Create a new AZD client
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return fmt.Errorf("failed to create azd client: %w", err)
	}

	defer azdClient.Close()

	// Wait for debugger if AZD_EXT_DEBUG is set
	if err := azdext.WaitForDebugger(ctx, azdClient); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, azdext.ErrDebuggerAborted) {
			return nil
		}
		return fmt.Errorf("failed waiting for debugger: %w", err)
	}

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

				warnings, validationErrors := validateExtensionMetadata(schema)

				progress("Validation complete")

				if len(validationErrors) > 0 {
					aggregatedError := fmt.Errorf(
						"Extension contains validation failures: %s",
						strings.Join(validationErrors, "; "),
					)
					return ux.Error, common.NewDetailedError("Validation failed", aggregatedError)
				}

				if len(warnings) > 0 {
					return ux.Warning, errors.New(validationWarningsMessage(warnings))
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

	// On Windows, kill any running extension processes to release file locks
	if runtime.GOOS == "windows" {
		killExtensionProcesses(extensionId, destPath)
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
					// Set execute permissions on the copied binary
					if err := os.Chmod(destFilePath, 0755); err != nil {
						return fmt.Errorf("failed to set execute permissions on %s: %w", destFilePath, err)
					}
				}
			}
		}

		return nil
	})
}

// validateExtensionMetadata returns validation warnings and required-field errors
// for the given extension schema. Errors indicate missing required fields. Warnings
// flag recommended but optional fields that improve the extension experience.
func validateExtensionMetadata(schema *models.ExtensionSchema) (warnings, errs []string) {
	// Required fields - missing values are errors.
	if schema.Id == "" {
		errs = append(errs, "Missing required field: id")
	}
	if schema.Version == "" {
		errs = append(errs, "Missing required field: version")
	}
	if len(schema.Capabilities) == 0 {
		errs = append(errs, "Missing required field: capabilities")
	}
	if schema.DisplayName == "" {
		errs = append(errs, "Missing required field: displayName")
	}
	if schema.Description == "" {
		errs = append(errs, "Missing required field: description")
	}

	// Capability-specific recommendations.
	hasCustomCommands := slices.Contains(schema.Capabilities, extensions.CustomCommandCapability)
	hasServiceTarget := slices.Contains(schema.Capabilities, extensions.ServiceTargetProviderCapability)

	// Missing namespace is fatal for custom-commands extensions: bindExtension
	// uses the last '.'-segment of Namespace as the cobra command name, so an
	// empty namespace silently installs an unreachable command. The init wizard
	// always populates namespace, so this only triggers on hand-edited files.
	if hasCustomCommands && schema.Namespace == "" {
		errs = append(errs,
			"Missing 'namespace' field in extension.yaml - "+
				"required by the 'custom-commands' capability. "+
				"Set it to the prefix users will type after 'azd' (e.g. 'demo' to expose 'azd demo <command>').",
		)
	}

	// Kept as a warning: the init wizard doesn't yet prompt for providers, so
	// promoting this to an error would block every service-target-provider scaffold.
	if hasServiceTarget && len(schema.Providers) == 0 {
		warnings = append(warnings,
			"Missing 'providers' field in extension.yaml - "+
				"required by the 'service-target-provider' capability. "+
				"List the providers your extension contributes (each entry needs a name, type, and description).",
		)
	}

	if schema.Usage == "" {
		warnings = append(warnings,
			"Missing 'usage' field in extension.yaml - shown to users as a usage hint in 'azd <namespace> --help'.",
		)
	}

	return warnings, errs
}

// validationWarningsMessage formats validation warnings into a multi-line message with bullet points.
func validationWarningsMessage(warnings []string) string {
	var message strings.Builder
	message.WriteString("validation warnings:")
	for _, warning := range warnings {
		message.WriteString("\n  - ")
		message.WriteString(warning)
	}

	return message.String()
}

// escapePowerShellSingleQuotes escapes single quotes for use in PowerShell single-quoted strings.
func escapePowerShellSingleQuotes(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// killExtensionProcesses terminates any running extension processes on Windows
// to release file locks before copying new binaries.
func killExtensionProcesses(extensionBinaryPrefix, installDir string) {
	// Kill by process name pattern (e.g., jongio-azd-copilot-windows-amd64)
	for _, arch := range []string{"windows-amd64", "windows-arm64"} {
		procName := extensionBinaryPrefix + "-" + arch
		//nolint:gosec // G204: procName is derived from extension metadata; single quotes are escaped
		_ = exec.Command("powershell", "-NoProfile", "-Command",
			"Stop-Process -Name '"+escapePowerShellSingleQuotes(procName)+"' -Force -ErrorAction SilentlyContinue").Run()
	}

	// Kill any processes running from the install directory.
	// Pass installDir as a parameter to avoid injection from special characters.
	if installDir != "" {
		//nolint:gosec // G204: installDir is derived from config and passed as an argument, not interpolated
		_ = exec.Command("powershell", "-NoProfile", "-Command",
			"param([string]$p) Get-Process | Where-Object { $_.Path -and $_.Path.StartsWith($p) }"+
				" | Stop-Process -Force -ErrorAction SilentlyContinue",
			installDir,
		).Run()
	}
}

func defaultBuildFlags(flags *buildFlags) {
	if flags.outputPath == "" {
		flags.outputPath = "bin"
	}
}
