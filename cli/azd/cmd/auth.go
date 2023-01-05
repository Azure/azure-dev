package cmd

import (
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
)

func authActions(root *actions.ActionDescriptor) *actions.ActionDescriptor {
	group := root.Add("auth", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Hidden: true,
		},
	})

	group.Add("token", &actions.ActionDescriptorOptions{
		Command:        newAuthTokenCmd(),
		FlagsResolver:  newAuthTokenFlags,
		ActionResolver: newAuthTokenAction,
		OutputFormats:  []output.Format{output.JsonFormat},
		DefaultFormat:  output.NoneFormat,
	})

	return group
}
