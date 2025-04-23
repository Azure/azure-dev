// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.extensions/internal"
	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.extensions/internal/models"
	"github.com/azure/azure-dev/cli/azd/pkg/common"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/spf13/cobra"
)

type buildFlags struct {
	cwd          string
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
			err := runBuildAction(flags)
			if err != nil {
				return err
			}

			internal.WriteCommandSuccess("Build completed successfully!")
			return nil
		},
	}

	buildCmd.Flags().
		StringVar(
			&flags.cwd,
			"cwd", ".",
			"Paths to the extension directory. Defaults to the current directory.",
		)
	buildCmd.Flags().
		StringVarP(
			&flags.outputPath,
			"output", "o", "./bin",
			"Path to the output directory. Defaults to ./bin folder.",
		)
	buildCmd.Flags().
		BoolVar(
			&flags.allPlatforms, "all", false,
			"When set builds for all os/platforms. Defaults to the current os/platform only.",
		)
	buildCmd.Flags().
		BoolVar(
			&flags.skipInstall,
			"skip-install", false,
			"When set skips reinstalling extension after successful build.",
		)

	return buildCmd
}

func runBuildAction(flags *buildFlags) error {
	extensionYamlPath := filepath.Join(flags.cwd, "extension.yaml")
	if _, err := os.Stat(extensionYamlPath); err != nil {
		return fmt.Errorf("extension.yaml file not found in the specified path: %w", err)
	}

	absOutputPath, err := filepath.Abs(flags.outputPath)
	if err != nil {
		return fmt.Errorf("failed to get absolute path for output directory: %w", err)
	}

	absExtensionPath, err := filepath.Abs(flags.cwd)
	if err != nil {
		return fmt.Errorf("failed to get absolute path for extension directory: %w", err)
	}

	// Load metadata
	schema, err := models.LoadExtension(flags.cwd)
	if err != nil {
		return fmt.Errorf("failed to load extension metadata: %w", err)
	}

	fmt.Println()
	fmt.Printf("%s: %s\n", output.WithBold("Output Path"), output.WithHyperlink(absOutputPath, absOutputPath))

	taskList := ux.NewTaskList(nil)
	taskList.AddTask(ux.TaskOptions{
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
			buildScript := filepath.Join(flags.cwd, scriptFile)
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
	})

	taskList.AddTask(ux.TaskOptions{
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

	if err := taskList.Run(); err != nil {
		return fmt.Errorf("failed to run build tasks: %w", err)
	}

	return nil
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
	if flags.cwd == "" {
		flags.cwd = "."
	}

	if flags.outputPath == "" {
		flags.outputPath = filepath.Join(flags.cwd, "bin")
	}
}
