package cmd

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/servicelinker/armservicelinker/v2"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/binding"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/keyvault"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func bindActions(root *actions.ActionDescriptor) {
	root.Add("binding", &actions.ActionDescriptorOptions{
		Command:        newBindingCmd(),
		FlagsResolver:  newBindingFlags,
		ActionResolver: newBindingAction,
		OutputFormats:  []output.Format{output.JsonFormat, output.NoneFormat},
		DefaultFormat:  output.NoneFormat,
		HelpOptions: actions.ActionHelpOptions{
			Description: getCmdBindingHelpDescription,
			Footer:      getCmdBindingHelpFooter,
		},
		GroupingOptions: actions.CommandGroupOptions{
			RootLevelHelp: actions.CmdGroupManage,
		},
	})
}

func newBindingCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "binding <service>",
		Short: "Create bindings for the service.",
	}
	cmd.Args = cobra.MaximumNArgs(1)

	return cmd
}

type bindingFlags struct {
	internal.EnvFlag
	global *internal.GlobalCommandOptions
}

func (f *bindingFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	f.EnvFlag.Bind(local, global)
	f.global = global
}

func newBindingFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *bindingFlags {
	flags := &bindingFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func getCmdBindingHelpFooter(*cobra.Command) string {
	return generateCmdHelpSamplesBlock(map[string]string{
		"Create bindings for all services in the current project.": output.WithHighLightFormat(
			"azd binding --all",
		),
		"Create bindings for the service named 'api'.": output.WithHighLightFormat(
			"azd binding api",
		),
	})
}

func getCmdBindingHelpDescription(*cobra.Command) string {
	return generateCmdHelpDescription(
		fmt.Sprintf("Manage your application bindings. %s", output.WithWarningFormat("(Beta)")),
		[]string{})
}

var serviceBindingFeature = alpha.MustFeatureKey("serviceBinding")

type bindingAction struct {
	flags           *envSetFlags
	args            []string
	projectConfig   *project.ProjectConfig
	env             *environment.Environment
	resourceManager project.ResourceManager
	bindingManager  binding.BindingManager
	console         input.Console
	alphaManager    *alpha.FeatureManager
	kvs             keyvault.KeyVaultService
}

func newBindingAction(
	flags *envSetFlags,
	args []string,
	projectConfig *project.ProjectConfig,
	env *environment.Environment,
	resourceManager project.ResourceManager,
	bindingManager binding.BindingManager,
	console input.Console,
	alphaManager *alpha.FeatureManager,
	kvs keyvault.KeyVaultService,
) actions.Action {
	return &bindingAction{
		flags:           flags,
		args:            args,
		projectConfig:   projectConfig,
		env:             env,
		resourceManager: resourceManager,
		bindingManager:  bindingManager,
		console:         console,
		alphaManager:    alphaManager,
		kvs:             kvs,
	}
}

func (b *bindingAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	if !b.alphaManager.IsEnabled(serviceBindingFeature) {
		return nil, fmt.Errorf(
			"service binding is currently under alpha support and must be explicitly enabled."+
				" Run `%s` to enable this feature.", alpha.GetEnableCommand(serviceBindingFeature),
		)
	}

	b.console.WarnForFeature(ctx, serviceBindingFeature)

	subscriptionId := b.env.GetSubscriptionId()
	if subscriptionId == "" {
		return nil, fmt.Errorf("infrastructure has not been provisioned. Run `azd provision`")
	}

	resourceGroupName, err := b.resourceManager.GetResourceGroupName(
		ctx, subscriptionId, b.projectConfig)
	if err != nil {
		return nil, err
	}

	// binding command title
	b.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title: "Binding services (azd binding)",
	})

	targetServiceName := ""
	if len(b.args) == 1 {
		targetServiceName = b.args[0]
	}

	// validate binding configs to fail earlier in case there are any user errors
	bindingCount := 0
	for svcName, svcConfig := range b.projectConfig.Services {
		if targetServiceName != "" && targetServiceName != svcName {
			continue
		}

		bindingSource, err := getBindingSource(svcName, *svcConfig)
		if err != nil {
			return nil, err
		}

		err = b.bindingManager.ValidateBindingConfigs(bindingSource, svcConfig.Bindings)
		if err != nil {
			return nil, err
		}

		bindingCount += len(svcConfig.Bindings)
	}

	// create bindings by services
	for svcName, svcConfig := range b.projectConfig.Services {
		stepMessage := fmt.Sprintf("Create bindings for service %s", svcName)
		b.console.ShowSpinner(ctx, stepMessage, input.Step)

		if targetServiceName != "" && targetServiceName != svcName {
			b.console.StopSpinner(ctx, stepMessage, input.StepSkipped)
			continue
		}

		// suppose no errors, as we checked the binding source above
		bindingSource, _ := getBindingSource(svcName, *svcConfig)

		err = b.bindingManager.CreateBindings(ctx, subscriptionId, resourceGroupName,
			bindingSource, svcConfig.Bindings)
		if err != nil {
			b.console.StopSpinner(ctx, stepMessage, input.StepFailed)
			return nil, err
		} else {
			b.console.StopSpinner(ctx, stepMessage, input.StepDone)
		}
	}

	header := fmt.Sprintf("%d bindings were created for your services.", bindingCount)
	if bindingCount == 1 {
		header = "1 binding was created for your service."
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: header,
			FollowUp: fmt.Sprintf("To view the bindings in Azure Portal: %s",
				getBindingsViewLink()),
		},
	}, nil
}

// Get binding source from service config
func getBindingSource(
	serviceName string,
	serviceConfig project.ServiceConfig,
) (*binding.BindingSource, error) {
	sourceType, err := convertServiceKindToSourceType(serviceConfig.Host)
	if err != nil {
		return nil, err
	}

	return &binding.BindingSource{
		SourceType:     sourceType,
		SourceResource: serviceName,
		ClientType:     convertLanguageKindToClientType(serviceConfig.Language),
	}, nil
}

// Converts the hosting service kind to the binding source type
func convertServiceKindToSourceType(
	kind project.ServiceTargetKind,
) (binding.SourceResourceType, error) {
	switch kind {
	case project.AppServiceTarget:
		return binding.SourceTypeWebApp, nil
	case project.AzureFunctionTarget:
		return binding.SourceTypeFunctionApp, nil
	case project.ContainerAppTarget,
		project.DotNetContainerAppTarget:
		return binding.SourceTypeContainerApp, nil
	case project.SpringAppTarget:
		return binding.SourceTypeSpringApp, nil
	default:
		return "", fmt.Errorf("binding is not supported for '%s'", kind)
	}
}

// Converts the language kind to the binding client type
func convertLanguageKindToClientType(
	kind project.ServiceLanguageKind,
) armservicelinker.ClientType {
	switch kind {
	case project.ServiceLanguageJava:
		return armservicelinker.ClientTypeJava
	case project.ServiceLanguageJavaScript:
	case project.ServiceLanguageTypeScript:
		return armservicelinker.ClientTypeNodejs
	case project.ServiceLanguageDotNet:
	case project.ServiceLanguageCsharp:
		return armservicelinker.ClientTypeDotnet
	case project.ServiceLanguagePython:
		return armservicelinker.ClientTypePython
	default:
		return armservicelinker.ClientTypeNone
	}
	return armservicelinker.ClientTypeNone
}

func getBindingsViewLink() string {
	return "https://ms.portal.azure.com/?servicelinkerextension=canary" +
		"#view/ServiceLinkerExtension/ServiceLinkerMenuBlade/~/overview"
}
