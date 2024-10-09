package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type ProvisionFlags struct {
	all                   bool
	platform              bool
	noProgress            bool
	preview               bool
	ignoreDeploymentState bool
	global                *internal.GlobalCommandOptions
	*internal.EnvFlag
}

func (i *ProvisionFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	i.BindNonCommon(local, global)
	i.bindCommon(local, global)
}

func (i *ProvisionFlags) BindNonCommon(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	local.BoolVar(&i.noProgress, "no-progress", false, "Suppresses progress information.")
	//deprecate:Flag hide --no-progress
	_ = local.MarkHidden("no-progress")
	i.global = global
}

func (i *ProvisionFlags) bindCommon(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	local.BoolVar(&i.preview, "preview", false, "Preview changes to Azure resources.")
	local.BoolVar(
		&i.ignoreDeploymentState,
		"no-state",
		false,
		"Do not use latest Deployment State (bicep only).")
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

	i.EnvFlag = &internal.EnvFlag{}
	i.EnvFlag.Bind(local, global)
}

func (i *ProvisionFlags) SetCommon(envFlag *internal.EnvFlag) {
	i.EnvFlag = envFlag
}

func NewProvisionFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *ProvisionFlags {
	flags := &ProvisionFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func NewProvisionFlagsFromEnvAndOptions(envFlag *internal.EnvFlag, global *internal.GlobalCommandOptions) *ProvisionFlags {
	flags := &ProvisionFlags{
		EnvFlag: envFlag,
		global:  global,
	}

	return flags
}

func NewProvisionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "provision [<service>]",
		Short: "Provision the Azure resources for an application.",
		Args:  cobra.MaximumNArgs(1),
	}
}

type ProvisionAction struct {
	args                []string
	flags               *ProvisionFlags
	provisionManager    *provisioning.Manager
	projectManager      project.ProjectManager
	resourceManager     project.ResourceManager
	env                 *environment.Environment
	envManager          environment.Manager
	formatter           output.Formatter
	projectConfig       *project.ProjectConfig
	writer              io.Writer
	console             input.Console
	subManager          *account.SubscriptionsManager
	importManager       *project.ImportManager
	alphaFeatureManager *alpha.FeatureManager
	portalUrlBase       string
}

func NewProvisionAction(
	args []string,
	flags *ProvisionFlags,
	provisionManager *provisioning.Manager,
	projectManager project.ProjectManager,
	importManager *project.ImportManager,
	resourceManager project.ResourceManager,
	projectConfig *project.ProjectConfig,
	env *environment.Environment,
	envManager environment.Manager,
	console input.Console,
	formatter output.Formatter,
	writer io.Writer,
	subManager *account.SubscriptionsManager,
	alphaFeatureManager *alpha.FeatureManager,
	cloud *cloud.Cloud,
) actions.Action {
	return &ProvisionAction{
		args:                args,
		flags:               flags,
		provisionManager:    provisionManager,
		projectManager:      projectManager,
		resourceManager:     resourceManager,
		env:                 env,
		envManager:          envManager,
		formatter:           formatter,
		projectConfig:       projectConfig,
		writer:              writer,
		console:             console,
		subManager:          subManager,
		importManager:       importManager,
		alphaFeatureManager: alphaFeatureManager,
		portalUrlBase:       cloud.PortalUrlBase,
	}
}

// SetFlags sets the flags for the provision action. Panics if `flags` is nil
func (p *ProvisionAction) SetFlags(flags *ProvisionFlags) {
	if flags == nil {
		panic("flags is nil")
	}

	p.flags = flags
}

func (p *ProvisionAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	var targetServiceName string
	if len(p.args) == 1 {
		targetServiceName = strings.TrimSpace(p.args[0])
	}

	if targetServiceName != "" && p.flags.all {
		return nil, fmt.Errorf("cannot specify both --all and <service>")
	}

	if targetServiceName != "" && p.flags.platform {
		return nil, fmt.Errorf("cannot specify both --platform and <service>")
	}

	if p.flags.platform && p.flags.all {
		return nil, fmt.Errorf("cannot specify both --platform and --all")
	}

	// Command title
	defaultTitle := "Provisioning Azure resources (azd provision)"
	defaultTitleNote := "Provisioning Azure resources can take some time"
	if p.flags.preview {
		defaultTitle = "Previewing Azure resource changes (azd provision --preview)"
		defaultTitleNote = "This is a preview. No changes will be applied to your Azure resources."
	}

	p.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title:     defaultTitle,
		TitleNote: defaultTitleNote},
	)

	if p.alphaFeatureManager.IsEnabled(azapi.FeatureDeploymentStacks) {
		p.console.WarnForFeature(ctx, azapi.FeatureDeploymentStacks)
	}

	startTime := time.Now()

	if err := p.projectManager.Initialize(ctx, p.projectConfig); err != nil {
		return nil, err
	}

	// Get Subscription to Display in Command Title Note
	// Subscription and Location are ONLY displayed when they are available (found from env), otherwise, this message
	// is not displayed.
	// This needs to happen after the provisionManager initializes to make sure the env is ready for the provisioning
	// provider
	subscription, subErr := p.subManager.GetSubscription(ctx, p.env.GetSubscriptionId())
	if subErr == nil {
		location, err := p.subManager.GetLocation(ctx, p.env.GetSubscriptionId(), p.env.GetLocation())
		var locationDisplay string
		if err != nil {
			log.Printf("failed getting location: %v", err)
		} else {
			locationDisplay = location.DisplayName
		}

		var subscriptionDisplay string
		if v, err := strconv.ParseBool(os.Getenv("AZD_DEMO_MODE")); err == nil && v {
			subscriptionDisplay = subscription.Name
		} else {
			subscriptionDisplay = fmt.Sprintf("%s (%s)", subscription.Name, subscription.Id)
		}

		p.console.MessageUxItem(ctx, &ux.EnvironmentDetails{
			Subscription: subscriptionDisplay,
			Location:     locationDisplay,
		})

	} else {
		log.Printf("failed getting subscriptions. Skip displaying sub and location: %v", subErr)
	}

	// Provision root platform infrastructure
	_, _, err := p.provisionPlatform(ctx, targetServiceName)
	if err != nil {
		return nil, err
	}

	// Provision services infrastructure
	_, _, err = p.provisionServices(ctx, targetServiceName)
	if err != nil {
		return nil, err
	}

	// if err != nil {
	// 	if p.formatter.Kind() == output.JsonFormat {
	// 		stateResult, err := p.provisionManager.State(ctx, nil)
	// 		if err != nil {
	// 			return nil, fmt.Errorf(
	// 				"deployment failed and the deployment result is unavailable: %w",
	// 				multierr.Combine(err, err),
	// 			)
	// 		}

	// 		if err := p.formatter.Format(
	// 			provisioning.NewEnvRefreshResultFromState(stateResult.State), p.writer, nil); err != nil {
	// 			return nil, fmt.Errorf(
	// 				"deployment failed and the deployment result could not be displayed: %w",
	// 				multierr.Combine(err, err),
	// 			)
	// 		}
	// 	}

	// if previewMode {
	// 	p.console.MessageUxItem(ctx, deployResultToUx(deployPreviewResult))

	// 	return &actions.ActionResult{
	// 		Message: &actions.ResultMessage{
	// 			Header: fmt.Sprintf(
	// 				"Generated provisioning preview in %s.", ux.DurationAsText(since(startTime))),
	// 			FollowUp: getResourceGroupFollowUp(
	// 				ctx,
	// 				p.formatter,
	// 				p.portalUrlBase,
	// 				p.projectConfig,
	// 				p.resourceManager,
	// 				p.env,
	// 				true,
	// 			),
	// 		},
	// 	}, nil
	// }

	// if deployResult.SkippedReason == provisioning.DeploymentStateSkipped {
	// 	return &actions.ActionResult{
	// 		Message: &actions.ResultMessage{
	// 			Header: "There are no changes to provision for your application.",
	// 		},
	// 	}, nil
	// }

	// servicesStable, err := p.importManager.ServiceStable(ctx, p.projectConfig)
	// if err != nil {
	// 	return nil, err
	// }

	// for _, svc := range servicesStable {
	// 	eventArgs := project.ServiceLifecycleEventArgs{
	// 		Project: p.projectConfig,
	// 		Service: svc,
	// 		Args: map[string]any{
	// 			"bicepOutput": deployResult.Deployment.Outputs,
	// 		},
	// 	}

	// 	if err := svc.RaiseEvent(ctx, project.ServiceEventEnvUpdated, eventArgs); err != nil {
	// 		return nil, err
	// 	}
	// }

	// if p.formatter.Kind() == output.JsonFormat {
	// 	stateResult, err := p.provisionManager.State(ctx, nil)
	// 	if err != nil {
	// 		return nil, fmt.Errorf(
	// 			"deployment succeeded but the deployment result is unavailable: %w",
	// 			multierr.Combine(err, err),
	// 		)
	// 	}

	// 	if err := p.formatter.Format(
	// 		provisioning.NewEnvRefreshResultFromState(stateResult.State), p.writer, nil); err != nil {
	// 		return nil, fmt.Errorf(
	// 			"deployment succeeded but the deployment result could not be displayed: %w",
	// 			multierr.Combine(err, err),
	// 		)
	// 	}
	// }

	if p.flags.preview {
		return &actions.ActionResult{
			Message: &actions.ResultMessage{
				Header: fmt.Sprintf(
					"Generated provisioning preview in %s.", ux.DurationAsText(since(startTime))),
				FollowUp: getResourceGroupFollowUp(
					ctx,
					p.formatter,
					p.portalUrlBase,
					p.projectConfig,
					p.resourceManager,
					p.env,
					true,
				),
			},
		}, nil
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: fmt.Sprintf(
				"Your application was provisioned in Azure in %s.", ux.DurationAsText(since(startTime))),
			FollowUp: getResourceGroupFollowUp(
				ctx,
				p.formatter,
				p.portalUrlBase,
				p.projectConfig,
				p.resourceManager,
				p.env,
				false,
			),
		},
	}, nil
}

func (p *ProvisionAction) provisionPlatform(
	ctx context.Context,
	targetServiceName string,
) (*provisioning.DeployResult, *provisioning.DeployPreviewResult, error) {
	infra, err := p.importManager.ProjectInfrastructure(ctx, p.projectConfig)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = infra.Cleanup() }()

	stepMessage := "Initializing"
	p.console.Message(ctx, output.WithBold("Provisioning platform infrastructure"))
	p.console.ShowSpinner(ctx, stepMessage, input.Step)

	if targetServiceName != "" {
		p.console.StopSpinner(ctx, "Platform not selected", input.StepSkipped)
		return nil, nil, nil
	}

	infraOptions := infra.Options
	infraOptions.IgnoreDeploymentState = p.flags.ignoreDeploymentState

	if err := p.provisionManager.Initialize(ctx, p.projectConfig.Path, infraOptions); err != nil {
		p.console.ShowSpinner(ctx, stepMessage, input.Step)

		if errors.Is(err, os.ErrNotExist) {
			p.console.StopSpinner(ctx, "No infrastructure found", input.StepSkipped)
			return nil, nil, nil
		} else {
			p.console.StopSpinner(ctx, "Provisioning Infrastructure", input.StepFailed)
			return nil, nil, fmt.Errorf("initializing provisioning manager: %w", err)
		}
	}

	projectEventArgs := project.ProjectLifecycleEventArgs{
		Project: p.projectConfig,
		Args: map[string]any{
			"preview": p.flags.preview,
		},
	}

	var deployResult *provisioning.DeployResult
	var previewResult *provisioning.DeployPreviewResult

	err = p.projectConfig.Invoke(ctx, project.ProjectEventProvision, projectEventArgs, func() error {
		if p.flags.preview {
			previewResult, err = p.provisionManager.Preview(ctx)
			if err == nil {
				p.console.MessageUxItem(ctx, deployResultToUx(previewResult))
			}
		} else {
			deployResult, err = p.provisionManager.Deploy(ctx)
		}
		return err
	})

	if err != nil {
		p.console.StopSpinner(ctx, stepMessage, input.StepFailed)
		return nil, nil, err
	}

	p.console.StopSpinner(ctx, stepMessage, input.StepDone)

	return deployResult, previewResult, nil
}

func (p *ProvisionAction) provisionServices(
	ctx context.Context,
	targetServiceName string,
) (map[string]*provisioning.DeployResult, map[string]*provisioning.DeployPreviewResult, error) {
	stableServices, err := p.importManager.ServiceStable(ctx, p.projectConfig)
	if err != nil {
		return nil, nil, err
	}

	targetServiceName, err = getTargetServiceName(
		ctx,
		p.projectManager,
		p.importManager,
		p.projectConfig,
		string(project.ServiceEventProvision),
		targetServiceName,
		p.flags.all,
	)
	if err != nil {
		return nil, nil, err
	}

	deployResults := map[string]*provisioning.DeployResult{}
	previewResults := map[string]*provisioning.DeployPreviewResult{}

	for _, svc := range stableServices {
		p.console.Message(
			ctx,
			fmt.Sprintf("%s %s %s",
				output.WithBold("Provisioning infrastructure for"),
				output.WithHighLightFormat(svc.Name),
				output.WithBold("service"),
			),
		)

		stepMessage := "Initializing"
		p.console.ShowSpinner(ctx, stepMessage, input.Step)

		if p.flags.platform {
			p.console.StopSpinner(ctx, stepMessage, input.StepSkipped)
			return nil, nil, nil
		}

		// Skip this service if both cases are true:
		// 1. The user specified a service name
		// 2. This service is not the one the user specified
		if targetServiceName != "" && targetServiceName != svc.Name {
			p.console.StopSpinner(ctx, "Service not selected", input.StepSkipped)
			continue
		}

		infraOptions := svc.Infra
		infraOptions.IgnoreDeploymentState = p.flags.ignoreDeploymentState
		if infraOptions.Name == "" {
			infraOptions.Name = fmt.Sprintf("%s-%s", p.env.Name(), svc.Name)
		}

		if err := p.provisionManager.Initialize(ctx, svc.Path(), infraOptions); err != nil {
			p.console.ShowSpinner(ctx, stepMessage, input.Step)

			if errors.Is(err, os.ErrNotExist) {
				p.console.StopSpinner(ctx, "No infrastructure found", input.StepSkipped)
				continue
			} else {
				p.console.StopSpinner(ctx, stepMessage, input.StepFailed)
				return nil, nil, err
			}
		}

		serviceEventArgs := project.ServiceLifecycleEventArgs{
			Project: p.projectConfig,
			Service: svc,
			Args: map[string]any{
				"preview": p.flags.preview,
			},
		}

		err = svc.Invoke(ctx, project.ServiceEventProvision, serviceEventArgs, func() error {
			if p.flags.preview {
				previewResult, err := p.provisionManager.Preview(ctx)
				if err != nil {
					return err
				}

				p.console.MessageUxItem(ctx, deployResultToUx(previewResult))

				previewResults[svc.Name] = previewResult
			} else {
				deployResult, err := p.provisionManager.Deploy(ctx)
				if err != nil {
					return err
				}

				deployResults[svc.Name] = deployResult
			}

			return nil
		})

		if err != nil {
			p.console.StopSpinner(ctx, stepMessage, input.StepFailed)
			return nil, nil, err
		}

		p.console.StopSpinner(ctx, stepMessage, input.StepDone)
	}

	return deployResults, previewResults, nil
}

// deployResultToUx creates the ux element to display from a provision preview
func deployResultToUx(previewResult *provisioning.DeployPreviewResult) ux.UxItem {
	var operations []*ux.Resource
	for _, change := range previewResult.Preview.Properties.Changes {
		operations = append(operations, &ux.Resource{
			Operation: ux.OperationType(change.ChangeType),
			Type:      change.ResourceType,
			Name:      change.Name,
		})
	}
	return &ux.PreviewProvision{
		Operations: operations,
	}
}

func GetCmdProvisionHelpDescription(c *cobra.Command) string {
	return generateCmdHelpDescription(fmt.Sprintf(
		"Provision the Azure resources for an application."+
			" This step may take a while depending on the resources provisioned."+
			" You should run %s any time you update your Bicep or Terraform file."+
			"\n\nThis command prompts you to input the following:",
		output.WithHighLightFormat(c.CommandPath())), []string{
		formatHelpNote("Azure location: The Azure location where your resources will be deployed."),
		formatHelpNote("Azure subscription: The Azure subscription where your resources will be deployed."),
	})
}

func GetCmdProvisionHelpFooter(*cobra.Command) string {
	return generateCmdHelpSamplesBlock(map[string]string{
		"Provision all resources for an application.": output.WithHighLightFormat("azd provision"),
		//nolint:lll
		"Provision all resources for a specific service within the application. ": output.WithHighLightFormat("azd provision <service>"),
		//nolint:lll
		"Provision the root platform infrastructure for the application. ": output.WithHighLightFormat("azd provision --platform"),
	})
}
