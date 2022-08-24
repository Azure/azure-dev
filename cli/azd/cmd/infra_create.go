package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"go.uber.org/multierr"
)

type infraCreateAction struct {
	noProgress  bool
	rootOptions *internal.GlobalCommandOptions
}

func infraCreateCmd(rootOptions *internal.GlobalCommandOptions) *cobra.Command {
	cmd := commands.Build(
		&infraCreateAction{
			rootOptions: rootOptions,
		},
		rootOptions,
		"create",
		"Create Azure resources for an application.",
		"",
	)

	cmd.Aliases = []string{"provision"}
	return cmd
}

func (ica *infraCreateAction) SetupFlags(persis, local *pflag.FlagSet) {
	local.BoolVar(&ica.noProgress, "no-progress", false, "Suppresses progress information.")
}

func (ica *infraCreateAction) Run(ctx context.Context, cmd *cobra.Command, args []string, azdCtx *azdcontext.AzdContext) error {
	azCli := azcli.GetAzCli(ctx)
	console := input.GetConsole(ctx)

	if err := ensureProject(azdCtx.ProjectPath()); err != nil {
		return err
	}

	if err := tools.EnsureInstalled(ctx, azCli); err != nil {
		return err
	}

	if err := ensureLoggedIn(ctx); err != nil {
		return fmt.Errorf("failed to ensure login: %w", err)
	}

	env, err := loadOrInitEnvironment(ctx, &ica.rootOptions.EnvironmentName, azdCtx, console)
	if err != nil {
		return fmt.Errorf("loading environment: %w", err)
	}

	prj, err := project.LoadProjectConfig(azdCtx.ProjectPath(), &environment.Environment{})
	if err != nil {
		return fmt.Errorf("loading project: %w", err)
	}

	if err = prj.Initialize(ctx, &env); err != nil {
		return err
	}

	formatter := output.GetFormatter(ctx)

	if strings.TrimSpace(prj.Infra.Module) == "" {
		prj.Infra.Module = "main"
	}

	infraManager, err := provisioning.NewManager(ctx, env, prj.Path, prj.Infra, !ica.rootOptions.NoPrompt)
	if err != nil {
		return fmt.Errorf("creating provisioning manager: %w", err)
	}

	previewResult, err := infraManager.Preview(ctx)
	if err != nil {
		return fmt.Errorf("previewing deployment: %w", err)
	}

	provisioningScope := provisioning.NewSubscriptionScope(ctx, env.GetLocation(), env.GetSubscriptionId(), env.GetEnvName())
	deployResult, err := infraManager.Deploy(ctx, &previewResult.Deployment, provisioningScope)
	if err != nil {
		return fmt.Errorf("deploying infrastructure: %w", err)
	}

	if err != nil {
		if formatter.Kind() == output.JsonFormat {
			deploy, deployErr := azCli.GetSubscriptionDeployment(ctx, env.GetSubscriptionId(), env.GetEnvName())
			if deployErr != nil {
				return fmt.Errorf("deployment failed and the deployment result is unavailable: %w", multierr.Combine(err, deployErr))
			}

			if fmtErr := formatter.Format(deploy, cmd.OutOrStdout(), nil); fmtErr != nil {
				return fmt.Errorf("deployment failed and the deployment result could not be displayed: %w", multierr.Combine(err, fmtErr))
			}
		}

		return fmt.Errorf("deployment failed: %w", err)
	}

	if err := provisioning.UpdateEnvironment(&env, &deployResult.Deployment.Outputs); err != nil {
		return fmt.Errorf("updating environment with deployment outputs: %w", err)
	}

	for _, svc := range prj.Services {
		if err := svc.RaiseEvent(ctx, project.Deployed, map[string]any{"bicepOutput": deployResult.Deployment.Outputs}); err != nil {
			return err
		}
	}

	if formatter.Kind() == output.JsonFormat {
		if err = formatter.Format(deployResult.Operations, cmd.OutOrStdout(), nil); err != nil {
			return fmt.Errorf("deployment result could not be displayed: %w", err)
		}
	}

	return nil
}
