package cmd

import (
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/spf13/cobra"
)

func bindActions(root *actions.ActionDescriptor) *actions.ActionDescriptor {
	group := root.Add("binding", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "binding",
			Short: "Manage bindings.",
		},
		HelpOptions: actions.ActionHelpOptions{
			Description: getCmdBindingHelpDescription,
		},
		GroupingOptions: actions.CommandGroupOptions{
			RootLevelHelp: actions.CmdGroupManage,
		},
	})

	return group
}

func getCmdBindingHelpDescription(*cobra.Command) string {
	return generateCmdHelpDescription(
		"Manage your application bindings. With this command group, you can create, delete"+
			" or view your application environments.",
		[]string{})
}
