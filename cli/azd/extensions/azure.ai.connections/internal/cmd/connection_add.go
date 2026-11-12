// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"azure.ai.connections/internal/exterrors"
	"azure.ai.connections/internal/foundry"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type connectionAddRuntime struct {
	project       connectionAddProjectClient
	selectProject connectionProjectSelector
	close         func()
}

type connectionAddDependencies struct {
	newRuntime     func() (*connectionAddRuntime, error)
	projectDir     func() (string, error)
	loadDefinition connectionDefinitionLoader
}

type connectionAddJSONResult struct {
	Name                 string   `json:"name"`
	Host                 string   `json:"host"`
	Mutation             string   `json:"mutation"`
	Ref                  string   `json:"ref"`
	Project              string   `json:"project"`
	EnvironmentVariables []string `json:"environmentVariables"`
}

func newConnectionAddCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	return newConnectionAddCommandWithDependencies(extCtx, connectionAddDependencies{
		newRuntime:     newDefaultConnectionAddRuntime,
		projectDir:     connectionAuthoringProjectDir,
		loadDefinition: foundry.DecodeContainedDocument,
	})
}

func newConnectionAddCommandWithDependencies(
	extCtx *azdext.ExtensionContext,
	dependencies connectionAddDependencies,
) *cobra.Command {
	if extCtx == nil {
		extCtx = &azdext.ExtensionContext{}
	}
	if dependencies.newRuntime == nil {
		dependencies.newRuntime = newDefaultConnectionAddRuntime
	}
	if dependencies.projectDir == nil {
		dependencies.projectDir = connectionAuthoringProjectDir
	}
	if dependencies.loadDefinition == nil {
		dependencies.loadDefinition = foundry.DecodeContainedDocument
	}

	var filePath string
	var projectKey string
	cmd := &cobra.Command{
		Use:   "add <name> --file <path> [--project <service-key>]",
		Short: "Add a local Connection service to this azd project.",
		Long: "Author a local azure.ai.connection service in azure.yaml. " +
			"The definition remains in the supplied file. This command only edits the existing project; " +
			"it does not read or modify an azd environment, resolve values, or create or deploy remote resources.\n\n" +
			"For a new service, the sole Foundry Project is selected automatically. With multiple Projects, " +
			"use --project or select one interactively. Updates preserve a unique existing owner; use --project " +
			"to repair a missing or ambiguous owner. Dangling or malformed uses entries must be repaired manually.\n\n" +
			"The complete project mutation is validated before writing. Updates use separate Project API requests, " +
			"not a transaction. If a request fails, inspect the manifest and rerun to reconcile a possible partial update.",
		Example: `  # Add a Connection definition to an existing Project service
  azd ai connection add search --file connections/search.yaml

  # Select the Project service explicitly
  azd ai connection add search --file connections/search.yaml --project foundry`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("environment") {
				return exterrors.Validation(
					exterrors.CodeConflictingArguments,
					"--environment is not supported by local Connection authoring.",
					"Remove --environment; this command does not read or modify an azd environment.",
				)
			}
			if strings.TrimSpace(filePath) == "" {
				return exterrors.Validation(
					exterrors.CodeMissingConnectionField,
					"--file must name a non-blank Connection definition path.",
					"Provide an existing YAML or JSON file with --file.",
				)
			}
			if err := azdext.ValidateServiceName(args[0]); err != nil {
				return exterrors.Validation(
					exterrors.CodeInvalidParameter,
					err.Error(),
					"Use a valid azd service name and retry.",
				)
			}
			projectExplicit := cmd.Flags().Changed("project")
			if projectExplicit && strings.TrimSpace(projectKey) == "" {
				return exterrors.Validation(
					exterrors.CodeInvalidParameter,
					"--project must name an existing azure.ai.project service key.",
					"Provide a non-blank service key with --project.",
				)
			}

			runtime, err := dependencies.newRuntime()
			if err != nil {
				return fmt.Errorf("creating azd client for local Connection authoring: %w", err)
			}
			if runtime.close != nil {
				defer runtime.close()
			}

			projectRoot, err := dependencies.projectDir()
			if err != nil {
				return fmt.Errorf("local Connection authoring requires an existing azd project: %w", err)
			}
			ctx := azdext.WithAccessToken(cmd.Context())
			plan, err := planConnectionAdd(
				ctx,
				runtime.project,
				projectRoot,
				args[0],
				filePath,
				projectExplicit,
				projectKey,
				extCtx.NoPrompt,
				runtime.selectProject,
				dependencies.loadDefinition,
			)
			if err != nil {
				return err
			}
			if err := plan.apply(ctx, runtime.project); err != nil {
				return err
			}
			if extCtx.OutputFormat == "json" {
				return writeConnectionAddJSON(cmd.OutOrStdout(), plan)
			}
			return writeConnectionAddHuman(cmd.OutOrStdout(), plan)
		},
	}

	cmd.Flags().StringVar(
		&filePath,
		"file",
		"",
		"Path to a Connection YAML or JSON definition",
	)
	cmd.Flags().StringVar(
		&projectKey,
		"project",
		"",
		"Existing azure.ai.project service key to own this Connection",
	)
	_ = cmd.MarkFlagRequired("file")
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name: "output", AllowedValues: []string{"json", "table"}, Default: "table",
	})
	return cmd
}

func newDefaultConnectionAddRuntime() (*connectionAddRuntime, error) {
	client, err := azdext.NewAzdClient()
	if err != nil {
		return nil, err
	}

	return &connectionAddRuntime{
		project: client.Project(),
		selectProject: func(
			ctx context.Context,
			projectKeys []string,
		) (string, error) {
			choices := make([]*azdext.SelectChoice, len(projectKeys))
			for index, key := range projectKeys {
				choices[index] = &azdext.SelectChoice{Value: key, Label: key}
			}
			response, err := client.Prompt().Select(ctx, &azdext.SelectRequest{
				Options: &azdext.SelectOptions{
					Message:     "Select the Foundry Project service for this Connection",
					HelpMessage: "Use --project to select a Project service key explicitly.",
					Choices:     choices,
				},
			})
			if err != nil {
				return "", fmt.Errorf("selecting a Foundry Project service: %w", err)
			}
			if response == nil || response.Value == nil {
				return "", errors.New("Foundry Project selection was cancelled")
			}
			selectedIndex := int(*response.Value)
			if selectedIndex < 0 || selectedIndex >= len(projectKeys) {
				return "", errors.New("Foundry Project selection returned an invalid choice")
			}
			return projectKeys[selectedIndex], nil
		},
		close: client.Close,
	}, nil
}

func writeConnectionAddJSON(writer io.Writer, plan *connectionAddPlan) error {
	variables := plan.environmentVariables
	if variables == nil {
		variables = []string{}
	}
	result := connectionAddJSONResult{
		Name:                 plan.serviceName,
		Host:                 aiConnectionHost,
		Mutation:             plan.mutation,
		Ref:                  plan.ref,
		Project:              plan.projectKey,
		EnvironmentVariables: variables,
	}
	if err := json.NewEncoder(writer).Encode(result); err != nil {
		return fmt.Errorf("writing Connection authoring result: %w", err)
	}
	return nil
}

func writeConnectionAddHuman(writer io.Writer, plan *connectionAddPlan) error {
	var verb string
	switch plan.mutation {
	case "added":
		verb = "Added"
	case "updated":
		verb = "Updated"
	case "unchanged":
		verb = "Unchanged"
	default:
		return fmt.Errorf("unsupported Connection service mutation %q", plan.mutation)
	}

	if _, err := fmt.Fprintf(
		writer,
		"%s local Connection service %q for Foundry Project service %q.\n",
		verb,
		plan.serviceName,
		plan.projectKey,
	); err != nil {
		return fmt.Errorf("writing Connection authoring result: %w", err)
	}
	if _, err := fmt.Fprintf(writer, "Definition remains in %s.\n", plan.ref); err != nil {
		return fmt.Errorf("writing Connection authoring result: %w", err)
	}
	for _, variable := range plan.environmentVariables {
		if _, err := fmt.Fprintf(
			writer,
			"Required environment variable %s: azd env set %s <value>\n",
			variable,
			variable,
		); err != nil {
			return fmt.Errorf("writing Connection authoring result: %w", err)
		}
	}
	if _, err := fmt.Fprintln(
		writer,
		"Connection values are not resolved by this command; deployment validates them.",
	); err != nil {
		return fmt.Errorf("writing Connection authoring result: %w", err)
	}
	if _, err := fmt.Fprintf(
		writer,
		"Run `azd deploy %s` or `azd up` to apply this Connection.\n",
		plan.serviceName,
	); err != nil {
		return fmt.Errorf("writing Connection authoring result: %w", err)
	}
	return nil
}
