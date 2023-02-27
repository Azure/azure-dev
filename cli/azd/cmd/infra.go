// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/cmd/middleware"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
)

func infraActions(root *actions.ActionDescriptor) *actions.ActionDescriptor {
	cmd := &cobra.Command{
		Short: i18nGetText(i18nCmdInfraShort),
	}
	setGroupCommandAnnotation(cmd, cmdGroupManage)
	group := root.Add("infra", &actions.ActionDescriptorOptions{
		Command: cmd,
	})

	group.
		Add("create", &actions.ActionDescriptorOptions{
			Command:        newInfraCreateCmd(),
			FlagsResolver:  newInfraCreateFlags,
			ActionResolver: newInfraCreateAction,
			OutputFormats:  []output.Format{output.JsonFormat, output.NoneFormat},
			DefaultFormat:  output.NoneFormat,
		}).
		UseMiddleware("hooks", middleware.NewHooksMiddleware)

	group.
		Add("delete", &actions.ActionDescriptorOptions{
			Command:        newInfraDeleteCmd(),
			FlagsResolver:  newInfraDeleteFlags,
			ActionResolver: newInfraDeleteAction,
			OutputFormats:  []output.Format{output.JsonFormat, output.NoneFormat},
			DefaultFormat:  output.NoneFormat,
		}).
		UseMiddleware("hooks", middleware.NewHooksMiddleware)

	return group
}
