package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
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
	return &cobra.Command{
		Use:   "down",
		Short: "Delete Azure resources for an application.",
	}
}

type downAction struct {
	flags            *downFlags
	provisionManager *provisioning.Manager
	importManager    *project.ImportManager
	env              *environment.Environment
	console          input.Console
	projectConfig    *project.ProjectConfig
}

func newDownAction(
	flags *downFlags,
	provisionManager *provisioning.Manager,
	env *environment.Environment,
	projectConfig *project.ProjectConfig,
	console input.Console,
	alphaFeatureManager *alpha.FeatureManager,
	importManager *project.ImportManager,
) actions.Action {
	return &downAction{
		flags:            flags,
		provisionManager: provisionManager,
		env:              env,
		console:          console,
		projectConfig:    projectConfig,
		importManager:    importManager,
	}
}

func (a *downAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	// Command title
	a.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title:     "Deleting all resources and deployed code on Azure (azd down)",
		TitleNote: "Local application code is not deleted when running 'azd down'.",
	})

	startTime := time.Now()

	infra, err := a.importManager.ProjectInfrastructure(ctx, a.projectConfig)
	if err != nil {
		return nil, err
	}
	defer func() { _ = infra.Cleanup() }()

	if err := a.provisionManager.Initialize(ctx, a.projectConfig.Path, infra.Options); err != nil {
		return nil, fmt.Errorf("initializing provisioning manager: %w", err)
	}

	destroyOptions := provisioning.NewDestroyOptions(a.flags.forceDelete, a.flags.purgeDelete)
	if _, err := a.provisionManager.Destroy(ctx, destroyOptions); err != nil {
		return nil, fmt.Errorf("deleting infrastructure: %w", err)
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
			" files on your local machine.", output.WithHighLightFormat("azd down")), nil)
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
