// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package figspec

import (
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

func TestCustomizations_GetSuggestions(t *testing.T) {
	c := &Customizations{}

	tests := []struct {
		name     string
		path     string
		flagName string
		want     []string
	}{
		{
			"auth_login_fedcred", "azd auth login",
			"federated-credential-provider",
			[]string{"github", "azure-pipelines", "oidc"},
		},
		{"auth_login_other", "azd auth login", "other", nil},
		{"pipeline_provider", "azd pipeline config", "provider", []string{"github", "azdo"}},
		{"pipeline_authtype", "azd pipeline config", "auth-type", []string{"federated", "client-credentials"}},
		{"pipeline_other", "azd pipeline config", "output", nil},
		{"copilot_consent_action", "azd copilot consent allow", "action", []string{"all", "readonly"}},
		{"copilot_consent_operation", "azd copilot consent allow", "operation", []string{"tool", "sampling"}},
		{"copilot_consent_permission", "azd copilot consent allow", "permission", []string{"allow", "deny", "prompt"}},
		{"copilot_consent_scope", "azd copilot consent allow", "scope", []string{"global", "project"}},
		{"copilot_consent_other", "azd copilot consent allow", "other", nil},
		{"unrelated", "azd init", "template", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
			fs.String(tt.flagName, "", "test flag")
			ctx := &FlagContext{Flag: fs.Lookup(tt.flagName), CommandPath: tt.path}
			require.Equal(t, tt.want, c.GetSuggestions(ctx))
		})
	}
}

func TestCustomizations_GetCommandArgs(t *testing.T) {
	c := &Customizations{}

	t.Run("env_set", func(t *testing.T) {
		ctx := &CommandContext{CommandPath: "azd env set"}
		args := c.GetCommandArgs(ctx)
		require.Len(t, args, 2)
		require.Equal(t, "key", args[0].Name)
		require.True(t, args[0].IsOptional)
		require.Equal(t, "value", args[1].Name)
		require.True(t, args[1].IsOptional)
	})

	t.Run("hooks_run", func(t *testing.T) {
		ctx := &CommandContext{CommandPath: "azd hooks run"}
		args := c.GetCommandArgs(ctx)
		require.Len(t, args, 1)
		require.Equal(t, "name", args[0].Name)
		require.NotEmpty(t, args[0].Suggestions)
		require.Contains(t, args[0].Suggestions, "prebuild")
		require.Contains(t, args[0].Suggestions, "postdeploy")
	})

	t.Run("service_commands", func(t *testing.T) {
		for _, path := range []string{"azd build", "azd deploy", "azd package", "azd publish", "azd restore"} {
			t.Run(path, func(t *testing.T) {
				ctx := &CommandContext{CommandPath: path}
				args := c.GetCommandArgs(ctx)
				require.Len(t, args, 1)
				require.Equal(t, "service", args[0].Name)
				require.True(t, args[0].IsOptional)
			})
		}
	})

	t.Run("ext_install", func(t *testing.T) {
		ctx := &CommandContext{CommandPath: "azd extension install"}
		args := c.GetCommandArgs(ctx)
		require.Len(t, args, 1)
		require.Equal(t, "extension-id|extension-bundle.zip", args[0].Name)
		// Offers both extension-id completion and .zip file-path suggestions.
		require.Equal(t, []string{FigGenListExtensions, FigGenFilepathsZip}, args[0].Generators)
		require.Empty(t, args[0].Generator)
	})

	t.Run("unknown", func(t *testing.T) {
		ctx := &CommandContext{CommandPath: "azd unknown"}
		require.Nil(t, c.GetCommandArgs(ctx))
	})
}

func TestCustomizations_GetFlagArgs(t *testing.T) {
	c := &Customizations{}

	t.Run("deploy_from_package", func(t *testing.T) {
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		fs.String("from-package", "", "test")
		ctx := &FlagContext{Flag: fs.Lookup("from-package"), CommandPath: "azd deploy"}
		arg := c.GetFlagArgs(ctx)
		require.NotNil(t, arg)
		require.Equal(t, "file-path|image-tag", arg.Name)
	})

	t.Run("publish_from_package", func(t *testing.T) {
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		fs.String("from-package", "", "test")
		ctx := &FlagContext{Flag: fs.Lookup("from-package"), CommandPath: "azd publish"}
		arg := c.GetFlagArgs(ctx)
		require.NotNil(t, arg)
		require.Equal(t, "image-tag", arg.Name)
	})

	t.Run("publish_to", func(t *testing.T) {
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		fs.String("to", "", "test")
		ctx := &FlagContext{Flag: fs.Lookup("to"), CommandPath: "azd publish"}
		arg := c.GetFlagArgs(ctx)
		require.NotNil(t, arg)
		require.Equal(t, "image-tag", arg.Name)
	})

	t.Run("deploy_other_flag", func(t *testing.T) {
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		fs.String("other", "", "test")
		ctx := &FlagContext{Flag: fs.Lookup("other"), CommandPath: "azd deploy"}
		require.Nil(t, c.GetFlagArgs(ctx))
	})

	t.Run("unrelated_command", func(t *testing.T) {
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		fs.String("from-package", "", "test")
		ctx := &FlagContext{Flag: fs.Lookup("from-package"), CommandPath: "azd init"}
		require.Nil(t, c.GetFlagArgs(ctx))
	})
}
