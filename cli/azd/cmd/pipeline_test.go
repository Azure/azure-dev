// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
)

func TestPipelineCmd(t *testing.T) {
	root := actions.NewActionDescriptor("azd", &actions.ActionDescriptorOptions{})
	group := pipelineActions(root)
	assert.EqualValues(t, "pipeline", group.Options.Command.Use)
	assert.EqualValues(t, "Manage GitHub Actions or Azure Pipelines.", group.Options.Command.Short)

	childCommands := group.Children()
	assert.EqualValues(t, 1, len(childCommands))
}

func TestPipelineConfigCmd(t *testing.T) {
	command := newPipelineConfigCmd()
	assert.EqualValues(t, "config", command.Use)
	assert.EqualValues(t, "Create and configure your deployment pipeline by using GitHub Actions or Azure Pipelines.",
		command.Short)

	childCommands := command.Commands()
	assert.EqualValues(t, 0, len(childCommands))
}

func TestSetupFlags(t *testing.T) {
	command := newPipelineConfigCmd()
	_ = newPipelineConfigFlags(command, &internal.GlobalCommandOptions{})
	flagName := "principal-name"
	principalNameFlag := command.LocalFlags().Lookup(flagName)
	assert.NotEqual(t, (*pflag.Flag)(nil), principalNameFlag)
	assert.Equal(t, "", principalNameFlag.Value.String())
	assert.Equal(
		t,
		"The name of the service principal to use to grant access to Azure resources as part of the pipeline.",
		principalNameFlag.Usage,
	)
	principalNameFlag = command.PersistentFlags().Lookup(flagName)
	assert.Equal(t, (*pflag.Flag)(nil), principalNameFlag)

	flagName = "remote-name"
	remoteNameFlag := command.LocalFlags().Lookup(flagName)
	assert.NotEqual(t, (*pflag.Flag)(nil), remoteNameFlag)
	assert.Equal(t, "origin", remoteNameFlag.Value.String())
	assert.Equal(t, "The name of the git remote to configure the pipeline to run on.", remoteNameFlag.Usage)
	remoteNameFlag = command.PersistentFlags().Lookup(flagName)
	assert.Equal(t, (*pflag.Flag)(nil), remoteNameFlag)

	flagName = "principal-role"
	principalRoleNameFlag := command.LocalFlags().Lookup(flagName)
	assert.NotEqual(t, (*pflag.Flag)(nil), principalRoleNameFlag)
	assert.Equal(t, "contributor", principalRoleNameFlag.Value.String())
	assert.Equal(t, "The role to assign to the service principal.", principalRoleNameFlag.Usage)
	principalRoleNameFlag = command.PersistentFlags().Lookup(flagName)
	assert.Equal(t, (*pflag.Flag)(nil), principalRoleNameFlag)
}
