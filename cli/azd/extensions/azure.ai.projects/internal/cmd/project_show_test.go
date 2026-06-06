// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProjectShowCommand_AcceptsNoArgs(t *testing.T) {
	t.Parallel()
	cmd := newProjectShowCommand(nil)
	assert.NoError(t, cmd.Args(cmd, []string{}))
}

func TestProjectShowCommand_RejectsArgs(t *testing.T) {
	t.Parallel()
	cmd := newProjectShowCommand(nil)
	assert.Error(t, cmd.Args(cmd, []string{"extra"}))
}

func TestProjectShowCommand_DefaultOutputFormat(t *testing.T) {
	t.Parallel()
	cmd := newProjectShowCommand(nil)
	assertOutputFlagOptions(t, cmd, "table", []string{"json", "table"})
}

func TestProjectShowCommand_HasNoProjectEndpointFlag(t *testing.T) {
	t.Parallel()
	cmd := newProjectShowCommand(nil)
	assert.Nil(t, cmd.Flags().Lookup("project-endpoint"),
		"--project-endpoint flag should not be registered on `project show`; "+
			"it adds no value over echoing back the user-provided URL")
}

func TestHumanSourceDetail(t *testing.T) {
	t.Parallel()
	tests := []struct {
		source     EndpointSource
		azdEnvName string
		want       string
	}{
		{SourceFlag, "", "--project-endpoint flag"},
		{SourceAzdEnv, "dev", "azd env (dev)"},
		{SourceAzdEnv, "", "azd env"},
		{SourceGlobalConfig, "", "global config (~/.azd/config.json)"},
		{SourceFoundryEnv, "", "FOUNDRY_PROJECT_ENDPOINT"},
	}
	for _, tt := range tests {
		t.Run(string(tt.source)+"/"+tt.azdEnvName, func(t *testing.T) {
			t.Parallel()
			got := humanSourceDetail(tt.source, tt.azdEnvName)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRootCommand_HasProjectSubcommands(t *testing.T) {
	t.Parallel()
	root := NewRootCommand()
	names := make(map[string]bool, len(root.Commands()))
	for _, sub := range root.Commands() {
		names[sub.Name()] = true
	}
	assert.True(t, names["set"], "root should have `set` subcommand")
	assert.True(t, names["unset"], "root should have `unset` subcommand")
	assert.True(t, names["show"], "root should have `show` subcommand")
}

func TestJSONSourceDetail(t *testing.T) {
	t.Parallel()
	// These values are part of the public JSON contract; verify they are stable.
	tests := []struct {
		source EndpointSource
		want   string
	}{
		{SourceFlag, "--project-endpoint flag"},
		{SourceAzdEnv, "azd env"},
		{SourceGlobalConfig, "~/.azd/config.json"},
		{SourceFoundryEnv, "FOUNDRY_PROJECT_ENDPOINT"},
	}
	for _, tt := range tests {
		t.Run(string(tt.source), func(t *testing.T) {
			t.Parallel()
			got := jsonSourceDetail(tt.source)
			assert.Equal(t, tt.want, got,
				"jsonSourceDetail(%q) must return a stable machine-readable value", tt.source)
		})
	}
}
