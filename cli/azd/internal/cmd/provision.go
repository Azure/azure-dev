// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
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
	"github.com/azure/azure-dev/cli/azd/pkg/agentRunner"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/llm"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"go.uber.org/multierr"
)

type ProvisionFlags struct {
	noProgress            bool
	preview               bool
	ignoreDeploymentState bool
	global                *internal.GlobalCommandOptions
	*internal.EnvFlag
}

const (
	AINotValid                  = "is not valid according to the validation procedure"
	openAIsubscriptionNoQuotaId = "The subscription does not have QuotaId/Feature required by SKU 'S0' " +
		"from kind 'OpenAI'"
	responsibleAITerms              = "until you agree to Responsible AI terms for this resource"
	specialFeatureOrQuotaIdRequired = "SpecialFeatureOrQuotaIdRequired"
)

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
		"(Bicep only) Forces a fresh deployment based on current Bicep template files, "+
			"ignoring any stored deployment state.")

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
		Use:   "provision",
		Short: "Provision Azure resources for your project.",
	}
}

type ProvisionAction struct {
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
	llmManager          llm.Manager
}

func NewProvisionAction(
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
	llmManager llm.Manager,
) actions.Action {
	return &ProvisionAction{
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
		llmManager:          llmManager,
	}
}

// SetFlags sets the flags for the provision action. Panics if `flags` is nil
func (p *ProvisionAction) SetFlags(flags *ProvisionFlags) {
	if flags == nil {
		panic("flags is nil")
	}

	p.flags = flags
}

func (p *ProvisionAction) errorWithSuggestion(ctx context.Context, originalError error) error {
	// Show preview of the error
	previewWriter := p.console.ShowPreviewer(ctx,
		&input.ShowPreviewerOptions{
			Prefix:       "  ",
			MaxLineCount: 20,
			Title:        "Error Preview",
		})
	fmt.Fprintf(previewWriter, "%s", originalError.Error())

	// Ask user if they want to get error suggestions from AI
	selection, err := p.console.Select(ctx, input.ConsoleOptions{
		Message: "Do you want to get error suggestions from AI?",
		Options: []string{
			"Yes",
			"No",
		},
	})

	p.console.StopPreviewer(ctx, false)

	if err != nil {
		return fmt.Errorf("prompting failed to get error suggestions: %w", err)
	}

	switch selection {
	case 0: // get error suggestion
		// it takes around 30-60s
		p.console.MessageUxItem(ctx, &ux.MessageTitle{
			Title:     "Getting AI error suggestions",
			TitleNote: "Getting AI error suggestions can take some time",
		})
		
		result, errSampling := agentRunner.Run(ctx, p.console, p.llmManager, originalError)
		// If llm/sampling fails, we still want to return the original error
		if errSampling != nil {
			fmt.Printf("Not able to get AI error suggestions: %s\n", errSampling)
			return originalError
		}

		return &internal.ErrorWithSuggestion{Err: originalError, Suggestion: result}
	case 1: // don't get error suggestion
		return originalError
	}

	return originalError
}

func (p *ProvisionAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	if p.flags.noProgress {
		fmt.Fprintln(
			p.console.Handles().Stderr,
			//nolint:Lll
			output.WithWarningFormat(
				"WARNING: The '--no-progress' flag is deprecated and will be removed in a future release.",
			),
		)
	}
	previewMode := p.flags.preview

	// Command title
	defaultTitle := "Provisioning Azure resources (azd provision)"
	defaultTitleNote := "Provisioning Azure resources can take some time"
	if previewMode {
		defaultTitle = "Previewing Azure resource changes (azd provision --preview)"
		defaultTitleNote = "This is a preview. No changes will be applied to your Azure resources."
	}

	p.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title:     defaultTitle,
		TitleNote: defaultTitleNote},
	)

	startTime := time.Now()

	if err := p.projectManager.Initialize(ctx, p.projectConfig); err != nil {
		return nil, p.errorWithSuggestion(ctx, err)
	}

	if err := p.projectManager.EnsureAllTools(ctx, p.projectConfig, nil); err != nil {
		return nil, p.errorWithSuggestion(ctx, err)
	}

	infra, err := p.importManager.ProjectInfrastructure(ctx, p.projectConfig)
	if err != nil {
		return nil, p.errorWithSuggestion(ctx, err)
	}
	defer func() { _ = infra.Cleanup() }()

	infraOptions := infra.Options
	infraOptions.IgnoreDeploymentState = p.flags.ignoreDeploymentState
	if err := p.provisionManager.Initialize(ctx, p.projectConfig.Path, infraOptions); err != nil {
		return nil, p.errorWithSuggestion(ctx, fmt.Errorf("initializing provisioning manager: %w", err))
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

	var deployResult *provisioning.DeployResult
	var deployPreviewResult *provisioning.DeployPreviewResult

	projectEventArgs := project.ProjectLifecycleEventArgs{
		Project: p.projectConfig,
		Args: map[string]any{
			"preview": previewMode,
		},
	}

	if p.alphaFeatureManager.IsEnabled(azapi.FeatureDeploymentStacks) {
		p.console.WarnForFeature(ctx, azapi.FeatureDeploymentStacks)
	}

	err = p.projectConfig.Invoke(ctx, project.ProjectEventProvision, projectEventArgs, func() error {
		var err error
		if previewMode {
			deployPreviewResult, err = p.provisionManager.Preview(ctx)
		} else {
			deployResult, err = p.provisionManager.Deploy(ctx)
		}
		return err
	})

	if err != nil {
		if p.formatter.Kind() == output.JsonFormat {
			stateResult, err := p.provisionManager.State(ctx, nil)
			if err != nil {
				return nil, p.errorWithSuggestion(ctx, fmt.Errorf(
					"deployment failed and the deployment result is unavailable: %w",
					multierr.Combine(err, err),
				))
			}

			if err := p.formatter.Format(
				provisioning.NewEnvRefreshResultFromState(stateResult.State), p.writer, nil); err != nil {
				return nil, p.errorWithSuggestion(ctx, fmt.Errorf(
					"deployment failed and the deployment result could not be displayed: %w",
					multierr.Combine(err, err),
				))
			}
		}

		//if user don't have access to openai
		errorMsg := err.Error()
		if strings.Contains(errorMsg, specialFeatureOrQuotaIdRequired) && strings.Contains(errorMsg, "OpenAI") {
			requestAccessLink := "https://go.microsoft.com/fwlink/?linkid=2259205&clcid=0x409"
			return nil, &internal.ErrorWithSuggestion{
				Err: err,
				Suggestion: "\nSuggested Action: The selected subscription does not have access to" +
					" Azure OpenAI Services. Please visit " + output.WithLinkFormat("%s", requestAccessLink) +
					" to request access.",
			}
		}

		if strings.Contains(errorMsg, AINotValid) &&
			strings.Contains(errorMsg, openAIsubscriptionNoQuotaId) {
			return nil, &internal.ErrorWithSuggestion{
				Suggestion: "\nSuggested Action: The selected " +
					"subscription has not been enabled for use of Azure AI service and does not have quota for " +
					"any pricing tiers. Please visit " + output.WithLinkFormat("%s", p.portalUrlBase) +
					" and select 'Create' on specific services to request access.",
				Err: err,
			}
		}

		//if user haven't agree to Responsible AI terms
		if strings.Contains(errorMsg, responsibleAITerms) {
			return nil, &internal.ErrorWithSuggestion{
				Suggestion: "\nSuggested Action: Please visit azure portal in " +
					output.WithLinkFormat("%s", p.portalUrlBase) + ". Create the resource in azure portal " +
					"to go through Responsible AI terms, and then delete it. " +
					"After that, run 'azd provision' again",
				Err: err,
			}
		}

		return nil, p.errorWithSuggestion(ctx, fmt.Errorf("deployment failed: %w", err))

	}

	if previewMode {
		p.console.MessageUxItem(ctx, deployResultToUx(deployPreviewResult))

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

	if deployResult.SkippedReason == provisioning.DeploymentStateSkipped {
		return &actions.ActionResult{
			Message: &actions.ResultMessage{
				Header: "There are no changes to provision for your application.",
			},
		}, nil
	}

	servicesStable, err := p.importManager.ServiceStable(ctx, p.projectConfig)
	if err != nil {
		return nil, p.errorWithSuggestion(ctx, err)
	}

	for _, svc := range servicesStable {
		eventArgs := project.ServiceLifecycleEventArgs{
			Project: p.projectConfig,
			Service: svc,
			Args: map[string]any{
				"bicepOutput": deployResult.Deployment.Outputs,
			},
		}

		if err := svc.RaiseEvent(ctx, project.ServiceEventEnvUpdated, eventArgs); err != nil {
			return nil, p.errorWithSuggestion(ctx, err)
		}
	}

	if p.formatter.Kind() == output.JsonFormat {
		stateResult, err := p.provisionManager.State(ctx, nil)
		if err != nil {
			return nil, p.errorWithSuggestion(ctx, fmt.Errorf(
				"deployment succeeded but the deployment result is unavailable: %w",
				multierr.Combine(err, err),
			))
		}

		if err := p.formatter.Format(
			provisioning.NewEnvRefreshResultFromState(stateResult.State), p.writer, nil); err != nil {
			return nil, p.errorWithSuggestion(ctx, fmt.Errorf(
				"deployment succeeded but the deployment result could not be displayed: %w",
				multierr.Combine(err, err),
			))
		}
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
