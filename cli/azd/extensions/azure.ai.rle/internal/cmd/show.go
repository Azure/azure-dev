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

With no environment name, show uses the environment saved in .azd-rle.json.
When an environment name is provided, the command resolves it from the Foundry
project and includes its full version history.`,
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

	if len(result.Versions) == 0 {
		output.Message("No version history found.")
		return nil
	}

	rows := make([][]string, 0, len(result.Versions))
	for _, version := range result.Versions {
		rows = append(rows, []string{
			version.Name,
			version.Version,
			version.DiskImageConversionStatus,
			version.Id,
			version.UpdatedAt,
			version.AcrImagePath,
		})
	}
	output.Message("")
	output.Table(
		[]string{"NAME", "VERSION", "DISK IMAGE", "ENVIRONMENT ID", "UPDATED", "ACR IMAGE"},
		rows,
	)
	output.Message("")
	return nil
}

func (a *showAction) resolveTarget() (showResult, error) {
	if strings.TrimSpace(a.environmentName) == "" {
		state, err := loadRleState()
		if err != nil {
			return showResult{}, err
		}
		if err := requireDeployedEnvironment(state); err != nil {
			return showResult{}, err
		}

		client, err := createRleClient(state.ProjectEndpoint)
		if err != nil {
			return showResult{}, err
		}

		environment, err := client.getEnvironmentVersion(a.cmd.Context(), state.Name, state.EnvironmentVersion)
		if err != nil {
			return showResult{}, serviceError(err)
		}
		versions, err := resolveEnvironmentVersions(a.cmd.Context(), client, environment)
		if err != nil {
			return showResult{}, serviceError(err)
		}
		return showResult{Environment: *environment, Versions: versions}, nil
	}

	projectEndpoint, err := resolveEnvironmentListProjectEndpoint()
	if err != nil {
		return showResult{}, err
	}
	client, err := createRleClient(projectEndpoint)
	if err != nil {
		return showResult{}, err
	}
	environment, err := resolveLatestEnvironmentByName(a.cmd.Context(), client, strings.TrimSpace(a.environmentName))
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
