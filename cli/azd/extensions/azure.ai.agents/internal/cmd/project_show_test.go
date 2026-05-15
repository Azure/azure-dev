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

func TestProjectCommand_HasSubcommands(t *testing.T) {
	t.Parallel()
	cmd := newProjectCommand(nil)
	names := make([]string, 0, len(cmd.Commands()))
	for _, sub := range cmd.Commands() {
		names = append(names, sub.Name())
	}
	assert.Contains(t, names, "set")
	assert.Contains(t, names, "unset")
	assert.Contains(t, names, "show")
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
