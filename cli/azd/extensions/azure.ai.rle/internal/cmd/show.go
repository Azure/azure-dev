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

type showResult struct {
	Environment environmentResource   `json:"environment"`
	Versions    []environmentResource `json:"versions"`
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

	result, err := a.resolveTarget()
	if err != nil {
		return err
	}

	if output.IsJSON() {
		return output.JSON(result)
	}

	rows := make([][]string, 0, len(result.Versions))
	for _, version := range result.Versions {
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

func (a *showAction) resolveTarget() (showResult, error) {
	environmentName := strings.TrimSpace(a.environmentName)
	if environmentName == "" {
		state, err := loadRleState()
		if err != nil {
			return showResult{}, err
		}
		environmentName = strings.TrimSpace(state.Name)
		if environmentName == "" {
			return showResult{}, &azdext.LocalError{
				Message:    "The saved RLE environment does not include a name.",
				Code:       "rle_environment_name_missing",
				Category:   azdext.LocalErrorCategoryUser,
				Suggestion: "Provide an environment name: azd ai rle show <environment-name>.",
			}
		}
	}

	projectEndpoint, err := resolveEnvironmentListProjectEndpoint()
	if err != nil {
		return showResult{}, err
	}
	client, err := createRleClient(projectEndpoint)
	if err != nil {
		return showResult{}, err
	}
	environment, err := resolveLatestEnvironmentByName(a.cmd.Context(), client, environmentName)
	if err != nil {
		return showResult{}, err
	}
	versions, err := resolveEnvironmentVersions(a.cmd.Context(), client, environment)
	if err != nil {
		return showResult{}, serviceError(err)
	}
	return showResult{Environment: *environment, Versions: versions}, nil
}

func resolveEnvironmentVersions(
	ctx context.Context,
	client *rleClient,
	current *environmentResource,
) ([]environmentResource, error) {
	history, err := client.listEnvironmentVersions(ctx, current.Name)
	if err != nil {
		return nil, err
	}
	if len(history) == 0 {
		return []environmentResource{*current}, nil
	}

	versions := make([]environmentResource, 0, len(history))
	for _, summary := range history {
		var version environmentResource
		if summary.EnvironmentId == current.Id && summary.Version == current.Version {
			version = *current
		} else {
			resolved, err := client.getEnvironmentVersion(ctx, current.Name, summary.Version)
			if err != nil {
				return nil, err
			}
			version = *resolved
		}

		version.Id = firstNonEmpty(version.Id, summary.EnvironmentId)
		version.Name = firstNonEmpty(version.Name, current.Name)
		version.Version = firstNonEmpty(version.Version, summary.Version)
		version.AcrImagePath = firstNonEmpty(version.AcrImagePath, summary.AcrImagePath)
		version.CreatedAt = firstNonEmpty(version.CreatedAt, summary.CreatedAt)
		versions = append(versions, version)
	}

	return versions, nil
}
