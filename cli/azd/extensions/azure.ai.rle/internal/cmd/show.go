// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"strings"

	"azure.ai.rle/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type showAction struct {
	cmd             *cobra.Command
	outputFormat    *string
	environmentName string
}

func newShowCommand(outputFormat *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show [environment-name]",
		Short: "Show RLE environment details",
		Long: `Show RLE environment details.

The command resolves the environment from the Foundry project and includes its
full version history. With no environment name, it uses rle.name from the
current folder's rle.toml.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			environmentName := ""
			if len(args) == 1 {
				environmentName = args[0]
			}
			return (&showAction{
				cmd:             cmd,
				outputFormat:    outputFormat,
				environmentName: environmentName,
			}).Run()
		},
	}
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name:          "output",
		AllowedValues: []string{"default", "json"},
	})
	return cmd
}

func (a *showAction) Run() error {
	format, err := azdext.ParseOutputFormat(*a.outputFormat)
	if err != nil {
		return err
	}
	output := azdext.NewOutput(azdext.OutputOptions{
		Format:    format,
		Writer:    a.cmd.OutOrStdout(),
		ErrWriter: a.cmd.ErrOrStderr(),
	})

	versions, err := a.resolveTarget()
	if err != nil {
		return err
	}

	if output.IsJSON() {
		return output.JSON(versions)
	}

	rows := make([][]string, 0, len(versions))
	for _, version := range versions {
		rows = append(rows, []string{
			version.Version,
			firstNonEmpty(version.Harness.Type, "-"),
			firstNonEmpty(version.Harness.Protocol, "-"),
			firstNonEmpty(version.DiskImageConversionStatus, "-"),
			version.UpdatedAt,
		})
	}
	renderTableOrNoResults(output,
		[]string{"VERSION", "TYPE", "PROTOCOL", "DISK IMAGE", "UPDATED"},
		rows,
		noEnvironmentVersionsMessage,
	)
	for _, version := range versions {
		if len(version.Tasks) == 0 {
			continue
		}
		taskRows := make([][]string, 0, len(version.Tasks))
		for _, task := range version.Tasks {
			taskRows = append(taskRows, []string{
				task.Name,
				firstNonEmpty(task.DiskImageConversionStatus, "-"),
				sanitizeTerminalText(task.DiskImageConversionError),
			})
		}
		fmt.Fprintf(a.cmd.OutOrStdout(), "\nTasks in version %s:\n", version.Version)
		renderTableOrNoResults(output, []string{"TASK", "DISK IMAGE", "ERROR"}, taskRows, "")
	}
	return nil
}

func (a *showAction) resolveTarget() ([]environmentResource, error) {
	environmentName := strings.TrimSpace(a.environmentName)
	if environmentName == "" {
		config, err := project.LoadRleConfig(".")
		if err != nil {
			return nil, err
		}
		environmentName = config.Rle.Name
	}
	projectEndpoint, err := resolveEnvironmentListProjectEndpoint()
	if err != nil {
		return nil, err
	}
	client, err := createRleClient(projectEndpoint)
	if err != nil {
		return nil, err
	}
	versions, err := listAllEnvironmentVersions(a.cmd.Context(), client, environmentName)
	if err != nil {
		return nil, err
	}
	for i := range versions {
		// Task environments have no top-level image or environment-level status; status is per task.
		if versions[i].DiskImageConversionStatus != "" || versions[i].AcrImagePath != "" {
			continue
		}
		tasks, err := listAllVersionTasks(a.cmd.Context(), client, environmentName, versions[i].Version)
		if err != nil {
			return nil, serviceError(err)
		}
		versions[i].Tasks = tasks
	}
	return versions, nil
}

func listAllVersionTasks(
	ctx context.Context,
	client *rleClient,
	environmentName string,
	version string,
) ([]harborTaskStatus, error) {
	var tasks []harborTaskStatus
	token := ""
	for range environmentListMaxPages {
		page, err := client.listEnvironmentVersionTasks(ctx, environmentName, version, token, environmentListPageSize)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, page.Data...)
		if strings.TrimSpace(page.NextContinuationToken) == "" {
			return tasks, nil
		}
		token = page.NextContinuationToken
	}
	return nil, paginationSafetyLimitError(
		"Environment task list",
		"rle_environment_task_list_safety_limit",
	)
}

func listAllEnvironmentVersions(
	ctx context.Context,
	client *rleClient,
	environmentName string,
) ([]environmentResource, error) {
	history := make([]environmentResource, 0)
	continuationToken := ""
	complete := false
	seenCursors := map[string]struct{}{}
	for range environmentListMaxPages {
		page, err := client.listEnvironmentVersions(ctx, environmentName, continuationToken, environmentListPageSize)
		if isRleNotFound(err) {
			return nil, environmentNotFoundError(environmentName)
		}
		if err != nil {
			return nil, serviceError(err)
		}
		history = append(history, page.Data...)
		if strings.TrimSpace(page.NextContinuationToken) == "" {
			complete = true
			break
		}
		continuationToken, err = nextPaginationCursor(
			seenCursors,
			page.NextContinuationToken,
			func() error {
				return &azdext.LocalError{
					Message:  "Environment version pagination did not return a new continuation token.",
					Code:     "rle_environment_version_cursor_invalid",
					Category: azdext.LocalErrorCategoryInternal,
				}
			},
		)
		if err != nil {
			return nil, err
		}
	}
	if !complete {
		return nil, paginationSafetyLimitError(
			"Environment version list",
			"rle_environment_version_list_safety_limit",
		)
	}
	return history, nil
}
