package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/exp/slices"
)

// Setup account command category
func accountCmd(rootOptions *internal.GlobalCommandOptions) *cobra.Command {
	root := &cobra.Command{
		Use:   "account",
		Short: "Manages and sets default Azure account settings.",
	}

	// Subscription listing with Table & JSON outputs
	root.AddCommand(output.AddOutputParam(
		accountListCmd(rootOptions),
		[]output.Format{output.TableFormat, output.JsonFormat},
		output.TableFormat,
	))

	// Location listing with Table and JSON outputs
	root.AddCommand(output.AddOutputParam(
		locationListCmd(rootOptions),
		[]output.Format{output.TableFormat, output.JsonFormat},
		output.TableFormat,
	))

	// Account settings with JSON output
	root.AddCommand(output.AddOutputParam(accountShowCmd(rootOptions),
		[]output.Format{output.JsonFormat},
		output.JsonFormat,
	))

	// No explicit command outputs
	root.AddCommand(accountSetCmd(rootOptions))
	root.AddCommand(accountClearCmd(rootOptions))

	root.Flags().BoolP("help", "h", false, fmt.Sprintf("Gets help for %s.", root.Name()))

	return root
}

// Command to list available Azure subscriptions for the current logged in principal.
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
						Heading:       "Subscription ID",
						ValueTemplate: "{{.Id}}",
					},
					{
						Heading:       "Name",
						ValueTemplate: "{{.Name}}",
					},
					{
						Heading:       "Default",
						ValueTemplate: "{{.IsDefault}}",
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

// Command to list valid locations for the default Azure subscription/account.
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
						Heading:       "Key",
						ValueTemplate: "{{.Name}}",
					},
					{
						Heading:       "Regional Name",
						ValueTemplate: "{{.RegionalDisplayName}}",
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

// Command that shows the default subscription & location for the logged in user
func accountShowCmd(rootOptions *internal.GlobalCommandOptions) *cobra.Command {
	action := commands.ActionFunc(func(ctx context.Context, cmd *cobra.Command, args []string, azdCtx *azdcontext.AzdContext) error {
		formatter := output.GetFormatter(ctx)
		writer := output.GetDefaultWriter()
		manager := account.NewManager(ctx)

		config, err := manager.GetAccountDefaults(ctx)
		if err != nil {
			return fmt.Errorf("failed retrieving account defaults: %w", err)
		}

		err = formatter.Format(config, writer, nil)
		if err != nil {
			return fmt.Errorf("failed formatting output: %w", err)
		}

		return nil
	})

	return commands.Build(action, rootOptions, "show", "Shows the default Azure subscription", nil)
}

func accountClearCmd(rootOptions *internal.GlobalCommandOptions) *cobra.Command {
	action := commands.ActionFunc(func(ctx context.Context, cmd *cobra.Command, args []string, azdCtx *azdcontext.AzdContext) error {
		console := input.GetConsole(ctx)
		manager := account.NewManager(ctx)

		err := manager.Clear(ctx)
		if err != nil {
			return errors.New("failed clearing AZD account defaults")
		}

		console.Message(ctx, output.WithSuccessFormat("SUCCESS: Azure Developer CLI defaults have been reset."))

		return nil
	})

	return commands.Build(action, rootOptions, "clear", "Clears all defaults from Azure Developer CLI configuration.", nil)
}

// Options for account set command
type accountSetAction struct {
	rootOptions      *internal.GlobalCommandOptions
	subscriptionId   string
	subscriptionName string
	location         string
}

func accountSetCmd(rootOptions *internal.GlobalCommandOptions) *cobra.Command {
	return commands.Build(
		&accountSetAction{
			rootOptions: rootOptions,
		}, rootOptions,
		"set",
		"Sets the default Azure subscription and/or location.",
		nil)
}

func (a *accountSetAction) SetupFlags(persis, local *pflag.FlagSet) {
	local.StringVarP(&a.subscriptionName, "name", "n", "", "Azure subscription name.")
	local.StringVarP(&a.subscriptionId, "subscriptionId", "s", "", "Azure Subscription ID.")
	local.StringVarP(&a.location, "location", "l", "", "Azure location.")
}

func (a *accountSetAction) Run(ctx context.Context, cmd *cobra.Command, args []string, azdCtx *azdcontext.AzdContext) error {
	console := input.GetConsole(ctx)
	manager := account.NewManager(ctx)

	subscriptionSet := false

	// Sets defaults subscription when -s / --subscriptionId argument has been specified
	if strings.TrimSpace(a.subscriptionId) != "" {
		_, err := manager.SetDefaultSubscription(ctx, a.subscriptionId)
		if err != nil {
			return fmt.Errorf("failed setting default subscription, '%s'", a.subscriptionId)
		}
		subscriptionSet = true
	}

	// Sets defaults subscription when -n / --name argument has been specified
	if !subscriptionSet && strings.TrimSpace(a.subscriptionName) != "" {
		subscriptions, err := manager.GetSubscriptions(ctx)
		if err != nil {
			return err
		}

		// Lookup subscriptions and attempt to match by name
		subIndex := slices.IndexFunc(subscriptions, func(s azcli.AzCliSubscriptionInfo) bool {
			return strings.TrimSpace(strings.ToLower(a.subscriptionName)) == strings.ToLower(s.Name)
		})

		if subIndex < 0 {
			return fmt.Errorf("subscription '%s' not found", a.subscriptionName)
		}

		_, err = manager.SetDefaultSubscription(ctx, subscriptions[subIndex].Id)
		if err != nil {
			return fmt.Errorf("failed setting default subscription, '%s'", a.subscriptionId)
		}
	}

	// Sets default location when -l / --location argument has been specified
	if strings.TrimSpace(a.location) != "" {
		_, err := manager.SetDefaultLocation(ctx, a.location)
		if err != nil {
			return fmt.Errorf("failed setting default location, '%s' : %w", a.location, err)
		}
	}

	console.Message(ctx, output.WithSuccessFormat("SUCCESS: Account defaults updated."))

	return nil
}
