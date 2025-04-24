// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"dario.cat/mergo"
	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.extensions/internal"
	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.extensions/internal/models"
	"github.com/azure/azure-dev/cli/azd/pkg/common"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/spf13/cobra"
)

type publishFlags struct {
	extensionPath string
	repository    string
	version       string
	registryPath  string
	artifacts     string
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

			if err := defaultPublishFlags(flags); err != nil {
				return err
			}

			err := runPublishAction(flags)
			if err != nil {
				return err
			}

			internal.WriteCommandSuccess("Extension published successfully")
			return nil
		},
	}

	publishCmd.Flags().StringVarP(
		&flags.extensionPath,
		"path", "p", flags.extensionPath,
		"Path to the azd extension project",
	)
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
	publishCmd.Flags().StringVar(
		&flags.artifacts,
		"artifacts", flags.artifacts,
		"Path to the artifacts to upload to the release (e.g. ./artifacts/*.zip)",
	)

	return publishCmd
}

func runPublishAction(flags *publishFlags) error {
	extensionMetadata, err := models.LoadExtension(flags.extensionPath)
	if err != nil {
		return err
	}

	if flags.version == "" {
		flags.version = extensionMetadata.Version
	}

	if flags.artifacts == "" {
		localRegistryArtifactsPath, err := internal.LocalRegistryArtifactsPath()
		if err != nil {
			return err
		}

		flags.artifacts = filepath.Join(localRegistryArtifactsPath, extensionMetadata.Id, flags.version, "*.zip")
	}

	// Setting remote repository overrides local artifacts
	if flags.repository != "" {
		flags.artifacts = ""
	}

	var release *githubRelease
	artifactMap := map[string]extensions.ExtensionArtifact{}
	assets := []*githubReleaseAsset{}

	tagName := fmt.Sprintf("azd-ext-%s_%s", extensionMetadata.SafeDashId(), flags.version)

	absRegistryPath, err := filepath.Abs(flags.registryPath)
	if err != nil {
		return err
	}

	fmt.Println()

	if flags.repository != "" {
		repo, err := getGithubRepo(flags.extensionPath, flags.repository)
		if err != nil {
			return err
		}

		release, err = getGithubRelease(flags.extensionPath, flags.repository, tagName)
		if err != nil {
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
		fmt.Printf("%s: %s\n", output.WithBold("Artifacts"), flags.artifacts)
	}

	fmt.Printf("%s: %s\n", output.WithBold("Registry"), output.WithHyperlink(absRegistryPath, absRegistryPath))

	taskList := ux.NewTaskList(nil)
	taskList.
		AddTask(ux.TaskOptions{
			Title: "Fetching local artifacts",
			Action: func(spf ux.SetProgressFunc) (ux.TaskState, error) {
				if flags.artifacts == "" {
					return ux.Skipped, nil
				}

				files, err := filepath.Glob(flags.artifacts)
				if err != nil {
					return ux.Error, common.NewDetailedError(
						"Failed to list artifacts",
						fmt.Errorf("failed to list artifacts: %w", err),
					)
				}

				if len(files) == 0 {
					return ux.Error, common.NewDetailedError(
						"Artifacts not found",
						fmt.Errorf("no artifacts found at path: %s", flags.artifacts),
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

					assets = append(assets, &githubReleaseAsset{
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

func createPlatformMetadata(
	extensionMetadata *models.ExtensionSchema,
	osArch string,
	assetName string,
) (map[string]any, error) {
	binaryFileName := getFileNameWithoutExt(assetName)
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

func getGithubRepo(cwd string, repo string) (*githubRepo, error) {
	args := []string{"repo", "view"}
	if repo != "" {
		args = append(args, repo)
	}

	args = append(args, "--json", "nameWithOwner,url")
	viewRepoCmd := exec.Command("gh", args...)
	viewRepoCmd.Dir = cwd

	resultBytes, err := viewRepoCmd.CombinedOutput()
	if err != nil {
		return nil, common.NewDetailedError(
			"Failed to get GitHub repository",
			fmt.Errorf("failed to run command: %w, Command output: %s", err, string(resultBytes)),
		)
	}

	var repoResult *githubRepo
	if err := json.Unmarshal(resultBytes, &repoResult); err != nil {
		return nil, fmt.Errorf("failed to deserialize command output: %w, Command output: %s", err, string(resultBytes))
	}

	return repoResult, nil
}

func getGithubRelease(cwd string, repo string, tagName string) (*githubRelease, error) {
	args := []string{"release", "view", tagName}
	if repo != "" {
		args = append(args, "--repo", repo)
	}

	args = append(args, "--json", "name,tagName,url,assets")

	// #nosec G204: Subprocess launched with variable
	viewReleaseCmd := exec.Command("gh", args...)
	viewReleaseCmd.Dir = cwd

	resultBytes, err := viewReleaseCmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to run command: %w, Command output: %s", err, string(resultBytes))
	}

	var releaseResult *githubRelease
	if err := json.Unmarshal(resultBytes, &releaseResult); err != nil {
		return nil, fmt.Errorf("failed to deserialize command output: %w, Command output: %s", err, string(resultBytes))
	}

	return releaseResult, nil
}

type githubRelease struct {
	Name    string                `json:"name"`
	TagName string                `json:"tagName"`
	Url     string                `json:"url"`
	Assets  []*githubReleaseAsset `json:"assets"`
}

type githubRepo struct {
	Name string `json:"nameWithOwner"`
	Url  string `json:"url"`
}

type githubReleaseAsset struct {
	Id          string `json:"id"`
	ContentType string `json:"contentType"`
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	State       string `json:"state"`
	Url         string `json:"url"`
	Path        string `json:"path"`
}

func defaultPublishFlags(flags *publishFlags) error {
	if flags.extensionPath == "" {
		flags.extensionPath = "."
	}

	if flags.registryPath == "" {
		azdConfigDir, err := internal.AzdConfigDir()
		if err != nil {
			return err
		}

		flags.registryPath = filepath.Join(azdConfigDir, "registry.json")
	}

	return nil
}

var (
	operatingSystems = []string{"windows", "linux", "darwin"}
	architectures    = []string{"amd64", "arm64"}
)
