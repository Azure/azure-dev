// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"

	"azure.resources/internal/add"
	"azure.resources/internal/environment"
	"azure.resources/internal/resources"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func newAddCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	var layerName string
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a resource or service to a resources infrastructure layer.",
		Long: "Interactively configure a resource or service and save changes to azure.yaml.\n" +
			"Resources are stored in the selected infra.layers entry's config, never top-level resources.\n" +
			"A summary is displayed after saving.\n" +
			"Existing layers require confirmation before they are updated or replaced.\n" +
			"Only one resources infrastructure entry is supported.\n" +
			"The layer prompt defaults to " + defaultResourcesLayer + ".",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cmd.Flags().Changed("layer") {
				if err := resources.ValidateEntryName(layerName); err != nil || layerName == "" {
					return errors.New("--layer requires a nonempty infrastructure name starting with a lowercase letter")
				}
			}
			if extCtx.NoPrompt {
				return errors.New("azd resources add requires interactive input; run without --no-prompt")
			}
			projectDir, err := azdext.GetProjectDir()
			if err != nil {
				return err
			}
			client, err := azdext.NewAzdClient()
			if err != nil {
				return err
			}
			defer client.Close()
			ctx := cmd.Context()
			current, err := loadProject(ctx, client.Project())
			if err != nil {
				return err
			}
			// Reject incompatible project shapes before asking for input or Azure context.
			initialLayer := defaultResourcesLayer
			if cmd.Flags().Changed("layer") {
				initialLayer = layerName
			}
			if _, err := parseProject(current, initialLayer); err != nil {
				return err
			}
			if !cmd.Flags().Changed("layer") {
				answer, err := client.Prompt().Prompt(ctx, &azdext.PromptRequest{Options: &azdext.PromptOptions{
					Message: "Enter the infrastructure layer name", DefaultValue: defaultResourcesLayer, Required: true,
				}})
				if err != nil {
					return fmt.Errorf("selecting infrastructure layer: %w", err)
				}
				layerName = answer.GetValue()
			}
			project, err := parseProject(current, layerName)
			if err != nil {
				return err
			}
			if err := resources.ValidateProjectLayers(current, layerName); err != nil {
				return err
			}
			if project.target != nil {
				message := fmt.Sprintf("Add resources to existing layer %q?", layerName)
				if project.provider != resources.ProviderName {
					message = fmt.Sprintf("Replace infrastructure layer %q (provider %q) with a resources layer?",
						layerName, project.provider)
				}
				accepted, err := confirmAdd(ctx, client, message, false)
				if err != nil || !accepted {
					return err
				}
			}
			wizard := &add.Wizard{
				Client: client, Writer: cmd.OutOrStdout(), ProjectDir: projectDir,
				Resources: project.config, Services: project.services, EntryName: layerName,
				Environment: extCtx.Environment,
			}
			result, err := wizard.Run(ctx)
			if err != nil {
				return err
			}
			layer, err := project.layerChange(layerName, result)
			if err != nil {
				return err
			}
			services, err := project.serviceChanges(result)
			if err != nil {
				return err
			}
			// Resolve the environment before touching azure.yaml so a missing environment
			// can't leave the project saved without its resource values.
			envName := extCtx.Environment
			if len(result.Environment) > 0 {
				envName, err = environment.ResolveName(ctx, client.Environment(), extCtx.Environment)
				if err != nil {
					return fmt.Errorf("getting the environment: %w", err)
				}
			}

			if _, err := client.Project().SetConfigValue(ctx, layer); err != nil {
				return fmt.Errorf("saving infrastructure layer %q: %w", layerName, err)
			}
			if err := saveAddServices(ctx, client.Project(), current, services); err != nil {
				return err
			}
			if len(result.Environment) > 0 {
				for _, key := range slices.Sorted(maps.Keys(result.Environment)) {
					_, err := client.Environment().SetValue(ctx, &azdext.SetEnvRequest{
						EnvName: envName, Key: key, Value: result.Environment[key],
					})
					if err != nil {
						return fmt.Errorf("azure.yaml saved, but saving environment value %s failed: %w", key, err)
					}
				}
			}
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), project.summary(layerName, result)); err != nil {
				return fmt.Errorf("displaying project changes: %w", err)
			}
			return followUpAdd(ctx, client, result.HasService, layerName, envName, cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&layerName, "layer", "",
		"Infrastructure layer name (prompt default: "+defaultResourcesLayer+")")
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{Name: "output", AllowedValues: []string{"default"}})
	return cmd
}

func confirmAdd(ctx context.Context, client *azdext.AzdClient, message string, defaultValue bool) (bool, error) {
	response, err := client.Prompt().Confirm(ctx, &azdext.ConfirmRequest{Options: &azdext.ConfirmOptions{
		Message: message, DefaultValue: new(defaultValue),
	}})
	if err != nil {
		return false, err
	}
	if response.Value == nil {
		return false, errors.New("confirmation returned no answer")
	}
	return response.GetValue(), nil
}

func followUpAdd(
	ctx context.Context, client *azdext.AzdClient, hasService bool, layerName, environment string, writer io.Writer,
) error {
	verb := "provision"
	if hasService {
		verb = "provision and deploy the project"
	}
	response, err := client.Prompt().Select(ctx, &azdext.SelectRequest{Options: &azdext.SelectOptions{
		Message: fmt.Sprintf("Do you want to %s these changes?", verb),
		Choices: []*azdext.SelectChoice{
			{Value: "preview", Label: "Yes (preview changes)"},
			{Value: "now", Label: "Yes"},
			{Value: "later", Label: "No"},
		},
	}})
	if err != nil {
		return err
	}
	if response.Value == nil || response.GetValue() < 0 || response.GetValue() > 2 {
		return errors.New("provisioning selection returned an invalid answer")
	}
	run := func(args []string) error {
		if environment != "" {
			args = append(args, "--environment", environment)
		}
		_, err := client.Workflow().Run(ctx, &azdext.RunWorkflowRequest{Workflow: &azdext.Workflow{
			Name: "resources-add", Steps: []*azdext.WorkflowStep{{Command: &azdext.WorkflowCommand{Args: args}}},
		}})
		return err
	}
	if response.GetValue() == 2 {
		command := "azd provision " + layerName
		if hasService {
			command = "azd up"
		}
		if environment != "" {
			command += fmt.Sprintf(" --environment %q", environment)
		}
		_, err := fmt.Fprintf(writer, "Run '%s' to apply these changes later.\n", command)
		return err
	}
	if response.GetValue() == 0 {
		if err := run([]string{"provision", layerName, "--preview"}); err != nil {
			return err
		}
		accepted, err := confirmAdd(ctx, client, fmt.Sprintf("Proceed to %s these changes to Azure?", verb), true)
		if err != nil || !accepted {
			return err
		}
	}
	if hasService {
		return run([]string{"up"})
	}
	return run([]string{"provision", layerName})
}
