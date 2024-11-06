package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type downFlags struct {
	all         bool
	platform    bool
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
	local.BoolVar(
		&i.all,
		"all",
		false,
		"Deploys all services that are listed in "+azdcontext.ProjectFileName,
	)
	local.BoolVar(
		&i.platform,
		"platform",
		false,
		"Deploys the root platform infrastructure",
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
		Use:   "down [<service>]",
		Short: "Delete Azure resources for an application.",
		Args:  cobra.MaximumNArgs(1),
	}
}

type downAction struct {
	args                []string
	flags               *downFlags
	projectManager      project.ProjectManager
	provisionManager    *provisioning.Manager
	importManager       *project.ImportManager
	env                 *environment.Environment
	console             input.Console
	projectConfig       *project.ProjectConfig
	alphaFeatureManager *alpha.FeatureManager
}

func newDownAction(
	args []string,
	flags *downFlags,
	projectManager project.ProjectManager,
	provisionManager *provisioning.Manager,
	env *environment.Environment,
	projectConfig *project.ProjectConfig,
	console input.Console,
	alphaFeatureManager *alpha.FeatureManager,
	importManager *project.ImportManager,
) actions.Action {
	return &downAction{
		args:                args,
		flags:               flags,
		projectManager:      projectManager,
		provisionManager:    provisionManager,
		env:                 env,
		console:             console,
		projectConfig:       projectConfig,
		importManager:       importManager,
		alphaFeatureManager: alphaFeatureManager,
	}
}

func (a *downAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	var targetServiceName string
	if len(a.args) == 1 {
		targetServiceName = strings.TrimSpace(a.args[0])
	}

	if targetServiceName != "" && a.flags.all {
		return nil, fmt.Errorf("cannot specify both --all and <service>")
	}

	if targetServiceName != "" && a.flags.platform {
		return nil, fmt.Errorf("cannot specify both --platform and <service>")
	}

	if a.flags.platform && a.flags.all {
		return nil, fmt.Errorf("cannot specify both --platform and --all")
	}

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

	if a.alphaFeatureManager.IsEnabled(azapi.FeatureDeploymentStacks) {
		a.console.WarnForFeature(ctx, azapi.FeatureDeploymentStacks)
	}

	destroyOptions := provisioning.NewDestroyOptions(a.flags.forceDelete, a.flags.purgeDelete)

	results := map[string]*provisioning.DestroyResult{}

	// Deprovision services infrastructure
	serviceResults, err := a.deprovisionServices(ctx, targetServiceName, destroyOptions)
	if err != nil {
		return nil, err
	}

	for svcName, result := range serviceResults {
		results[svcName] = result
	}

	// Deprovision root platform infrastructure
	platformResult, err := a.deprovisionPlatform(ctx, targetServiceName, destroyOptions)
	if err != nil {
		return nil, err
	}

	serviceResults["_"] = platformResult

	if len(results) == 0 {
		return nil, nil
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: fmt.Sprintf("Your application was removed from Azure in %s.", ux.DurationAsText(since(startTime))),
		},
	}, nil
}

func (a *downAction) deprovisionPlatform(
	ctx context.Context,
	targetServiceName string,
	destroyOptions provisioning.DestroyOptions,
) (*provisioning.DestroyResult, error) {
	projectInfra, err := a.importManager.ProjectInfrastructure(ctx, a.projectConfig)
	if err != nil {
		return nil, err
	}
	defer func() { _ = projectInfra.Cleanup() }()

	stepMessage := "Initializing"
	a.console.Message(ctx, output.WithBold("\nDeprovisioning platform infrastructure"))
	a.console.ShowSpinner(ctx, stepMessage, input.Step)

	if targetServiceName != "" {
		a.console.StopSpinner(ctx, "Platform not selected", input.StepSkipped)
		return nil, nil
	}

	infraOptions := projectInfra.Options

	if err := a.provisionManager.Initialize(ctx, a.projectConfig.Path, infraOptions); err != nil {
		a.console.ShowSpinner(ctx, stepMessage, input.Step)

		if errors.Is(err, os.ErrNotExist) {
			a.console.StopSpinner(ctx, "No infrastructure found", input.StepSkipped)
			return nil, nil
		} else {
			a.console.StopSpinner(ctx, "Deprovisioning Infrastructure", input.StepFailed)
			return nil, fmt.Errorf("initializing provisioning manager: %w", err)
		}
	}

	projectEventArgs := project.ProjectLifecycleEventArgs{
		Project: a.projectConfig,
	}

	var destroyResult *provisioning.DestroyResult

	err = a.projectConfig.Invoke(ctx, project.ProjectEventDown, projectEventArgs, func() error {
		result, err := a.provisionManager.Destroy(ctx, destroyOptions)
		if err != nil {
			if errors.Is(err, infra.ErrDeploymentsNotFound) {
				a.console.ShowSpinner(ctx, stepMessage, input.Step)
				a.console.StopSpinner(ctx, "No deployments found", input.StepSkipped)
				return nil
			}

			return err
		}

		destroyResult = result
		return nil
	})

	if err != nil {
		a.console.StopSpinner(ctx, stepMessage, input.StepFailed)
		return nil, err
	}

	a.console.StopSpinner(ctx, stepMessage, input.StepDone)

	return destroyResult, nil
}

func (a *downAction) deprovisionServices(
	ctx context.Context,
	targetServiceName string,
	destroyOptions provisioning.DestroyOptions,
) (map[string]*provisioning.DestroyResult, error) {
	stableServices, err := a.importManager.ServiceStable(ctx, a.projectConfig)
	if err != nil {
		return nil, err
	}

	targetServiceName, err = getTargetServiceName(
		ctx,
		a.projectManager,
		a.importManager,
		a.projectConfig,
		string(project.ServiceEventDown),
		targetServiceName,
		a.flags.all,
	)
	if err != nil {
		return nil, err
	}

	destroyResults := map[string]*provisioning.DestroyResult{}

	for index, svc := range stableServices {
		if index > 0 {
			a.console.Message(ctx, "")
		}

		a.console.Message(
			ctx,
			fmt.Sprintf("%s %s %s",
				output.WithBold("Provisioning infrastructure for"),
				output.WithHighLightFormat(svc.Name),
				output.WithBold("service"),
			),
		)

		stepMessage := "Initializing"
		a.console.ShowSpinner(ctx, stepMessage, input.Step)

		if a.flags.platform {
			a.console.StopSpinner(ctx, stepMessage, input.StepSkipped)
			return nil, nil
		}

		// Skip this service if both cases are true:
		// 1. The user specified a service name
		// 2. This service is not the one the user specified
		if targetServiceName != "" && targetServiceName != svc.Name {
			a.console.StopSpinner(ctx, "Service not selected", input.StepSkipped)
			continue
		}

		infraOptions := svc.Infra
		if infraOptions.Name == "" {
			infraOptions.Name = fmt.Sprintf("%s-%s", a.env.Name(), svc.Name)
		}

		if err := a.provisionManager.Initialize(ctx, svc.Path(), infraOptions); err != nil {
			a.console.ShowSpinner(ctx, stepMessage, input.Step)

			if errors.Is(err, os.ErrNotExist) {
				a.console.StopSpinner(ctx, "No infrastructure found", input.StepSkipped)
				continue
			} else {
				a.console.StopSpinner(ctx, stepMessage, input.StepFailed)
				return nil, err
			}
		}

		serviceEventArgs := project.ServiceLifecycleEventArgs{
			Project: a.projectConfig,
			Service: svc,
		}

		err = svc.Invoke(ctx, project.ServiceEventProvision, serviceEventArgs, func() error {
			destroyResult, err := a.provisionManager.Destroy(ctx, destroyOptions)
			if err != nil {
				if errors.Is(err, infra.ErrDeploymentsNotFound) {
					a.console.ShowSpinner(ctx, stepMessage, input.Step)
					a.console.StopSpinner(ctx, "No deployments found", input.StepSkipped)
					return nil
				}

				return err
			}

			destroyResults[svc.Name] = destroyResult

			return nil
		})

		if err != nil {
			a.console.StopSpinner(ctx, stepMessage, input.StepFailed)
			return nil, err
		}

		a.console.StopSpinner(ctx, stepMessage, input.StepDone)
	}

	return destroyResults, nil
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
		//nolint:lll
		"Delete all resources for a specific service within the application. ": output.WithHighLightFormat("azd down <service>"),
		//nolint:lll
		"Delete the root platform infrastructure for the application. ":    output.WithHighLightFormat("azd down --platform"),
		"Forcibly delete all applications resources without confirmation.": output.WithHighLightFormat("azd down --force"),
		"Permanently delete resources that are soft-deleted by default," +
			" without confirmation.": output.WithHighLightFormat("azd down --purge"),
	})
}
