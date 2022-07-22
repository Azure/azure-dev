package cmd

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/AlecAivazis/survey/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/spin"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/theckman/yacspin"
	"go.uber.org/multierr"
)

type infraCreateAction struct {
	noProgress  bool
	rootOptions *commands.GlobalCommandOptions
}

func infraCreateCmd(rootOptions *commands.GlobalCommandOptions) *cobra.Command {
	cmd := commands.Build(
		&infraCreateAction{
			rootOptions: rootOptions,
		},
		rootOptions,
		"create",
		"Create Azure resources for an application",
		"",
	)

	cmd.Aliases = []string{"provision"}
	return cmd
}

func (ica *infraCreateAction) SetupFlags(persis, local *pflag.FlagSet) {
	local.BoolVar(&ica.noProgress, "no-progress", false, "Suppress progress information")
}

func (ica *infraCreateAction) Run(ctx context.Context, cmd *cobra.Command, args []string, azdCtx *environment.AzdContext) error {
	azCli := commands.GetAzCliFromContext(ctx)
	askOne := makeAskOne(ica.rootOptions.NoPrompt)

	if err := ensureProject(azdCtx.ProjectPath()); err != nil {
		return err
	}

	if err := ensureLoggedIn(ctx); err != nil {
		return fmt.Errorf("failed to ensure login: %w", err)
	}

	env, err := loadOrInitEnvironment(ctx, &ica.rootOptions.EnvironmentName, azdCtx, askOne)
	if err != nil {
		return fmt.Errorf("loading environment: %w", err)
	}

	projectConfig, err := project.LoadProjectConfig(azdCtx.ProjectPath(), &environment.Environment{})
	if err != nil {
		return fmt.Errorf("loading project: %w", err)
	}

	// Default module name to "main"
	if projectConfig.Infra.Module == "" {
		projectConfig.Infra.Module = "main"
	}

	infraProvider, err := provisioning.NewInfraProvider(&env, azdCtx.ProjectDirectory(), projectConfig.Infra, azCli)
	if err != nil {
		return fmt.Errorf("error creating infra provider: %w", err)
	}

	requiredTools := infraProvider.RequiredExternalTools()
	if err := tools.EnsureInstalled(ctx, requiredTools...); err != nil {
		return err
	}

	template, err := infraProvider.Plan(ctx)
	if err != nil {
		return fmt.Errorf("compiling infra template: %w", err)
	}

	// When creating a deployment, we need an azure location which is used to store the deployment metadata. This can be
	// any azure location and the choice doesn't impact what location individual resources in the deployment use. By default
	// we'll just use whatever value is being passed to the `location` parameter for bicep, and if that's not defined,
	// we'll prompt the user as to what location they want to use.
	//
	// TODO: The UX here could be improved. One problem is the concept of "the location used to store deployment metadata,
	// but not the resources" is sort of confusing and hard to clearly articulate.
	var location string

	if len(template.Parameters) > 0 {
		updatedParameters := false
		for key, param := range template.Parameters {
			// If this parameter has a default, then there is no need for us to configure it
			if param.HasDefaultValue() {
				continue
			}
			if !param.HasValue() {
				var val string
				if err := askOne(&survey.Input{
					Message: fmt.Sprintf("Please enter a value for the '%s' deployment parameter:", key),
				}, &val); err != nil {
					return fmt.Errorf("prompting for deployment parameter: %w", err)
				}

				param.Value = val

				saveParameter := true
				if err := askOne(&survey.Confirm{
					Message: "Save the value in the environment for future use",
				}, &saveParameter); err != nil {
					return fmt.Errorf("prompting to save deployment parameter: %w", err)
				}

				if saveParameter {
					env.Values[key] = val
				}

				updatedParameters = true
			}

			if key == "location" {
				location = fmt.Sprint(param.Value)
			}
		}

		if updatedParameters {
			if err := infraProvider.SaveTemplate(ctx, *template); err != nil {
				return fmt.Errorf("saving deployment parameters: %w", err)
			}

			if err := env.Save(); err != nil {
				return fmt.Errorf("writing env file: %w", err)
			}
		}
	}

	for location == "" {
		// TODO: We will want to store this information somewhere (so we don't have to prompt the
		// user on every deployment if they don't have a `location` parameter in their bicep file.
		// When we store it, we should store it /per environment/ not as a property of the entire
		// project.
		selected, err := promptLocation(ctx, "Please select an Azure location to use to store deployment metadata:", askOne)
		if err != nil {
			return fmt.Errorf("prompting for deployment metadata region: %w", err)
		}

		location = selected
	}

	formatter, err := output.GetFormatter(cmd)
	if err != nil {
		return err
	}
	interactive := formatter.Kind() == output.NoneFormat

	// Do the creating. The call to `DeployToSubscription` blocks until the deployment completes,
	// which can take a bit, so we typically do some progress indication.
	// For interactive use (default case, using table formatter), we use a spinner.
	// With JSON formatter we emit progress information, unless --no-progress option was set.
	var provisionResult *provisioning.ProvisionApplyResult

	deployAndReportProgress := func(showProgress func(string)) error {
		provisioningScope := provisioning.NewSubscriptionProvisioningScope(azCli, location, env.GetSubscriptionId(), env.GetEnvName())
		deployChannel, progressChannel := infraProvider.Apply(ctx, template, provisioningScope)

		go func() {
			for progressReport := range progressChannel {
				if interactive {
					reportDeploymentStatusInteractive(*progressReport, showProgress)
				} else {
					reportDeploymentStatusJson(*progressReport, formatter, cmd)
				}
			}
		}()

		provisionResult = <-deployChannel
		if provisionResult.Error != nil {
			return provisionResult.Error
		}

		return nil
	}

	if interactive {
		deploymentSlug := azure.SubscriptionDeploymentRID(env.GetSubscriptionId(), env.GetEnvName())
		deploymentURL := withLinkFormat(
			"https://portal.azure.com/#blade/HubsExtension/DeploymentDetailsBlade/overview/id/%s\n\n",
			url.PathEscape(deploymentSlug))
		printWithStyling(
			"Provisioning Azure resources can take some time.\n\nYou can view detailed progress in the Azure Portal:\n%s",
			deploymentURL)
		//fmt.Fprintf(colorable.NewColorableStdout(), "Provisioning Azure resources can take some time.\n\nYou can view detailed progress in the Azure Portal:\n%s", deploymentURL)

		err = spin.RunWithUpdater("Creating Azure resources ", deployAndReportProgress,
			func(s *yacspin.Spinner, deploySuccess bool) {
				s.StopMessage("Created Azure resources\n")
			})
	} else {
		err = deployAndReportProgress(nil)
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

	if err = provisioning.UpdateEnvironment(&env, &provisionResult.Outputs); err != nil {
		return err
	}

	if formatter.Kind() == output.JsonFormat {
		if err = formatter.Format(provisionResult, cmd.OutOrStdout(), nil); err != nil {
			return fmt.Errorf("deployment result could not be displayed: %w", err)
		}
	}

	return nil
}

func reportDeploymentStatusInteractive(progressReport provisioning.ProvisionApplyProgress, showProgress func(string)) {
	succeededCount := 0

	for _, resourceOperation := range progressReport.Operations {
		if resourceOperation.Properties.ProvisioningState == "Succeeded" {
			succeededCount++
		}
	}

	status := fmt.Sprintf("Creating Azure resources (%d of ~%d completed) ", succeededCount, len(progressReport.Operations))
	showProgress(status)
}

type progressReport struct {
	Timestamp  time.Time                      `json:"timestamp"`
	Operations []tools.AzCliResourceOperation `json:"operations"`
}

func reportDeploymentStatusJson(progressReport provisioning.ProvisionApplyProgress, formatter output.Formatter, cmd *cobra.Command) {
	_ = formatter.Format(progressReport, cmd.OutOrStdout(), nil)
}
