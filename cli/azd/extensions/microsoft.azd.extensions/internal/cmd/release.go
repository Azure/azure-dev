// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.extensions/internal"
	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.extensions/internal/models"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/common"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/spf13/cobra"
)

type releaseFlags struct {
	cwd        string
	repository string
	artifacts  string
	title      string
	notes      string
	version    string
	preRelease bool
	draft      bool
	confirm    bool
}

func newReleaseCommand() *cobra.Command {
	flags := &releaseFlags{}
	releaseCmd := &cobra.Command{
		Use:   "release",
		Short: "Create a new extension release from the packaged artifacts",
		RunE: func(cmd *cobra.Command, args []string) error {
			internal.WriteCommandHeader(
				"Release azd extension version (azd x release)",
				"Creates a new Github release for the azd extension project",
			)

			defaultReleaseFlags(flags)
			err := runReleaseAction(cmd.Context(), flags)
			if err != nil {
				return err
			}

			internal.WriteCommandSuccess("Extension released successfully")
			return nil
		},
	}

	releaseCmd.Flags().StringVar(
		&flags.cwd,
		"cwd", ".",
		"Path to the azd extension project",
	)
	releaseCmd.Flags().StringVarP(
		&flags.repository,
		"repo", "r", flags.repository,
		"Github repository to create the release in (e.g. owner/repo)",
	)
	releaseCmd.Flags().StringVar(
		&flags.artifacts,
		"artifacts", flags.artifacts,
		"Path to the artifacts to upload to the release (e.g. ./artifacts/*.zip)",
	)
	releaseCmd.Flags().StringVarP(
		&flags.title,
		"title", "t", flags.title,
		"Title of the release",
	)
	releaseCmd.Flags().StringVarP(
		&flags.notes,
		"notes", "n", flags.notes,
		"Release notes",
	)
	releaseCmd.Flags().StringVarP(
		&flags.version,
		"version", "v", flags.version,
		"Version of the release",
	)
	releaseCmd.Flags().BoolVar(
		&flags.preRelease,
		"prerelease", flags.preRelease,
		"Create a pre-release version",
	)
	releaseCmd.Flags().BoolVarP(
		&flags.draft, "draft", "d",
		flags.draft,
		"Create a draft release",
	)
	releaseCmd.Flags().BoolVar(
		&flags.confirm,
		"confirm", flags.confirm,
		"Skip confirmation prompt",
	)

	releaseCmd.MarkFlagRequired("repo")

	return releaseCmd
}

func runReleaseAction(ctx context.Context, flags *releaseFlags) error {
	// Create a new context that includes the AZD access token
	ctx = azdext.WithAccessToken(ctx)

	// Create a new AZD client
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return fmt.Errorf("failed to create azd client: %w", err)
	}

	defer azdClient.Close()

	extensionMetadata, err := models.LoadExtension(flags.cwd)
	if err != nil {
		return err
	}

	if flags.version == "" {
		flags.version = extensionMetadata.Version
	}

	if flags.title == "" {
		flags.title = fmt.Sprintf("%s (%s)", extensionMetadata.DisplayName, flags.version)
	}

	if flags.artifacts == "" {
		localRegistryArtifactsPath, err := internal.LocalRegistryArtifactsPath()
		if err != nil {
			return err
		}

		flags.artifacts = filepath.Join(localRegistryArtifactsPath, extensionMetadata.Id, flags.version, "*.zip")
	}

	tagName := fmt.Sprintf("azd-ext-%s_%s", extensionMetadata.SafeDashId(), flags.version)

	args := []string{
		"release",
		"create",
		tagName,
	}

	if flags.notes != "" {
		args = append(args, "--notes", flags.notes)
	}

	if flags.title != "" {
		args = append(args, "--title", flags.title)
	}

	if flags.repository != "" {
		args = append(args, "--repo", flags.repository)
	}

	if flags.preRelease {
		args = append(args, "--prerelease")
	}

	if flags.draft {
		args = append(args, "--draft")
	}

	var releaseResult string

	repo, err := getGithubRepo(flags.cwd, flags.repository)
	if err != nil {
		return err
	}

	fmt.Println()
	fmt.Printf("%s: %s\n", output.WithBold("Artifacts"), flags.artifacts)
	fmt.Printf("%s: %s - %s\n",
		output.WithBold("GitHub Repo"),
		repo.Name,
		output.WithHyperlink(repo.Url, "View Repo"),
	)
	fmt.Printf("%s: %s (%s)\n", "GitHub Release", flags.title, tagName)
	fmt.Printf("%s: %t\n", output.WithBold("Prerelease"), flags.preRelease)
	fmt.Printf("%s: %t\n", output.WithBold("Draft"), flags.draft)

	if !flags.confirm {
		fmt.Println()
		confirmReleaseResponse, err := azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
			Options: &azdext.ConfirmOptions{
				Message:      "Are you sure you want to create the GitHub release?",
				DefaultValue: internal.ToPtr(false),
				Placeholder:  "no",
			},
		})
		if err != nil {
			return fmt.Errorf("failed to prompt for confirmation: %w", err)
		}

		if !*confirmReleaseResponse.Value {
			return errors.New("release cancelled by user")
		}
	}

	taskList := ux.NewTaskList(nil)
	taskList.
		AddTask(ux.TaskOptions{
			Title: "Validating artifacts",
			Action: func(spf ux.SetProgressFunc) (ux.TaskState, error) {
				files, err := filepath.Glob(flags.artifacts)
				if err != nil {
					return ux.Error, common.NewDetailedError("Artifacts not found",
						fmt.Errorf("failed to find artifacts: %w", err),
					)
				}

				if len(files) == 0 {
					return ux.Error, common.NewDetailedError("Artifacts not found",
						fmt.Errorf("no artifacts found at path: %s.", flags.artifacts),
					)
				}

				spf(fmt.Sprintf("Found %d artifacts", len(files)))
				args = append(args, files...)

				return ux.Success, nil
			},
		}).
		AddTask(
			ux.TaskOptions{
				Title: "Creating Github release",
				Action: func(spf ux.SetProgressFunc) (ux.TaskState, error) {
					// #nosec G204: Subprocess launched with variable
					ghReleaseCmd := exec.Command("gh", args...)
					ghReleaseCmd.Dir = flags.cwd

					resultBytes, err := ghReleaseCmd.CombinedOutput()
					releaseResult = string(resultBytes)
					if err != nil {
						return ux.Error, common.NewDetailedError(
							"Release failed",
							errors.New(releaseResult),
						)
					}

					return ux.Success, nil
				},
			})

	if err := taskList.Run(); err != nil {
		return err
	}

	release, err := getGithubRelease(flags.cwd, flags.repository, tagName)
	if err != nil {
		return err
	}

	fmt.Printf("%s: %s - %s\n",
		output.WithBold("GitHub Release"),
		release.Name,
		output.WithHyperlink(release.Url, "View Release"),
	)
	fmt.Println()

	return nil
}

func defaultReleaseFlags(flags *releaseFlags) {
	if flags.cwd == "" {
		flags.cwd = "."
	}
}
