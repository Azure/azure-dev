package cmd

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/binding"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func bindActions(root *actions.ActionDescriptor) *actions.ActionDescriptor {
	group := root.Add("binding", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "binding",
			Short: "Manage service bindings.",
		},
		HelpOptions: actions.ActionHelpOptions{
			Description: getCmdBindingHelpDescription,
		},
		GroupingOptions: actions.CommandGroupOptions{
			RootLevelHelp: actions.CmdGroupManage,
		},
	})

	group.Add("create", &actions.ActionDescriptorOptions{
		Command:        newBindingCreateCmd(),
		FlagsResolver:  newBindingCreateFlags,
		ActionResolver: newBindingCreateAction,
	})

	group.Add("delete", &actions.ActionDescriptorOptions{
		Command:        newBindingDeleteCmd(),
		FlagsResolver:  newBindingDeleteFlags,
		ActionResolver: newBindingDeleteAction,
	})

	return group
}

func getCmdBindingHelpDescription(*cobra.Command) string {
	return generateCmdHelpDescription(
		"Manage your application bindings. With this command group, you can create, delete"+
			" or view your application bindings.",
		[]string{})
}

func newBindingCreateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "create",
		Short: "Create service bindings.",
		Args:  cobra.MaximumNArgs(1),
	}
}

func newBindingCreateFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *bindingCreateFlags {
	flags := &bindingCreateFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

type bindingCreateFlags struct {
	envFlag
	global *internal.GlobalCommandOptions
}

func (f *bindingCreateFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	f.envFlag.Bind(local, global)
	f.global = global
}

type bindingCreateAction struct {
	flags           *envSetFlags
	args            []string
	projectConfig   *project.ProjectConfig
	env             *environment.Environment
	resourceManager project.ResourceManager
	bindingManager  binding.BindingManager
	console         input.Console
}

func newBindingCreateAction(
	flags *envSetFlags,
	args []string,
	projectConfig *project.ProjectConfig,
	env *environment.Environment,
	resourceManager project.ResourceManager,
	bindingManager binding.BindingManager,
	console input.Console,
) actions.Action {
	return &bindingCreateAction{
		flags:           flags,
		args:            args,
		projectConfig:   projectConfig,
		env:             env,
		resourceManager: resourceManager,
		bindingManager:  bindingManager,
		console:         console,
	}
}

func (b *bindingCreateAction) Run(ctx context.Context) (*actions.ActionResult, error) {
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

	// validate binding configs to fail earlier in case there are any user errors
	bindingCount := 0
	for svcName, svcConfig := range b.projectConfig.Services {
		sourceType, err := convertServiceKindToSourceType(svcConfig.Host)
		if err != nil {
			return nil, err
		}
		b.bindingManager.ValidateBindingConfigs(sourceType, svcName, svcConfig.Bindings)
		bindingCount += len(svcConfig.Bindings)
	}

	// create bindings by services
	for svcName, svcConfig := range b.projectConfig.Services {
		stepMessage := fmt.Sprintf("Creating bindings for service %s", svcName)
		b.console.ShowSpinner(ctx, stepMessage, input.Step)

		// suppose no errors, as we have already validated the binding configs
		sourceType, _ := convertServiceKindToSourceType(svcConfig.Host)

		err = b.bindingManager.CreateBindings(ctx, subscriptionId, resourceGroupName,
			sourceType, svcName, svcConfig.Bindings)
		if err != nil {
			b.console.StopSpinner(ctx, stepMessage, input.StepFailed)
			return nil, err
		} else {
			b.console.StopSpinner(ctx, stepMessage, input.StepDone)
		}
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: fmt.Sprintf("%d bindings were created for your services.", bindingCount),
			FollowUp: fmt.Sprintf("To view the bindings in Azure Portal: %s",
				getBindingsViewLink(subscriptionId, resourceGroupName)),
		},
	}, nil
}

func newBindingDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete",
		Short: "Delete service bindings.",
		Args:  cobra.MaximumNArgs(1),
	}
}

func newBindingDeleteFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *bindingDeleteFlags {
	flags := &bindingDeleteFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

type bindingDeleteFlags struct {
	envFlag
	global *internal.GlobalCommandOptions
}

func (f *bindingDeleteFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	f.envFlag.Bind(local, global)
	f.global = global
}

type bindingDeleteAction struct {
	flags           *envSetFlags
	args            []string
	projectConfig   *project.ProjectConfig
	env             *environment.Environment
	resourceManager project.ResourceManager
	bindingManager  binding.BindingManager
	console         input.Console
}

func newBindingDeleteAction(
	flags *envSetFlags,
	args []string,
	projectConfig *project.ProjectConfig,
	env *environment.Environment,
	resourceManager project.ResourceManager,
	bindingManager binding.BindingManager,
	console input.Console,
) actions.Action {
	return &bindingDeleteAction{
		flags:           flags,
		args:            args,
		projectConfig:   projectConfig,
		env:             env,
		resourceManager: resourceManager,
		bindingManager:  bindingManager,
		console:         console,
	}
}

func (b *bindingDeleteAction) Run(ctx context.Context) (*actions.ActionResult, error) {
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

	// validate binding configs to fail earlier in case there are any user errors
	for svcName, svcConfig := range b.projectConfig.Services {
		sourceType, err := convertServiceKindToSourceType(svcConfig.Host)
		if err != nil {
			return nil, err
		}
		b.bindingManager.ValidateBindingConfigs(sourceType, svcName, svcConfig.Bindings)
	}

	// delete bindings by services
	for svcName, svcConfig := range b.projectConfig.Services {
		stepMessage := fmt.Sprintf("Deleting bindings for service %s", svcName)
		b.console.ShowSpinner(ctx, stepMessage, input.Step)

		// suppose no errors, as we have already validated the binding configs
		sourceType, _ := convertServiceKindToSourceType(svcConfig.Host)

		err = b.bindingManager.DeleteBindings(ctx, subscriptionId, resourceGroupName,
			sourceType, svcName, svcConfig.Bindings)
		if err != nil {
			b.console.StopSpinner(ctx, stepMessage, input.StepFailed)
			return nil, err
		} else {
			b.console.StopSpinner(ctx, stepMessage, input.StepDone)
		}
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: fmt.Sprintf("%d bindings were deleted for your services.", 15),
		},
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

func getBindingsViewLink(subscriptionId, resourceGroupName string) string {
	return fmt.Sprintf(
		"https://portal.azure.com/#@/resource/subscriptions/%s/resourceGroups/%s/overview",
		subscriptionId,
		resourceGroupName)
}
