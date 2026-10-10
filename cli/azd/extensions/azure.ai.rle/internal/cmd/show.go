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
	version         string
	versionSet      bool
}

func newShowCommand(outputFormat *string) *cobra.Command {
	var version string
	cmd := &cobra.Command{
		Use:   "show [environment-name]",
		Short: "Show RLE environment details",
		Long: `Show RLE environment details.

The command resolves the environment from the Foundry project and shows its
full version history, including telemetry identity when available. Use
--version to show one exact version and its telemetry identity instead.
With no environment name, it uses the name saved in .azd-rle.json.`,
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
				version:         version,
				versionSet:      cmd.Flags().Changed("version"),
			}).Run()
		},
	}
	cmd.Flags().StringVar(&version, "version", "", "Show one exact published environment version.")
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

	if a.versionSet && strings.TrimSpace(a.version) == "" {
		return &azdext.LocalError{
			Message:    "An exact environment version is required for --version.",
			Code:       "rle_environment_version_required",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Provide a semantic version, for example --version 2.1.0.",
		}
	}
	environmentName, client, err := a.resolveTarget()
	if err != nil {
		return err
	}

	if a.versionSet {
		version, err := client.getEnvironmentVersion(a.cmd.Context(), environmentName, a.version)
		if isRleNotFound(err) {
			return environmentVersionNotFoundError(environmentName, a.version)
		}
		if err != nil {
			return serviceError(err)
		}
		if output.IsJSON() {
			return output.JSON(version)
		}
		renderTableOrNoResults(output, []string{"FIELD", "VALUE"}, [][]string{
			{"Environment", version.Name},
			{"Version", version.Version},
			{"Disk image", version.DiskImageConversionStatus},
			{"Environment ID", version.Id},
			{"Updated", version.UpdatedAt},
			{"Run scope", telemetryRunScope(version.Telemetry)},
			{"Lime run ID", telemetryRunID(version.Telemetry)},
			{"Run ID format", telemetryRunIDFormat(version.Telemetry)},
		}, "")
		return nil
	}

	versions, err := listAllEnvironmentVersions(a.cmd.Context(), client, environmentName)
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
			telemetryRunScope(version.Telemetry),
			telemetryRunID(version.Telemetry),
		})
	}
	renderTableOrNoResults(output,
		[]string{"VERSION", "DISK IMAGE", "ENVIRONMENT ID", "UPDATED", "RUN SCOPE", "LIME RUN ID"},
		rows,
		noEnvironmentVersionsMessage,
	)
	return nil
}

func telemetryRunScope(telemetry *environmentTelemetry) string {
	if telemetry == nil || telemetry.RunScope == "" {
		return "Unavailable"
	}
	return telemetry.RunScope
}

func telemetryRunID(telemetry *environmentTelemetry) string {
	if telemetry == nil {
		return "Unavailable"
	}
	if telemetry.LimeRunID != "" {
		return telemetry.LimeRunID
	}
	switch {
	case strings.EqualFold(telemetry.RunScope, "Rollout"):
		return "Per rollout"
	case strings.EqualFold(telemetry.RunScope, "Disabled"):
		return "Disabled"
	default:
		return "Unavailable"
	}
}

func telemetryRunIDFormat(telemetry *environmentTelemetry) string {
	if telemetry == nil || telemetry.RunIDFormat == "" {
		return "Unavailable"
	}
	return telemetry.RunIDFormat
}

func (a *showAction) resolveTarget() (string, *rleClient, error) {
	environmentName := strings.TrimSpace(a.environmentName)
	projectEndpoint := ""
	if environmentName == "" {
		state, err := loadRleState()
		if err != nil {
			return "", nil, err
		}
		environmentName = strings.TrimSpace(state.EnvironmentName)
		if environmentName == "" {
			return "", nil, &azdext.LocalError{
				Message:    "The saved RLE environment does not include a name.",
				Code:       "rle_environment_name_missing",
				Category:   azdext.LocalErrorCategoryUser,
				Suggestion: "Provide an environment name: azd ai rle show <environment-name>.",
			}
		}
		projectEndpoint = strings.TrimSpace(state.ProjectEndpoint)
		if projectEndpoint == "" {
			return "", nil, &azdext.LocalError{
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
			return "", nil, err
		}
	}
	client, err := createRleClient(projectEndpoint)
	if err != nil {
		return "", nil, err
	}
	return environmentName, client, nil
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
