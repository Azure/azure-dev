// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package figspec

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

func TestCustomizations_GetCommandArgGenerator(t *testing.T) {
	c := &Customizations{}

	tests := []struct {
		name    string
		path    string
		argName string
		want    string
	}{
		{"env_get_value", "azd env get-value", "keyName", FigGenListEnvironmentVariables},
		{"env_get_value_other", "azd env get-value", "other", ""},
		{"env_select", "azd env select", "environment", FigGenListEnvironments},
		{"template_show", "azd template show", "template", FigGenListTemplates},
		// install is handled via GetCommandArgs (combined id|zip arg), not here.
		{"ext_install", "azd extension install", "extension-id", ""},
		{"ext_show", "azd extension show", "extension-id", FigGenListExtensions},
		{"ext_upgrade", "azd extension upgrade", "extension-id", FigGenListInstalledExtensions},
		{"ext_uninstall", "azd extension uninstall", "extension-id", FigGenListInstalledExtensions},
		{"config_get", "azd config get", "path", FigGenListConfigKeys},
		{"config_set", "azd config set", "path", FigGenListConfigKeys},
		{"config_unset", "azd config unset", "path", FigGenListConfigKeys},
		{"unknown", "azd unknown", "arg", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := &CommandContext{CommandPath: tt.path}
			require.Equal(t, tt.want, c.GetCommandArgGenerator(ctx, tt.argName))
		})
	}
}

func TestCustomizations_GetFlagGenerator(t *testing.T) {
	c := &Customizations{}

	tests := []struct {
		name     string
		path     string
		flagName string
		want     string
	}{
		{"init_filter", "azd init", "filter", FigGenListTemplateTags},
		{"init_template", "azd init", "template", FigGenListTemplatesFiltered},
		{"init_other", "azd init", "other", ""},
		{"template_list_filter", "azd template list", "filter", FigGenListTemplateTags},
		{"template_list_other", "azd template list", "other", ""},
		{"unrelated", "azd deploy", "output", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
			fs.String(tt.flagName, "", "test")
			ctx := &FlagContext{Flag: fs.Lookup(tt.flagName), CommandPath: tt.path}
			require.Equal(t, tt.want, c.GetFlagGenerator(ctx))
		})
	}
}

func TestGenerateCommandArgs_CustomArgsWithGenerators(t *testing.T) {
	cmd := &cobra.Command{Use: "get-value <keyName>"}
	sb := newTestSpecBuilder(false)
	ctx := &CommandContext{Command: cmd, CommandPath: "azd env get-value"}

	// The customizations provider returns nil for this path (it's handled by generator),
	// but generator provider should still be called for Use-parsed args
	args := sb.generateCommandArgs(cmd, ctx)
	require.Len(t, args, 1)
	require.Equal(t, "keyName", args[0].Name)
	require.Equal(t, FigGenListEnvironmentVariables, args[0].Generator)
}

func TestGeneratorConstants(t *testing.T) {
	// Verify all generator constants have the expected prefix
	generators := []string{
		FigGenListEnvironments,
		FigGenListEnvironmentVariables,
		FigGenListTemplates,
		FigGenListTemplateTags,
		FigGenListTemplatesFiltered,
		FigGenListExtensions,
		FigGenListInstalledExtensions,
		FigGenListConfigKeys,
	}
	for _, g := range generators {
		require.True(t, strings.HasPrefix(g, "azdGenerators."), "generator %q should have azdGenerators. prefix", g)
	}
	require.Len(t, generators, 8, "verify we're testing all declared generators")
}
