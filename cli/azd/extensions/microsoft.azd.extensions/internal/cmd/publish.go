// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dario.cat/mergo"
	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.extensions/internal"
	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.extensions/internal/github"
	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.extensions/internal/models"
	"github.com/azure/azure-dev/cli/azd/pkg/common"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/spf13/cobra"
)

type publishFlags struct {
	repository   string
	version      string
	registryPath string
	artifacts    []string
}

func newPublishCommand() *cobra.Command {
	flags := &publishFlags{}
	publishCmd := &cobra.Command{
		Use:   "publish",
		Short: "Publish the extension to the extension source",
		RunE: func(cmd *cobra.Command, args []string) error {
			internal.WriteCommandHeader(
				"Publish azd extension (azd x publish)",
				"Publishes the azd extension project and updates the registry",
			)

			defaultRegistryUsed, err := defaultPublishFlags(flags)
			if err != nil {
				return err
			}

			err = runPublishAction(cmd.Context(), flags, defaultRegistryUsed)
			if err != nil {
				return err
			}

			internal.WriteCommandSuccess("Extension published successfully")
			return nil
		},
	}

	publishCmd.Flags().StringVar(
		&flags.repository,
		"repo", flags.repository,
		"Github repository to create the release in (e.g. owner/repo)",
	)
	publishCmd.Flags().StringVarP(
		&flags.version,
		"version", "v", flags.version,
		"Version of the release",
	)
	publishCmd.Flags().StringVarP(
		&flags.registryPath,
		"registry", "r", flags.registryPath,
		"Path to the extension source registry",
	)
	publishCmd.Flags().StringSliceVar(
		&flags.artifacts,
		"artifacts", nil,
		"Path to artifacts to process (comma-separated glob patterns, e.g. ./artifacts/*.zip,./artifacts/*.tar.gz)",
	)

	return publishCmd
}

func runPublishAction(ctx context.Context, flags *publishFlags, defaultRegistryUsed bool) error {
	absExtensionPath, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get absolute path for extension directory: %w", err)
	}

	extensionMetadata, err := models.LoadExtension(absExtensionPath)
	if err != nil {
		return err
	}

	if flags.version == "" {
		flags.version = extensionMetadata.Version
	}

	// Use artifacts patterns from flag
	artifactPatterns := flags.artifacts

	// Setting remote repository overrides local artifacts
	if flags.repository != "" {
		artifactPatterns = nil
	}

	var release *github.Release
	artifactMap := map[string]extensions.ExtensionArtifact{}
	assets := []*github.ReleaseAsset{}

	tagName := fmt.Sprintf("azd-ext-%s_%s", extensionMetadata.SafeDashId(), flags.version)

	absRegistryPath, err := filepath.Abs(flags.registryPath)
	if err != nil {
		return err
	}

	fmt.Println()

	// Initialize GitHub CLI wrapper
	ghCli, err := github.NewGitHubCli()
	if err != nil {
		return fmt.Errorf("failed to initialize GitHub CLI: %w", err)
	}

	// Check if GitHub CLI is installed when repository is specified
	if flags.repository != "" {
		if err := ghCli.CheckAndGetInstallError(); err != nil {
			return err
		}
	}

	if flags.repository != "" {
		repo, err := ghCli.ViewRepository(absExtensionPath, flags.repository)
		if err != nil {
			return err
		}

		release, err = ghCli.ViewRelease(absExtensionPath, flags.repository, tagName)
		if err != nil {
			if errors.Is(err, github.ErrReleaseNotFound) {
				return internal.NewUserFriendlyError("Github Release not found", strings.Join([]string{
					fmt.Sprintf(
						"The %s extension does not have a release tagged with version %s.",
						output.WithHighLightFormat(extensionMetadata.Id),
						output.WithHighLightFormat(flags.version),
					),
					fmt.Sprintf(
						"To create a new release, run: %s and then try again.",
						output.WithHighLightFormat("azd x release --repo {owner}/{repo}"),
					),
				}, "\n"))
			}

			return err
		}

		fmt.Printf("%s: %s - %s\n",
			output.WithBold("GitHub Repo"),
			repo.Name,
			output.WithHyperlink(repo.Url, "View Repo"),
		)
		fmt.Printf("%s: %s (%s) - %s\n",
			output.WithBold("GitHub Release"),
			release.Name,
			release.TagName,
			output.WithHyperlink(release.Url, "View Release"),
		)
	} else {
		// Show what artifacts will be processed
		if len(artifactPatterns) > 0 {
			fmt.Printf("%s: %s\n", output.WithBold("Artifacts"), strings.Join(artifactPatterns, ", "))
		} else {
			defaultPatterns, err := internal.DefaultArtifactPatterns(extensionMetadata.Id, flags.version)
			if err == nil {
				fmt.Printf("%s: %s (default)\n", output.WithBold("Artifacts"), strings.Join(defaultPatterns, ", "))
			} else {
				fmt.Printf("%s: <default registry path>\n", output.WithBold("Artifacts"))
			}
		}
	}

	fmt.Printf("%s: %s\n", output.WithBold("Registry"), output.WithHyperlink(absRegistryPath, absRegistryPath))

	taskList := ux.NewTaskList(nil).
		AddTask(ux.TaskOptions{
			Title: "Fetching local artifacts",
			Action: func(spf ux.SetProgressFunc) (ux.TaskState, error) {
				if flags.repository != "" {
					return ux.Skipped, nil
				}

				files, err := internal.FindArtifacts(artifactPatterns, extensionMetadata.Id, flags.version)
				if err != nil {
					return ux.Error, common.NewDetailedError(
						"Failed to find artifacts",
						err,
					)
				}

				if len(files) == 0 {
					patternDisplay := "default registry location"
					if len(artifactPatterns) > 0 {
						patternDisplay = strings.Join(artifactPatterns, ", ")
					}
					return ux.Error, common.NewDetailedError(
						"Artifacts not found",
						fmt.Errorf("no artifacts found at: %s", patternDisplay),
					)
				}

				for _, file := range files {
					fileInfo, err := os.Stat(file)
					if err != nil {
						return ux.Error, common.NewDetailedError(
							"Failed to get file info",
							fmt.Errorf("failed to get file info: %w", err),
						)
					}

					absFilePath, err := filepath.Abs(file)
					if err != nil {
						return ux.Error, common.NewDetailedError(
							"Failed to get absolute file path",
							fmt.Errorf("failed to get absolute file path: %w", err),
						)
					}

					assets = append(assets, &github.ReleaseAsset{
						Name: filepath.Base(file),
						Path: absFilePath,
						Size: fileInfo.Size(),
					})
				}

				return ux.Success, nil
			},
		}).
		AddTask(ux.TaskOptions{
			Title: "Fetching GitHub release assets",
			Action: func(spf ux.SetProgressFunc) (ux.TaskState, error) {
				if flags.repository == "" {
					return ux.Skipped, nil
				}

				for _, asset := range release.Assets {
					spf(fmt.Sprintf("Processing %s", asset.Name))
					tempPath, err := internal.DownloadAssetToTemp(asset.Url, asset.Name)
					if err != nil {
						return ux.Error, common.NewDetailedError(
							"Failed to download asset",
							fmt.Errorf("failed to download asset: %w", err),
						)
					}

					asset.Path = tempPath
					assets = append(assets, asset)
				}

				return ux.Success, nil
			},
		}).
		AddTask(ux.TaskOptions{
			Title: "Generating extension metadata",
			Action: func(spf ux.SetProgressFunc) (ux.TaskState, error) {
				for _, asset := range assets {
					spf(fmt.Sprintf("Processing %s", asset.Name))

					osArch, err := internal.InferOSArch(asset.Name)
					if err != nil {
						return ux.Error, common.NewDetailedError(
							"Invalid asset name",
							fmt.Errorf("failed to infer OS and architecture from asset name: %w", err),
						)
					}

					// Compute checksum
					checksum, err := internal.ComputeChecksum(asset.Path)
					if err != nil {
						return ux.Error, common.NewDetailedError(
							"Failed to compute checksum",
							fmt.Errorf("failed to compute checksum: %w", err),
						)
					}

					artifactMetadata, err := createPlatformMetadata(extensionMetadata, osArch, asset.Name)
					if err != nil {
						return ux.Error, common.NewDetailedError(
							"Failed to create platform metadata",
							fmt.Errorf("failed to create platform metadata: %w", err),
						)
					}

					artifactPath := asset.Url
					if artifactPath == "" {
						artifactPath = asset.Path
					}

					artifactMap[osArch] = extensions.ExtensionArtifact{
						URL: artifactPath,
						Checksum: extensions.ExtensionChecksum{
							Algorithm: "sha256",
							Value:     checksum,
						},
						AdditionalMetadata: artifactMetadata,
					}

					// Remove temp file assets that were downloaded and processed.
					if asset.Url != "" && asset.Path != "" {
						defer os.Remove(asset.Path)
					}
				}

				return ux.Success, nil
			},
		}).
		AddTask(ux.TaskOptions{
			Title: "Ensuring local extension source registry exists",
			Action: func(spf ux.SetProgressFunc) (ux.TaskState, error) {
				if !defaultRegistryUsed {
					return ux.Skipped, nil
				}

				if has, err := internal.HasLocalRegistry(); err == nil && has {
					return ux.Skipped, nil
				}

				if err := internal.CreateLocalRegistry(); err != nil {
					return ux.Error, common.NewDetailedError(
						"Registry creation failed",
						fmt.Errorf("failed to create local registry: %w", err),
					)
				}

				return ux.Success, nil
			},
		}).
		AddTask(ux.TaskOptions{
			Title: "Updating extension source registry",
			Action: func(spf ux.SetProgressFunc) (ux.TaskState, error) {
				registry, err := models.LoadRegistry(flags.registryPath)
				if err != nil {
					return ux.Error, common.NewDetailedError(
						"Failed to load registry",
						fmt.Errorf("failed to load registry: %w", err),
					)
				}

				addOrUpdateExtension(registry, extensionMetadata, artifactMap)
				if err := saveRegistry(flags.registryPath, registry); err != nil {
					return ux.Error, common.NewDetailedError(
						"Failed to save registry",
						fmt.Errorf("failed to save registry: %w", err),
					)
				}

				return ux.Success, nil
			},
		})

	return taskList.Run()
}

func addOrUpdateExtension(
	registry *extensions.Registry,
	extensionMetadata *models.ExtensionSchema,
	artifacts map[string]extensions.ExtensionArtifact,
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
				Providers:    extensionMetadata.Providers,
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
		Providers:    extensionMetadata.Providers,
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

func createPlatformMetadata(
	extensionMetadata *models.ExtensionSchema,
	osArch string,
	assetName string,
) (map[string]any, error) {
	binaryFileName := internal.GetFileNameWithoutExt(assetName)
	if strings.Contains(binaryFileName, "windows") {
		binaryFileName += ".exe"
	}

	platformMetadata := map[string]any{
		"entryPoint": binaryFileName,
	}

	for _, os := range operatingSystems {
		if err := mergo.Merge(&platformMetadata, extensionMetadata.Platforms[os]); err != nil {
			return nil, fmt.Errorf("failed to merge os metadata: %w", err)
		}
	}

	for _, arch := range architectures {
		if err := mergo.Merge(&platformMetadata, extensionMetadata.Platforms[arch]); err != nil {
			return nil, fmt.Errorf("failed to merge architecture metadata: %w", err)
		}
	}

	if err := mergo.Merge(&platformMetadata, extensionMetadata.Platforms[osArch]); err != nil {
		return nil, fmt.Errorf("failed to merge os/arch metadata: %w", err)
	}

	return platformMetadata, nil
}

// defaultPublishFlags sets flags.registryPath to <azd config>/registry.json if unset.
// Returns true when that default was applied.
func defaultPublishFlags(flags *publishFlags) (bool, error) {
	if flags.registryPath == "" {
		azdConfigDir, err := internal.AzdConfigDir()
		if err != nil {
			return false, err
		}

		flags.registryPath = filepath.Join(azdConfigDir, "registry.json")
		return true, nil
	}

	return false, nil
}

var (
	operatingSystems = []string{"windows", "linux", "darwin"}
	architectures    = []string{"amd64", "arm64"}
)
