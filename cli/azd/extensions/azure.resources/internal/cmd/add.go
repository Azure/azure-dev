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
			"After saving, the command shows a summary and the command to apply the changes.\n" +
			"Only one resources infrastructure entry is supported.\n" +
			"Without --layer, use the existing resources layer or create " + defaultResourcesLayer + ".\n" +
			"Replacing a layer that uses another provider requires confirmation.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
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
			return runAddWizard(cmd.Context(), client, cmd.OutOrStdout(), projectDir, extCtx.Environment, layerName, "")
		},
	}
	cmd.Flags().StringVar(&layerName, "layer", "",
		"Infrastructure layer name (defaults to the existing resources layer or "+defaultResourcesLayer+")")
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{Name: "output", AllowedValues: []string{"default"}})
	return cmd
}

func runAddWizard(
	ctx context.Context, client *azdext.AzdClient, writer io.Writer, projectDir, envName, layerName, itemType string,
) error {
	current, err := loadProject(ctx, client.Project())
	if err != nil {
		return err
	}
	project, err := parseProject(current, layerName)
	if err != nil {
		return err
	}
	layerName = project.layerName
	if err := resources.ValidateProjectLayers(current, layerName); err != nil {
		return err
	}
	if project.target != nil && project.provider != resources.ProviderName {
		message := fmt.Sprintf("Replace infrastructure layer %q (provider %q) with a resources layer?",
			layerName, project.provider)
		accepted, err := confirmAdd(ctx, client, message, false)
		if err != nil || !accepted {
			return err
		}
	}
	wizard := &add.Wizard{
		Client: client, Writer: writer, ProjectDir: projectDir,
		Resources: project.config, Services: project.services, EntryName: layerName,
		Environment: envName, ItemType: itemType,
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
	if len(result.Environment) > 0 {
		envName, err = environment.ResolveName(ctx, client.Environment(), envName)
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
	for _, key := range slices.Sorted(maps.Keys(result.Environment)) {
		_, err := client.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: envName, Key: key, Value: result.Environment[key],
		})
		if err != nil {
			return fmt.Errorf("azure.yaml saved, but saving environment value %s failed: %w", key, err)
		}
	}
	if _, err := fmt.Fprintln(writer, project.summary(layerName, result)); err != nil {
		return fmt.Errorf("displaying project changes: %w", err)
	}
	command := "azd provision " + layerName
	if result.HasService {
		command = "azd up"
	}
	if envName != "" {
		command += fmt.Sprintf(" --environment %q", envName)
	}
	_, err = fmt.Fprintf(writer, "Run '%s' to apply these changes.\n", command)
	return err
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
