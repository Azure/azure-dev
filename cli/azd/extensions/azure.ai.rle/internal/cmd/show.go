// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"strings"

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
full version history. With no environment name, it uses the name saved in
.azd-rle.json.`,
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
			version.DiskImageConversionStatus,
			version.Id,
			version.UpdatedAt,
		})
	}
	output.Message("")
	output.Table(
		[]string{"VERSION", "DISK IMAGE", "ENVIRONMENT ID", "UPDATED"},
		rows,
	)
	output.Message("")
	return nil
}

func (a *showAction) resolveTarget() ([]environmentResource, error) {
	environmentName := strings.TrimSpace(a.environmentName)
	projectEndpoint := ""
	if environmentName == "" {
		state, err := loadRleState()
		if err != nil {
			return nil, err
		}
		environmentName = strings.TrimSpace(state.EnvironmentName)
		if environmentName == "" {
			return nil, &azdext.LocalError{
				Message:    "The saved RLE environment does not include a name.",
				Code:       "rle_environment_name_missing",
				Category:   azdext.LocalErrorCategoryUser,
				Suggestion: "Provide an environment name: azd ai rle show <environment-name>.",
			}
		}
		projectEndpoint = strings.TrimSpace(state.ProjectEndpoint)
		if projectEndpoint == "" {
			return nil, &azdext.LocalError{
				Message:  "The saved RLE environment does not include a Foundry project endpoint.",
				Code:     "rle_project_required",
				Category: azdext.LocalErrorCategoryUser,
				Suggestion: "Run azd ai rle publish first, or provide an environment name " +
					"with FOUNDRY_PROJECT_ENDPOINT set.",
			}
		}
	}

	if projectEndpoint == "" {
		var err error
		projectEndpoint, err = resolveEnvironmentListProjectEndpoint()
		if err != nil {
			return nil, err
		}
	}
	client, err := createRleClient(projectEndpoint)
	if err != nil {
		return nil, err
	}
	return listAllEnvironmentVersions(a.cmd.Context(), client, environmentName)
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
	if len(history) == 0 {
		return nil, environmentVersionsNotFoundError(environmentName)
	}
	return history, nil
}
