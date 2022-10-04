package cmd

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func accountCmd(rootOptions *internal.GlobalCommandOptions) *cobra.Command {
	root := &cobra.Command{
		Use:   "account",
		Short: "Manages and sets default Azure account settings.",
	}

	root.AddCommand(output.AddOutputParam(
		accountListCmd(rootOptions),
		[]output.Format{output.TableFormat, output.JsonFormat},
		output.TableFormat,
	))

	root.AddCommand(output.AddOutputParam(
		locationListCmd(rootOptions),
		[]output.Format{output.TableFormat, output.JsonFormat},
		output.TableFormat,
	))
	root.AddCommand(output.AddOutputParam(accountShowCmd(rootOptions),
		[]output.Format{output.JsonFormat},
		output.JsonFormat,
	))

	root.AddCommand(accountSetSubscriptionCmd(rootOptions))
	root.AddCommand(accountSetLocationCmd(rootOptions))

	root.Flags().BoolP("help", "h", false, fmt.Sprintf("Gets help for %s.", root.Name()))

	return root
}

func accountListCmd(rootOptions *internal.GlobalCommandOptions) *cobra.Command {
	action := commands.ActionFunc(func(ctx context.Context, cmd *cobra.Command, args []string, azdCtx *azdcontext.AzdContext) error {
		formatter := output.GetFormatter(ctx)
		writer := output.GetDefaultWriter()
		manager := account.NewManager(ctx)

		subscriptions, err := manager.GetSubscriptions(ctx)
		if err != nil {
			return err
		}

		switch formatter.Kind() {
		case output.JsonFormat:
			err = formatter.Format(subscriptions, writer, nil)
		case output.TableFormat:
			tableOptions := output.TableFormatterOptions{
				Columns: []output.Column{
					{
						Heading:       "ID",
						ValueTemplate: "{{.Id}}",
					},
					{
						Heading:       "Name",
						ValueTemplate: "{{.Name}}",
					},
				},
			}

			err = formatter.Format(subscriptions, writer, tableOptions)
		}

		if err != nil {
			return fmt.Errorf("failed formatting output")
		}

		return nil
	})

	return commands.Build(action, rootOptions, "list", "Gets the available Azure subscriptions for the logged in account.", nil)
}

func locationListCmd(rootOptions *internal.GlobalCommandOptions) *cobra.Command {
	action := commands.ActionFunc(func(ctx context.Context, cmd *cobra.Command, args []string, azdCtx *azdcontext.AzdContext) error {
		formatter := output.GetFormatter(ctx)
		writer := output.GetDefaultWriter()
		manager := account.NewManager(ctx)

		locations, err := manager.GetLocations(ctx)
		if err != nil {
			return err
		}

		switch formatter.Kind() {
		case output.JsonFormat:
			err = formatter.Format(locations, writer, nil)
		case output.TableFormat:
			tableOptions := output.TableFormatterOptions{
				Columns: []output.Column{
					{
						Heading:       "Name",
						ValueTemplate: "{{.Name}}",
					},
					{
						Heading:       "Regional Name",
						ValueTemplate: "{{.DisplayName}}",
					},
				},
			}

			err = formatter.Format(locations, writer, tableOptions)
		}

		if err != nil {
			return fmt.Errorf("failed formatting output")
		}

		return nil
	})

	return commands.Build(action, rootOptions, "list-locations", "Gets the available Azure locations for the default Azure account.", nil)
}

func accountShowCmd(rootOptions *internal.GlobalCommandOptions) *cobra.Command {
	action := commands.ActionFunc(func(ctx context.Context, cmd *cobra.Command, args []string, azdCtx *azdcontext.AzdContext) error {
		formatter := output.GetFormatter(ctx)
		writer := output.GetDefaultWriter()

		config := config.GetConfig(ctx)
		if config.DefaultSubscription == nil {
			return fmt.Errorf("default subscription has not been set")
		}

		err := formatter.Format(config.DefaultSubscription, writer, nil)
		if err != nil {
			return fmt.Errorf("failed formatting output: %w", err)
		}

		return nil
	})

	return commands.Build(action, rootOptions, "show", "Shows the default Azure subscription", nil)
}

func accountSetSubscriptionCmd(rootOptions *internal.GlobalCommandOptions) *cobra.Command {
	return commands.Build(
		&accountSetSubscriptionAction{
			rootOptions: rootOptions,
		}, rootOptions,
		"set",
		"Sets the default Azure subscription.",
		nil)
}

type accountSetSubscriptionAction struct {
	rootOptions    *internal.GlobalCommandOptions
	subscriptionId string
}

func (a *accountSetSubscriptionAction) SetupFlags(persis, local *pflag.FlagSet) {
	local.StringVarP(&a.subscriptionId, "subscriptionId", "s", "", "Azure Subscription ID.")
}

func (a *accountSetSubscriptionAction) Run(ctx context.Context, cmd *cobra.Command, args []string, azdCtx *azdcontext.AzdContext) error {
	console := input.GetConsole(ctx)
	manager := account.NewManager(ctx)

	subscription, err := manager.SetDefaultSubscription(ctx, a.subscriptionId)
	if err != nil {
		return fmt.Errorf("failed setting default subscription, '%s'", a.subscriptionId)
	}

	console.Message(ctx, output.WithSuccessFormat("SUCCESS: '%s (%s)' has been set as your default Azure subscription.", subscription.Name, subscription.Id))

	return nil
}

func accountSetLocationCmd(rootOptions *internal.GlobalCommandOptions) *cobra.Command {
	return commands.Build(
		&accountSetLocationAction{
			rootOptions: rootOptions,
		}, rootOptions,
		"set-location",
		"Sets the default Azure location.",
		nil)
}

type accountSetLocationAction struct {
	rootOptions *internal.GlobalCommandOptions
	location    string
}

func (a *accountSetLocationAction) SetupFlags(persis, local *pflag.FlagSet) {
	local.StringVarP(&a.location, "location", "l", "", "Azure location")
}

func (a *accountSetLocationAction) Run(ctx context.Context, cmd *cobra.Command, args []string, azdCtx *azdcontext.AzdContext) error {
	console := input.GetConsole(ctx)
	manager := account.NewManager(ctx)

	location, err := manager.SetDefaultLocation(ctx, a.location)
	if err != nil {
		return fmt.Errorf("failed setting default location, '%s'", a.location)
	}

	console.Message(ctx, output.WithSuccessFormat("SUCCESS: '%s (%s)' has been set as your default Azure location.", location.DisplayName, location.Name))

	return nil
}
