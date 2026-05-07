// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk/storage"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type ProvisionFlags struct {
	noProgress            bool
	preview               bool
	ignoreDeploymentState bool
	subscription          string
	location              string
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
	local.StringVar(
		&i.subscription,
		"subscription",
		"",
		"ID of an Azure subscription to use for the new environment",
	)
	local.StringVarP(&i.location, "location", "l", "", "Azure location for the new environment")
	i.global = global
}

// Subscription returns the value of the --subscription flag.
func (i *ProvisionFlags) Subscription() string {
	return i.subscription
}

// Location returns the value of the --location flag.
func (i *ProvisionFlags) Location() string {
	return i.location
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
	cmd := &cobra.Command{
		Use:   "provision [<layer>]",
		Short: "Provision Azure resources for your project.",
	}
	cmd.Args = cobra.MaximumNArgs(1)

	return cmd
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
	commandRunner       exec.CommandRunner
	serviceLocator      ioc.ServiceLocator
	subManager          *account.SubscriptionsManager
	importManager       *project.ImportManager
	alphaFeatureManager *alpha.FeatureManager
	portalUrlBase       string
	defaultProvider     provisioning.DefaultProviderResolver
	fileShareService    storage.FileShareService
	cloud               *cloud.Cloud

	// Graph-shared state (lazily initialized via graphOnce). Used by the
	// multi-layer provisioning graph path to share a thread-safe console
	// wrapper and mutexes across concurrent layer steps.
	graphOnce        sync.Once
	graphSyncConsole *syncConsole
	graphEnvMu       *sync.Mutex
	graphHookMu      *sync.Mutex
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
	commandRunner exec.CommandRunner,
	serviceLocator ioc.ServiceLocator,
	formatter output.Formatter,
	writer io.Writer,
	subManager *account.SubscriptionsManager,
	alphaFeatureManager *alpha.FeatureManager,
	cloud *cloud.Cloud,
	defaultProvider provisioning.DefaultProviderResolver,
	fileShareService storage.FileShareService,
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
		commandRunner:       commandRunner,
		serviceLocator:      serviceLocator,
		subManager:          subManager,
		importManager:       importManager,
		alphaFeatureManager: alphaFeatureManager,
		portalUrlBase:       cloud.PortalUrlBase,
		defaultProvider:     defaultProvider,
		fileShareService:    fileShareService,
		cloud:               cloud,
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
		return nil, err
	}

	if err := p.projectManager.EnsureAllTools(ctx, p.projectConfig, nil); err != nil {
		return nil, err
	}

	// Apply --subscription and --location flags to the environment before provisioning
	envChanged := false
	if p.flags.subscription != "" {
		if existing := p.env.GetSubscriptionId(); existing != "" && existing != p.flags.subscription {
			return nil, &internal.ErrorWithSuggestion{
				Err: fmt.Errorf(
					"environment '%s' (current: %s, requested: %s): %w",
					p.env.Name(), existing, p.flags.subscription, internal.ErrCannotChangeSubscription),
				Suggestion: "Run 'azd env new <name>' to create a new environment with a different subscription.",
			}
		}
		p.env.SetSubscriptionId(p.flags.subscription)
		envChanged = true
	}
	if p.flags.location != "" {
		if existing := p.env.GetLocation(); existing != "" && existing != p.flags.location {
			return nil, &internal.ErrorWithSuggestion{
				Err: fmt.Errorf(
					"environment '%s' (current: %s, requested: %s): %w",
					p.env.Name(), existing, p.flags.location, internal.ErrCannotChangeLocation),
				Suggestion: "Run 'azd env new <name>' to create a new environment with a different location.",
			}
		}
		p.env.SetLocation(p.flags.location)
		envChanged = true
	}
	if envChanged {
		if err := p.envManager.Save(ctx, p.env); err != nil {
			return nil, fmt.Errorf("saving environment: %w", err)
		}
	}

	infra, err := p.importManager.ProjectInfrastructure(ctx, p.projectConfig)
	if err != nil {
		return nil, err
	}
	defer func() { _ = infra.Cleanup() }()

	layer := ""
	if len(p.args) > 0 {
		layer = p.args[0]
	}

	layers := infra.Options.GetLayers()
	if layer != "" {
		layerOption, err := infra.Options.GetLayer(layer)
		if err != nil {
			return nil, err
		}

		layers = []provisioning.Options{layerOption}
	}

	if previewMode && len(layers) > 1 {
		return nil, &internal.ErrorWithSuggestion{
			Err:        internal.ErrPreviewMultipleLayers,
			Suggestion: "Run 'azd provision --preview <layer-name>' targeting a single layer.",
		}
	}

	// Unified entry point: provisionLayersGraph dispatches to the right
	// path (zero-layer info message, preview direct call, single-layer
	// one-node graph, or multi-layer N-node graph) and owns the shared
	// UX — environment details banner, JSON state dumps, and OpenAI /
	// Responsible AI error wrappers.
	return p.provisionLayersGraph(ctx, layers, startTime, previewMode)
}

// deployResultToUx creates the ux element to display from a provision preview
func deployResultToUx(previewResult *provisioning.DeployPreviewResult) ux.UxItem {
	var operations []*ux.Resource
	for _, change := range previewResult.Preview.Properties.Changes {
		// Convert property deltas to UX format
		var propertyDeltas []ux.PropertyDelta
		for _, delta := range change.Delta {
			propertyDeltas = append(propertyDeltas, ux.PropertyDelta{
				Path:       delta.Path,
				ChangeType: string(delta.ChangeType),
				Before:     delta.Before,
				After:      delta.After,
			})
		}

		operations = append(operations, &ux.Resource{
			Operation:      ux.OperationType(change.ChangeType),
			Type:           change.ResourceType,
			Name:           change.Name,
			PropertyDeltas: propertyDeltas,
		})
	}
	return &ux.PreviewProvision{
		Operations: operations,
	}
}

func GetCmdProvisionHelpDescription(c *cobra.Command) string {
	return generateCmdHelpDescription(
		fmt.Sprintf(
			"Provision the Azure resources for an application."+
				" This step may take a while depending on the resources provisioned."+
				" You should run %s any time you update your Bicep or Terraform file."+
				"\n\nThis command prompts you to input the following:",
			output.WithHighLightFormat(c.CommandPath())), []string{
			formatHelpNote("Azure location: The Azure location where your resources will be deployed."),
			formatHelpNote("Azure subscription: The Azure subscription where your resources will be deployed."),
			fmt.Sprintf("\nYou can also set these values in advance to skip the prompts:\n\n"+
				"  %s\n  %s\n\n"+
				"Use %s to configure values in the active environment."+
				" Use %s for structured output suitable for automation.",
				output.WithGrayFormat("azd env set AZURE_SUBSCRIPTION_ID <your-subscription-id>"),
				output.WithGrayFormat("azd env set AZURE_LOCATION <location>"),
				output.WithHighLightFormat("azd env set"),
				output.WithHighLightFormat("--output json"),
			),
			fmt.Sprintf("\nWhen <layer> is specified, only provisions resources for the given layer." +
				" When omitted, provisions resources for all layers defined in the project."),
		})
}
