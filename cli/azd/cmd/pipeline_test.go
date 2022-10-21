// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
)

func TestPipelineCmd(t *testing.T) {
	globalOpt := &internal.GlobalCommandOptions{}
	command := pipelineCmd(globalOpt)
	assert.EqualValues(t, "pipeline", command.Use)
	assert.EqualValues(t, "Manage GitHub Actions pipelines.", command.Short)

	childCommands := command.Commands()
	assert.EqualValues(t, 1, len(childCommands))
}

func TestPipelineConfigCmd(t *testing.T) {
	globalOpt := &internal.GlobalCommandOptions{}
	command, _ := pipelineConfigCmdDesign(globalOpt)
	assert.EqualValues(t, "config", command.Use)
	assert.EqualValues(t, "Create and configure your deployment pipeline by using GitHub Actions.", command.Short)

	childCommands := command.Commands()
	assert.EqualValues(t, 0, len(childCommands))
}

func TestSetupFlags(t *testing.T) {
	globalOpt := &internal.GlobalCommandOptions{}
	command, _ := pipelineConfigCmdDesign(globalOpt)

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
	assert.Equal(t, "Contributor", principalRoleNameFlag.Value.String())
	assert.Equal(t, "The role to assign to the service principal.", principalRoleNameFlag.Usage)
	principalRoleNameFlag = command.PersistentFlags().Lookup(flagName)
	assert.Equal(t, (*pflag.Flag)(nil), principalRoleNameFlag)
}
