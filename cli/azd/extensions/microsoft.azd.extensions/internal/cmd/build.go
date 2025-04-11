// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.extensions/internal"
	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.extensions/internal/models"
	"github.com/azure/azure-dev/cli/azd/pkg/common"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/spf13/cobra"
)

func newBuildCommand() *cobra.Command {
	buildCmd := &cobra.Command{
		Use:   "build",
		Short: "Build the azd extension project",
		RunE:  buildExtension,
	}

	buildCmd.Flags().StringP("path", "p", ".", "Paths to the extension directory. Defaults to the current directory.")
	buildCmd.Flags().StringP("output", "o", "", "Path to the output directory. Defaults to relative /bin folder.")
	buildCmd.Flags().Bool("all", false, "When set builds for all os/platforms. Defaults to the current os/platform only.")
	buildCmd.Flags().Bool("skip-install", false, "When set skips reinstalling extension after successful build.")

	return buildCmd
}

func buildExtension(cmd *cobra.Command, args []string) error {
	extensionPath, _ := cmd.Flags().GetString("path")
	outputPath, _ := cmd.Flags().GetString("output")
	allPlatforms, _ := cmd.Flags().GetBool("all")
	skipInstall, _ := cmd.Flags().GetBool("skip-install")

	azdConfigDir := os.Getenv("AZD_CONFIG_DIR")
	if azdConfigDir == "" {
		userHomeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get user home directory: %w", err)
		}
		azdConfigDir = filepath.Join(userHomeDir, ".azd")
	}

	if outputPath == "" {
		outputPath = filepath.Join(extensionPath, "bin")
	}

	internal.WriteCommandHeader(
		"Build and azd extension (azd x build)",
		"Builds the azd extension project for one ore more platforms",
	)

	extensionYamlPath := filepath.Join(extensionPath, "extension.yaml")
	if _, err := os.Stat(extensionYamlPath); err != nil {
		return fmt.Errorf("extension.yaml file not found in the specified path: %w", err)
	}

	absOutputPath, err := filepath.Abs(outputPath)
	if err != nil {
		return fmt.Errorf("failed to get absolute path for output directory: %w", err)
	}

	absExtensionPath, err := filepath.Abs(extensionPath)
	if err != nil {
		return fmt.Errorf("failed to get absolute path for extension directory: %w", err)
	}

	// Load metadata
	schema, err := models.LoadExtension(extensionPath)
	if err != nil {
		return fmt.Errorf("failed to load extension metadata: %w", err)
	}

	taskList := ux.NewTaskList(nil)
	taskList.AddTask(ux.TaskOptions{
		Title: "Building extension artifacts",
		Action: func(progress ux.SetProgressFunc) (ux.TaskState, error) {
			// Build the binaries
			buildScript := filepath.Join(extensionPath, "build.sh")
			if _, err := os.Stat(buildScript); err == nil {
				// nolint:gosec // G204
				cmd := exec.Command("bash", "build.sh")
				cmd.Dir = extensionPath

				envVars := map[string]string{
					"OUTPUT_DIR":        absOutputPath,
					"EXTENSION_DIR":     absExtensionPath,
					"EXTENSION_ID":      schema.Id,
					"EXTENSION_VERSION": schema.Version,
				}

				// By default builds for current os/arch
				if !allPlatforms {
					envVars["EXTENSION_PLATFORM"] = fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)
				}

				cmd.Env = os.Environ()

				for key, value := range envVars {
					cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", key, value))
				}

				if result, err := cmd.CombinedOutput(); err != nil {
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
			if skipInstall {
				return ux.Skipped, nil
			}

			extensionInstallDir := filepath.Join(azdConfigDir, "extensions", schema.Id)
			if err := copyFiles(absOutputPath, extensionInstallDir); err != nil {
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

	internal.WriteCommandSuccess("Build completed successfully!")
	return nil
}

func copyFiles(sourcePath, destPath string) error {
	if _, err := os.Stat(destPath); os.IsNotExist(err) {
		if err := os.MkdirAll(destPath, os.ModePerm); err != nil {
			return fmt.Errorf("failed to create install directory: %w", err)
		}
	}

	return filepath.Walk(sourcePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(sourcePath, path)
		if err != nil {
			return err
		}

		destPath := filepath.Join(destPath, relPath)

		if info.IsDir() {
			if err := os.MkdirAll(destPath, internal.PermissionDirectory); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", destPath, err)
			}
		} else {
			if err := copyFile(path, destPath); err != nil {
				return fmt.Errorf("failed to copy file %s to %s: %w", path, destPath, err)
			}
		}

		return nil
	})
}

func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	if _, err := io.Copy(destFile, sourceFile); err != nil {
		return err
	}

	return destFile.Sync()
}
