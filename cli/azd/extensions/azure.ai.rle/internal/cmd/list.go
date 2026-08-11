// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

const (
	environmentListPageSize = 200
	environmentListMaxPages = 100
)

type listAction struct {
	cmd          *cobra.Command
	outputFormat *string
}

func newListCommand(outputFormat *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List RLE environments in the Foundry project",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return (&listAction{cmd: cmd, outputFormat: outputFormat}).Run()
		},
	}
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name:          "output",
		AllowedValues: []string{"default", "json"},
	})
	return cmd
}

func (a *listAction) Run() error {
	projectEndpoint, err := resolveEnvironmentListProjectEndpoint()
	if err != nil {
		return err
	}

	client, err := createRleClient(projectEndpoint)
	if err != nil {
		return err
	}
	environments, err := listAllEnvironments(a.cmd.Context(), client)
	if err != nil {
		return err
	}

	format, err := azdext.ParseOutputFormat(*a.outputFormat)
	if err != nil {
		return err
	}
	output := azdext.NewOutput(azdext.OutputOptions{
		Format:    format,
		Writer:    a.cmd.OutOrStdout(),
		ErrWriter: a.cmd.ErrOrStderr(),
	})
	if output.IsJSON() {
		return output.JSON(environments)
	}
	if len(environments) == 0 {
		output.Message("No RLE environments found in this Foundry project.")
		return nil
	}

	rows := make([][]string, 0, len(environments))
	for _, environment := range environments {
		rows = append(rows, []string{
			environment.Name,
			environment.Version,
			environment.DiskImageConversionStatus,
			environment.Id,
			environment.UpdatedAt,
		})
	}
	output.Message("")
	output.Table(
		[]string{"NAME", "VERSION", "DISK IMAGE", "ENVIRONMENT ID", "UPDATED"},
		rows,
	)
	output.Message("")
	return nil
}

func listAllEnvironments(ctx context.Context, client *rleClient) ([]environmentResource, error) {
	var environments []environmentResource
	for pageNumber := range environmentListMaxPages {
		skip := pageNumber * environmentListPageSize
		page, err := client.listEnvironments(ctx, skip, environmentListPageSize)
		if err != nil {
			return nil, serviceError(err)
		}
		environments = append(environments, page.Value...)
		if len(page.Value) < environmentListPageSize {
			return environments, nil
		}
	}
	return nil, &azdext.LocalError{
		Message:  fmt.Sprintf("Environment list exceeded the %d-item safety limit.", environmentListPageSize*environmentListMaxPages),
		Code:     "rle_environment_list_safety_limit",
		Category: azdext.LocalErrorCategoryInternal,
	}
}

func resolveEnvironmentListProjectEndpoint() (string, error) {
	endpoint, err := resolveFoundryProjectEndpoint()
	if err != nil {
		return "", err
	}
	if endpoint != "" {
		return endpoint, nil
	}

	state, stateErr := loadRleState()
	if stateErr == nil && strings.TrimSpace(state.ProjectEndpoint) != "" {
		return state.ProjectEndpoint, nil
	}
	if stateErr != nil {
		var localErr *azdext.LocalError
		if !errors.As(stateErr, &localErr) || localErr.Code != "rle_project_not_initialized" {
			return "", stateErr
		}
	}

	return "", &azdext.LocalError{
		Message:  "Foundry project endpoint is required to list RLE environments.",
		Code:     "rle_project_required",
		Category: azdext.LocalErrorCategoryUser,
		Suggestion: fmt.Sprintf(
			"Set %s=https://<account>.services.ai.azure.com/api/projects/<project>, "+
				"or run this command from a deployed RLE environment folder.",
			foundryProjectEndpointEnvVar,
		),
	}
}
