// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// TestNewOperationCommand verifies the jobs command structure
func TestNewOperationCommand(t *testing.T) {
	cmd := newOperationCommand()

	require.NotNil(t, cmd)
	require.Equal(t, "jobs", cmd.Use)
	require.Equal(t, "Manage fine-tuning jobs", cmd.Short)
	require.NotNil(t, cmd.PersistentPreRunE, "jobs command should have PersistentPreRunE for environment validation")
}

func TestNewOperationCommand_HasAllSubcommands(t *testing.T) {
	cmd := newOperationCommand()

	// Get all subcommand names
	subcommands := make(map[string]bool)
	for _, subcmd := range cmd.Commands() {
		subcommands[subcmd.Use] = true
	}

	// Verify all expected subcommands are present
	expectedCommands := []string{
		"submit",
		"show",
		"list",
		"pause",
		"resume",
		"cancel",
		"deploy",
	}

	for _, expected := range expectedCommands {
		require.True(t, subcommands[expected], "Missing subcommand: %s", expected)
	}

	require.Equal(t, len(expectedCommands), len(cmd.Commands()),
		"Unexpected number of subcommands")
}

func TestNewOperationSubmitCommand(t *testing.T) {
	cmd := newOperationSubmitCommand()

	require.NotNil(t, cmd)
	require.Equal(t, "submit", cmd.Use)
	require.NotNil(t, cmd.PreRunE, "submit command should have PreRunE for validation")
	require.NotNil(t, cmd.RunE)
}

func TestNewOperationSubmitCommand_Flags(t *testing.T) {
	cmd := newOperationSubmitCommand()

	// Test that all expected flags are defined
	expectedFlags := []struct {
		name      string
		shorthand string
	}{
		{"file", "f"},
		{"model", "m"},
		{"training-file", "t"},
		{"validation-file", "v"},
		{"suffix", "s"},
		{"seed", "r"},
	}

	for _, flag := range expectedFlags {
		t.Run(flag.name, func(t *testing.T) {
			f := cmd.Flags().Lookup(flag.name)
			require.NotNil(t, f, "Flag --%s should be defined", flag.name)
			require.Equal(t, flag.shorthand, f.Shorthand,
				"Flag --%s should have shorthand -%s", flag.name, flag.shorthand)
		})
	}
}

func TestNewOperationShowCommand(t *testing.T) {
	cmd := newOperationShowCommand()

	require.NotNil(t, cmd)
	require.Equal(t, "show", cmd.Use)
	require.NotNil(t, cmd.PreRunE, "show command should have PreRunE for validation")
	require.NotNil(t, cmd.RunE)
}

func TestNewOperationShowCommand_Flags(t *testing.T) {
	cmd := newOperationShowCommand()

	expectedFlags := []struct {
		name         string
		shorthand    string
		defaultValue string
	}{
		{"id", "i", ""},
		{"output", "o", "table"},
	}

	for _, flag := range expectedFlags {
		t.Run(flag.name, func(t *testing.T) {
			f := cmd.Flags().Lookup(flag.name)
			require.NotNil(t, f, "Flag --%s should be defined", flag.name)
			require.Equal(t, flag.shorthand, f.Shorthand)
			require.Equal(t, flag.defaultValue, f.DefValue)
		})
	}

	// logs flag is a bool flag
	logsFlag := cmd.Flags().Lookup("logs")
	require.NotNil(t, logsFlag)
	require.Equal(t, "false", logsFlag.DefValue)
}

func TestNewOperationListCommand(t *testing.T) {
	cmd := newOperationListCommand()

	require.NotNil(t, cmd)
	require.Equal(t, "list", cmd.Use)
	require.Nil(t, cmd.PreRunE, "list command should not require PreRunE validation")
	require.NotNil(t, cmd.RunE)
}

func TestNewOperationListCommand_Flags(t *testing.T) {
	cmd := newOperationListCommand()

	// top flag (limit)
	topFlag := cmd.Flags().Lookup("top")
	require.NotNil(t, topFlag)
	require.Equal(t, "t", topFlag.Shorthand)
	require.Equal(t, "10", topFlag.DefValue)

	// after flag (pagination)
	afterFlag := cmd.Flags().Lookup("after")
	require.NotNil(t, afterFlag)
	require.Equal(t, "", afterFlag.DefValue)

	// output flag
	outputFlag := cmd.Flags().Lookup("output")
	require.NotNil(t, outputFlag)
	require.Equal(t, "o", outputFlag.Shorthand)
	require.Equal(t, "table", outputFlag.DefValue)
}

func TestNewOperationPauseCommand(t *testing.T) {
	cmd := newOperationPauseCommand()

	require.NotNil(t, cmd)
	require.Equal(t, "pause", cmd.Use)
	require.Contains(t, cmd.Short, "Pauses")
	require.NotNil(t, cmd.PreRunE, "pause command should have PreRunE for validation")
	require.NotNil(t, cmd.RunE)
}

func TestNewOperationPauseCommand_Flags(t *testing.T) {
	cmd := newOperationPauseCommand()

	idFlag := cmd.Flags().Lookup("id")
	require.NotNil(t, idFlag)
	require.Equal(t, "i", idFlag.Shorthand)
}

func TestNewOperationResumeCommand(t *testing.T) {
	cmd := newOperationResumeCommand()

	require.NotNil(t, cmd)
	require.Equal(t, "resume", cmd.Use)
	require.Contains(t, cmd.Short, "Resumes")
	require.NotNil(t, cmd.PreRunE, "resume command should have PreRunE for validation")
	require.NotNil(t, cmd.RunE)
}

func TestNewOperationResumeCommand_Flags(t *testing.T) {
	cmd := newOperationResumeCommand()

	idFlag := cmd.Flags().Lookup("id")
	require.NotNil(t, idFlag)
	require.Equal(t, "i", idFlag.Shorthand)
}

func TestNewOperationCancelCommand(t *testing.T) {
	cmd := newOperationCancelCommand()

	require.NotNil(t, cmd)
	require.Equal(t, "cancel", cmd.Use)
	require.Contains(t, cmd.Short, "Cancels")
	require.NotNil(t, cmd.PreRunE, "cancel command should have PreRunE for validation")
	require.NotNil(t, cmd.RunE)
}

func TestNewOperationCancelCommand_Flags(t *testing.T) {
	cmd := newOperationCancelCommand()

	idFlag := cmd.Flags().Lookup("id")
	require.NotNil(t, idFlag)
	require.Equal(t, "i", idFlag.Shorthand)

	forceFlag := cmd.Flags().Lookup("force")
	require.NotNil(t, forceFlag)
	require.Equal(t, "false", forceFlag.DefValue)
}

func TestNewOperationDeployModelCommand(t *testing.T) {
	cmd := newOperationDeployModelCommand()

	require.NotNil(t, cmd)
	require.Equal(t, "deploy", cmd.Use)
	require.Contains(t, cmd.Short, "Deploy")
	require.NotNil(t, cmd.PreRunE, "deploy command should have PreRunE for validation")
	require.NotNil(t, cmd.RunE)
}

func TestNewOperationDeployModelCommand_Flags(t *testing.T) {
	cmd := newOperationDeployModelCommand()

	expectedFlags := []struct {
		name         string
		shorthand    string
		defaultValue string
	}{
		{"job-id", "i", ""},
		{"deployment-name", "d", ""},
		{"model-format", "m", "OpenAI"},
		{"sku", "s", "GlobalStandard"},
		{"version", "v", "1"},
	}

	for _, flag := range expectedFlags {
		t.Run(flag.name, func(t *testing.T) {
			f := cmd.Flags().Lookup(flag.name)
			require.NotNil(t, f, "Flag --%s should be defined", flag.name)
			require.Equal(t, flag.shorthand, f.Shorthand)
			require.Equal(t, flag.defaultValue, f.DefValue)
		})
	}

	// capacity flag (int32)
	capacityFlag := cmd.Flags().Lookup("capacity")
	require.NotNil(t, capacityFlag)
	require.Equal(t, "c", capacityFlag.Shorthand)
	require.Equal(t, "1", capacityFlag.DefValue)

	// no-wait flag (bool)
	noWaitFlag := cmd.Flags().Lookup("no-wait")
	require.NotNil(t, noWaitFlag)
	require.Equal(t, "false", noWaitFlag.DefValue)
}

func TestNewOperationDeployModelCommand_RequiredFlags(t *testing.T) {
	cmd := newOperationDeployModelCommand()

	// Check that job-id and deployment-name are marked as required
	jobIDFlag := cmd.Flags().Lookup("job-id")
	require.NotNil(t, jobIDFlag)

	deploymentNameFlag := cmd.Flags().Lookup("deployment-name")
	require.NotNil(t, deploymentNameFlag)

	// Both flags should have annotations marking them as required
	// Note: Cobra marks required flags with annotations
	require.NotEmpty(t, jobIDFlag.Annotations)
	require.NotEmpty(t, deploymentNameFlag.Annotations)
}

func TestCommandsHaveDescriptions(t *testing.T) {
	commands := []struct {
		name    string
		cmdFunc func() *cobra.Command
	}{
		{"submit", func() *cobra.Command { return newOperationSubmitCommand() }},
		{"show", func() *cobra.Command { return newOperationShowCommand() }},
		{"list", func() *cobra.Command { return newOperationListCommand() }},
		{"pause", func() *cobra.Command { return newOperationPauseCommand() }},
		{"resume", func() *cobra.Command { return newOperationResumeCommand() }},
		{"cancel", func() *cobra.Command { return newOperationCancelCommand() }},
		{"deploy", func() *cobra.Command { return newOperationDeployModelCommand() }},
	}

	for _, tc := range commands {
		t.Run(tc.name, func(t *testing.T) {
			cmd := tc.cmdFunc()
			require.NotEmpty(t, cmd.Short, "Command %s should have a Short description", tc.name)
		})
	}
}
