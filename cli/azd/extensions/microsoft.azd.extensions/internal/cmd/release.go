// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.extensions/internal"
	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.extensions/internal/github"
	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.extensions/internal/models"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/common"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/spf13/cobra"
)

type releaseFlags struct {
	repository string
	artifacts  []string
	title      string
	notes      string
	notesFile  string
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

			err := runReleaseAction(cmd.Context(), flags)
			if err != nil {
				return err
			}

			internal.WriteCommandSuccess("Extension released successfully")
			return nil
		},
	}

	releaseCmd.Flags().StringVarP(
		&flags.repository,
		"repo", "r", flags.repository,
		"Github repository to create the release in (e.g. owner/repo)",
	)
	releaseCmd.Flags().StringSliceVar(
		&flags.artifacts,
		"artifacts", nil,
		"Path to artifacts to upload to the release "+
			"(comma-separated glob patterns, e.g. ./artifacts/*.zip,./artifacts/*.tar.gz)",
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
		&flags.notesFile,
		"notes-file", "F", flags.notesFile,
		"Read release notes from file (use \"-\" to read from standard input)",
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

	if err := azdext.WaitForDebugger(ctx, azdClient); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, azdext.ErrDebuggerAborted) {
			return nil
		}
		return fmt.Errorf("failed waiting for debugger: %w", err)
	}

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

	if flags.title == "" {
		flags.title = fmt.Sprintf("%s (%s)", extensionMetadata.DisplayName, flags.version)
	}

	// Use artifacts patterns from flag
	artifactPatterns := flags.artifacts

	if flags.notes != "" && flags.notesFile != "" {
		return errors.New("only one of --notes or --notes-file can be specified")
	}

	if flags.notesFile != "" {
		if flags.notesFile == "-" {
			// Read from standard input
			notes, err := io.ReadAll(os.Stdin)
			if err != nil {
				return fmt.Errorf("failed to read notes from stdin: %w", err)
			}
			flags.notes = string(notes)
		} else {
			// Read from file
			notes, err := os.ReadFile(flags.notesFile)
			if err != nil {
				return fmt.Errorf("failed to read notes from file: %w", err)
			}
			flags.notes = string(notes)
		}
	}

	// Automatically include CHANGELOG.md if no notes are provided
	if flags.notes == "" {
		fileInfo, err := os.Stat("CHANGELOG.md")
		if err == nil && !fileInfo.IsDir() {
			notes, err := os.ReadFile("CHANGELOG.md")
			if err != nil {
				return fmt.Errorf("failed to read notes from CHANGELOG.md: %w", err)
			}
			flags.notes = string(notes)
		}
	}

	tagName := fmt.Sprintf("azd-ext-%s_%s", extensionMetadata.SafeDashId(), flags.version)

	// Initialize GitHub CLI wrapper
	ghCli, err := github.NewGitHubCli()
	if err != nil {
		return fmt.Errorf("failed to initialize GitHub CLI: %w", err)
	}

	// Check if GitHub CLI is installed using the new method that returns UserFriendlyError
	if err := ghCli.CheckAndGetInstallError(); err != nil {
		return err // Pass the UserFriendlyError through
	}

	repo, err := ghCli.ViewRepository(absExtensionPath, flags.repository)
	if err != nil {
		return err
	}

	fmt.Println()
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

	var release *github.Release

	taskList := ux.NewTaskList(nil).
		AddTask(ux.TaskOptions{
			Title: "Validating artifacts",
			Action: func(spf ux.SetProgressFunc) (ux.TaskState, error) {
				files, err := internal.FindArtifacts(artifactPatterns, extensionMetadata.Id, flags.version)
				if err != nil {
					return ux.Error, common.NewDetailedError("Failed to find artifacts", err)
				}

				if len(files) == 0 {
					patternDisplay := "default registry location"
					if len(artifactPatterns) > 0 {
						patternDisplay = strings.Join(artifactPatterns, ", ")
					}
					return ux.Error, common.NewDetailedError("Artifacts not found",
						fmt.Errorf("no artifacts found at: %s", patternDisplay),
					)
				}

				spf(fmt.Sprintf("Found %d artifacts", len(files)))

				return ux.Success, nil
			},
		}).
		AddTask(
			ux.TaskOptions{
				Title: "Creating Github release",
				Action: func(spf ux.SetProgressFunc) (ux.TaskState, error) {
					files, err := internal.FindArtifacts(artifactPatterns, extensionMetadata.Id, flags.version)
					if err != nil {
						return ux.Error, common.NewDetailedError("Failed to find artifacts", err)
					}

					// Build options map for CreateRelease
					releaseOptions := map[string]string{}
					if flags.notes != "" {
						releaseOptions["notes"] = flags.notes
					}
					if flags.title != "" {
						releaseOptions["title"] = flags.title
					}
					if flags.repository != "" {
						releaseOptions["repo"] = flags.repository
					}
					if flags.preRelease {
						releaseOptions["prerelease"] = "true"
					}
					if flags.draft {
						releaseOptions["draft"] = "true"
					}

					// Create the release and get the result directly
					releaseResult, err := ghCli.CreateRelease(absExtensionPath, tagName, releaseOptions, files)
					if err != nil {
						if errors.Is(err, github.ErrReleaseAlreadyExists) {
							err = internal.NewUserFriendlyError("Release already exists",
								strings.Join([]string{
									fmt.Sprintf(
										"The %s extension already has been released with version %s",
										output.WithHighLightFormat(extensionMetadata.Id),
										output.WithHighLightFormat(flags.version),
									),
									"Please update the version number or delete the existing release before trying again.",
								}, "\n"),
							)
						}

						return ux.Error, common.NewDetailedError("Release failed", err)
					}

					// Store the release for later use
					release = releaseResult

					return ux.Success, nil
				},
			})

	if err := taskList.Run(); err != nil {
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
