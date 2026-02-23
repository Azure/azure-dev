// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"time"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	inf "github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type downFlags struct {
	forceDelete bool
	purgeDelete bool
	global      *internal.GlobalCommandOptions
	internal.EnvFlag
}

func (i *downFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	local.BoolVar(&i.forceDelete, "force", false, "Does not require confirmation before it deletes resources.")
	local.BoolVar(
		&i.purgeDelete,
		"purge",
		false,
		//nolint:lll
		"Does not require confirmation before it permanently deletes resources that are soft-deleted by default (for example, key vaults).",
	)

	i.EnvFlag.Bind(local, global)
	i.global = global
}

func newDownFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *downFlags {
	flags := &downFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newDownCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "down [<layer>]",
		Short: "Delete your project's Azure resources.",
	}
	cmd.Args = cobra.MaximumNArgs(1)
	return cmd
}

type downAction struct {
	flags                *downFlags
	args                 []string
	lazyProvisionManager *lazy.Lazy[*provisioning.Manager]
	importManager        *project.ImportManager
	lazyEnv              *lazy.Lazy[*environment.Environment]
	envManager           environment.Manager
	console              input.Console
	projectConfig        *project.ProjectConfig
	alphaFeatureManager  *alpha.FeatureManager
}

func newDownAction(
	args []string,
	flags *downFlags,
	lazyProvisionManager *lazy.Lazy[*provisioning.Manager],
	lazyEnv *lazy.Lazy[*environment.Environment],
	envManager environment.Manager,
	projectConfig *project.ProjectConfig,
	console input.Console,
	alphaFeatureManager *alpha.FeatureManager,
	importManager *project.ImportManager,
) actions.Action {
	return &downAction{
		flags:                flags,
		lazyProvisionManager: lazyProvisionManager,
		lazyEnv:              lazyEnv,
		envManager:           envManager,
		console:              console,
		projectConfig:        projectConfig,
		importManager:        importManager,
		alphaFeatureManager:  alphaFeatureManager,
		args:                 args,
	}
}

func (a *downAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	// Command title
	a.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title:     "Deleting all resources and deployed code on Azure (azd down)",
		TitleNote: "Local application code is not deleted when running 'azd down'.",
	})

	startTime := time.Now()

	// Check if there are any environments before proceeding
	envList, err := a.envManager.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing environments: %w", err)
	}
	if len(envList) == 0 {
		return nil, errors.New("no environments found. Run \"azd init\" or \"azd env new\" to create one")
	}

	// Get the environment non-interactively (respects -e flag or default environment)
	env, err := a.lazyEnv.GetValue()
	if err != nil {
		return nil, err
	}

	// Get the provisioning manager (resolved lazily to avoid premature env prompts)
	provisionManager, err := a.lazyProvisionManager.GetValue()
	if err != nil {
		return nil, fmt.Errorf("getting provisioning manager: %w", err)
	}

	infra, err := a.importManager.ProjectInfrastructure(ctx, a.projectConfig)
	if err != nil {
		return nil, err
	}
	defer func() { _ = infra.Cleanup() }()

	if a.alphaFeatureManager.IsEnabled(azapi.FeatureDeploymentStacks) {
		a.console.WarnForFeature(ctx, azapi.FeatureDeploymentStacks)
	}

	downLayer := ""
	if len(a.args) > 0 {
		downLayer = a.args[0]
	}

	layers := infra.Options.GetLayers()
	if downLayer != "" {
		layerOpt, err := infra.Options.GetLayer(downLayer)
		if err != nil {
			return nil, err
		}
		layers = []provisioning.Options{layerOpt}
	}
	slices.Reverse(layers)

	for _, layer := range layers {
		if downLayer != "" || len(layers) > 1 {
			a.console.EnsureBlankLine(ctx)
			a.console.Message(ctx, fmt.Sprintf("Layer: %s", output.WithHighLightFormat(layer.Name)))
			a.console.Message(ctx, "")
		}

		layer.Mode = provisioning.ModeDestroy
		if err := provisionManager.Initialize(ctx, a.projectConfig.Path, layer); err != nil {
			return nil, fmt.Errorf("initializing provisioning manager: %w", err)
		}

		destroyOptions := provisioning.NewDestroyOptions(a.flags.forceDelete, a.flags.purgeDelete)
		_, err := provisionManager.Destroy(ctx, destroyOptions)
		if errors.Is(err, inf.ErrDeploymentsNotFound) || errors.Is(err, inf.ErrDeploymentResourcesNotFound) {
			a.console.MessageUxItem(ctx, &ux.DoneMessage{Message: "No Azure resources were found."})
		} else if err != nil {
			return nil, fmt.Errorf("deleting infrastructure: %w", err)
		}
	}

	// Invalidate cache after successful down so azd show will refresh
	if err := a.envManager.InvalidateEnvCache(ctx, env.Name()); err != nil {
		log.Printf("warning: failed to invalidate state cache: %v", err)
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: fmt.Sprintf("Your application was removed from Azure in %s.", ux.DurationAsText(since(startTime))),
		},
	}, nil
}

func getCmdDownHelpDescription(*cobra.Command) string {
	return generateCmdHelpDescription(fmt.Sprintf(
		"Delete Azure resources for an application. Running %s will not delete application"+
			" files on your local machine.", output.WithHighLightFormat("azd down")), []string{
		"When <layer> is specified, only deletes resources for the given layer." +
			" When omitted, deletes resources for all layers defined in the project.",
	})
}

func getCmdDownHelpFooter(*cobra.Command) string {
	return generateCmdHelpSamplesBlock(map[string]string{
		"Delete all resources for an application." +
			" You will be prompted to confirm your decision.": output.WithHighLightFormat("azd down"),
		"Forcibly delete all applications resources without confirmation.": output.WithHighLightFormat("azd down --force"),
		"Permanently delete resources that are soft-deleted by default," +
			" without confirmation.": output.WithHighLightFormat("azd down --purge"),
	})
}
