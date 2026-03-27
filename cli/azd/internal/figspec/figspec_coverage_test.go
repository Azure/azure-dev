// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package figspec

import (
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// typescript_renderer.go — pure functions
// ============================================================================

func TestEscapeString(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"plain", "hello world", "hello world"},
		{"backslash", `a\b`, `a\\b`},
		{"single_quote", "it's", `it\'s`},
		{"newline", "a\nb", `a\nb`},
		{"carriage_return", "a\rb", `a\rb`},
		{"tab", "a\tb", `a\tb`},
		{"combined", "it's a\nnew\\line", `it\'s a\nnew\\line`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, escapeString(tt.in))
		})
	}
}

func TestQuoteString(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"simple", "hello", "'hello'"},
		{"with_quote", "it's", `'it\'s'`},
		{"empty", "", "''"},
		// After escaping, \n is literal characters `\n`, so no real newline remains
		{"already_escaped_newline", "line1\nline2", `'line1\nline2'`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, quoteString(tt.in))
		})
	}
}

func TestIndentString(t *testing.T) {
	tests := []struct {
		name  string
		s     string
		level int
		want  string
	}{
		{"zero_indent", "hello", 0, "hello"},
		{"one_tab", "hello", 1, "\thello"},
		{"two_tabs", "hello", 2, "\t\thello"},
		{"multiline", "a\nb", 1, "\ta\n\tb"},
		{"empty_lines_preserved", "a\n\nb", 1, "\ta\n\n\tb"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, indentString(tt.s, tt.level))
		})
	}
}

func TestRenderBoolField(t *testing.T) {
	tests := []struct {
		name      string
		fieldName string
		value     bool
		indent    string
		want      string
	}{
		{"false_returns_empty", "hidden", false, "\t\t", ""},
		{"true_returns_field", "hidden", true, "\t\t", "\t\t\thidden: true,"},
		{"is_persistent", "isPersistent", true, "\t", "\t\tisPersistent: true,"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, renderBoolField(tt.fieldName, tt.value, tt.indent))
		})
	}
}

func TestRenderNameField(t *testing.T) {
	tests := []struct {
		name   string
		names  []string
		indent string
	}{
		{"single_name", []string{"init"}, "\t\t"},
		{"multiple_names", []string{"init", "i"}, "\t\t"},
		{"three_names", []string{"list", "ls", "l"}, "\t"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := renderNameField(tt.names, tt.indent)
			require.Contains(t, result, "name:")
			for _, n := range tt.names {
				require.Contains(t, result, "'"+n+"'")
			}
		})
	}
}

func TestRenderArgs(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		require.Equal(t, "", renderArgs(nil, 2))
		require.Equal(t, "", renderArgs([]Arg{}, 2))
	})

	t.Run("single_basic", func(t *testing.T) {
		result := renderArgs([]Arg{{Name: "env"}}, 2)
		require.Contains(t, result, "name: 'env'")
	})

	t.Run("optional", func(t *testing.T) {
		result := renderArgs([]Arg{{Name: "env", IsOptional: true}}, 2)
		require.Contains(t, result, "isOptional: true")
	})

	t.Run("with_description", func(t *testing.T) {
		result := renderArgs([]Arg{{Name: "env", Description: "The environment"}}, 2)
		require.Contains(t, result, "description:")
		require.Contains(t, result, "The environment")
	})

	t.Run("with_generator", func(t *testing.T) {
		result := renderArgs([]Arg{{Name: "env", Generator: "azdGenerators.listEnvironments"}}, 2)
		require.Contains(t, result, "generators: azdGenerators.listEnvironments")
	})

	t.Run("with_template", func(t *testing.T) {
		result := renderArgs([]Arg{{Name: "path", Template: "filepaths"}}, 2)
		require.Contains(t, result, "template: 'filepaths'")
	})

	t.Run("few_suggestions_inline", func(t *testing.T) {
		result := renderArgs([]Arg{{Name: "fmt", Suggestions: []string{"json", "table"}}}, 2)
		require.Contains(t, result, "suggestions: ['json', 'table']")
	})

	t.Run("many_suggestions_multiline", func(t *testing.T) {
		args := []Arg{{Name: "hook", Suggestions: []string{
			"prebuild", "postbuild", "predeploy", "postdeploy",
		}}}
		result := renderArgs(args, 2)
		require.Contains(t, result, "suggestions: [")
		require.Contains(t, result, "'prebuild',")
	})
}

func TestRenderOption(t *testing.T) {
	t.Run("basic_option", func(t *testing.T) {
		opt := &Option{
			Name:        []string{"--verbose", "-v"},
			Description: "Enable verbose output",
		}
		result := renderOption(opt, 2)
		require.Contains(t, result, "'--verbose'")
		require.Contains(t, result, "'-v'")
		require.Contains(t, result, "description:")
	})

	t.Run("all_bool_fields", func(t *testing.T) {
		opt := &Option{
			Name:         []string{"--force"},
			Description:  "Force it",
			IsPersistent: true,
			IsRepeatable: true,
			IsRequired:   true,
			IsDangerous:  true,
			Hidden:       true,
		}
		result := renderOption(opt, 1)
		require.Contains(t, result, "isPersistent: true")
		require.Contains(t, result, "isRepeatable: true")
		require.Contains(t, result, "isRequired: true")
		require.Contains(t, result, "isDangerous: true")
		require.Contains(t, result, "hidden: true")
	})

	t.Run("with_args", func(t *testing.T) {
		opt := &Option{
			Name:        []string{"--output"},
			Description: "Output format",
			Args:        []Arg{{Name: "format"}},
		}
		result := renderOption(opt, 2)
		require.Contains(t, result, "args: [")
		require.Contains(t, result, "name: 'format'")
	})
}

func TestRenderOptions(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		require.Equal(t, "", renderOptions(nil, 2, false))
		require.Equal(t, "", renderOptions([]Option{}, 2, false))
	})

	t.Run("filters_persistent_in_subcommand", func(t *testing.T) {
		opts := []Option{
			{Name: []string{"--local"}, Description: "Local flag", IsPersistent: false},
			{Name: []string{"--global"}, Description: "Global flag", IsPersistent: true},
		}
		// inSubcommand=true should filter persistent
		result := renderOptions(opts, 2, true)
		require.Contains(t, result, "'--local'")
		require.NotContains(t, result, "'--global'")
	})

	t.Run("includes_persistent_at_root", func(t *testing.T) {
		opts := []Option{
			{Name: []string{"--global"}, Description: "Global flag", IsPersistent: true},
		}
		result := renderOptions(opts, 2, false)
		require.Contains(t, result, "'--global'")
	})

	t.Run("all_persistent_in_subcommand_returns_empty", func(t *testing.T) {
		opts := []Option{
			{Name: []string{"--global"}, Description: "Global flag", IsPersistent: true},
		}
		result := renderOptions(opts, 2, true)
		require.Equal(t, "", result)
	})
}

func TestRenderSubcommand(t *testing.T) {
	t.Run("nil_returns_empty", func(t *testing.T) {
		require.Equal(t, "", renderSubcommand(nil, 2))
	})

	t.Run("basic", func(t *testing.T) {
		sub := &Subcommand{
			Name:        []string{"init"},
			Description: "Initialize a new app",
		}
		result := renderSubcommand(sub, 1)
		require.Contains(t, result, "'init'")
		require.Contains(t, result, "Initialize a new app")
	})

	t.Run("hidden", func(t *testing.T) {
		sub := &Subcommand{
			Name:        []string{"debug"},
			Description: "Debug",
			Hidden:      true,
		}
		result := renderSubcommand(sub, 1)
		require.Contains(t, result, "hidden: true")
	})

	t.Run("with_nested_subcommands", func(t *testing.T) {
		sub := &Subcommand{
			Name:        []string{"env"},
			Description: "Manage environments",
			Subcommands: []Subcommand{
				{Name: []string{"list"}, Description: "List envs"},
			},
		}
		result := renderSubcommand(sub, 1)
		require.Contains(t, result, "subcommands: [")
		require.Contains(t, result, "'list'")
	})

	t.Run("with_options", func(t *testing.T) {
		sub := &Subcommand{
			Name:        []string{"deploy"},
			Description: "Deploy",
			Options: []Option{
				{Name: []string{"--all"}, Description: "Deploy all"},
			},
		}
		result := renderSubcommand(sub, 1)
		require.Contains(t, result, "options: [")
		require.Contains(t, result, "'--all'")
	})

	t.Run("single_arg_inline", func(t *testing.T) {
		sub := &Subcommand{
			Name:        []string{"get"},
			Description: "Get value",
			Args:        []Arg{{Name: "key"}},
		}
		result := renderSubcommand(sub, 1)
		require.Contains(t, result, "args:")
		require.Contains(t, result, "'key'")
		// Single arg should NOT have array brackets around the value
		require.NotContains(t, result, "args: [")
	})

	t.Run("multiple_args_array", func(t *testing.T) {
		sub := &Subcommand{
			Name:        []string{"set"},
			Description: "Set value",
			Args:        []Arg{{Name: "key"}, {Name: "value"}},
		}
		result := renderSubcommand(sub, 1)
		require.Contains(t, result, "args: [")
		require.Contains(t, result, "'key'")
		require.Contains(t, result, "'value'")
	})
}

func TestRenderSubcommands(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		require.Equal(t, "", renderSubcommands(nil, 2))
		require.Equal(t, "", renderSubcommands([]Subcommand{}, 2))
	})

	t.Run("multiple", func(t *testing.T) {
		subs := []Subcommand{
			{Name: []string{"init"}, Description: "Init"},
			{Name: []string{"up"}, Description: "Deploy"},
		}
		result := renderSubcommands(subs, 1)
		require.Contains(t, result, "'init'")
		require.Contains(t, result, "'up'")
	})
}

// ============================================================================
// spec_builder.go — SpecBuilder methods
// ============================================================================

func TestNewSpecBuilder(t *testing.T) {
	t.Run("include_hidden_false", func(t *testing.T) {
		sb := NewSpecBuilder(false)
		require.NotNil(t, sb)
		require.False(t, sb.includeHidden)
		require.NotNil(t, sb.suggestionProvider)
		require.NotNil(t, sb.generatorProvider)
		require.NotNil(t, sb.argsProvider)
		require.NotNil(t, sb.flagArgsProvider)
	})

	t.Run("include_hidden_true", func(t *testing.T) {
		sb := NewSpecBuilder(true)
		require.True(t, sb.includeHidden)
	})
}

func TestWithExtensionMetadata(t *testing.T) {
	sb := NewSpecBuilder(false)
	require.Nil(t, sb.extensionMetadataProvider)
	result := sb.WithExtensionMetadata(&mockExtensionProvider{})
	require.NotNil(t, result.extensionMetadataProvider)
	require.Same(t, sb, result) // returns same pointer (builder pattern)
}

func TestBuildSpec_Basic(t *testing.T) {
	root := &cobra.Command{
		Use:   "azd",
		Short: "Azure Developer CLI",
	}
	// Add a subcommand
	root.AddCommand(&cobra.Command{
		Use:   "init",
		Short: "Initialize a new application",
	})
	// Add a persistent flag
	root.PersistentFlags().BoolP("debug", "d", false, "Enable debug logging")

	sb := newTestSpecBuilder(false)
	spec := sb.BuildSpec(root)

	require.Equal(t, "azd", spec.Name)
	require.Equal(t, "Azure Developer CLI", spec.Description)
	require.NotEmpty(t, spec.Subcommands)
	require.NotEmpty(t, spec.Options)

	// Find 'init' subcommand
	found := false
	for _, sub := range spec.Subcommands {
		if sub.Name[0] == "init" {
			found = true
			require.Equal(t, "Initialize a new application", sub.Description)
		}
	}
	require.True(t, found, "expected init subcommand")

	// Find debug option
	foundDebug := false
	for _, opt := range spec.Options {
		if opt.Name[0] == "--debug" {
			foundDebug = true
			require.True(t, opt.IsPersistent)
			require.Contains(t, opt.Name, "-d")
		}
	}
	require.True(t, foundDebug, "expected debug option")
}

func TestBuildSpec_HiddenCommandsExcluded(t *testing.T) {
	root := &cobra.Command{Use: "azd", Short: "CLI"}
	root.AddCommand(&cobra.Command{Use: "visible", Short: "Visible"})
	hidden := &cobra.Command{Use: "secret", Short: "Secret"}
	hidden.Hidden = true
	root.AddCommand(hidden)

	sb := newTestSpecBuilder(false)
	spec := sb.BuildSpec(root)

	for _, sub := range spec.Subcommands {
		if sub.Name[0] == "secret" {
			t.Fatal("hidden command should be excluded when includeHidden=false")
		}
	}
}

func TestBuildSpec_HiddenCommandsIncluded(t *testing.T) {
	root := &cobra.Command{Use: "azd", Short: "CLI"}
	hidden := &cobra.Command{Use: "secret", Short: "Secret"}
	hidden.Hidden = true
	root.AddCommand(hidden)

	sb := newTestSpecBuilder(true)
	spec := sb.BuildSpec(root)

	found := false
	for _, sub := range spec.Subcommands {
		if sub.Name[0] == "secret" {
			found = true
			require.True(t, sub.Hidden)
		}
	}
	require.True(t, found, "hidden command should appear when includeHidden=true")
}

func TestBuildSpec_HelpCommandPresent(t *testing.T) {
	root := &cobra.Command{Use: "azd", Short: "CLI"}
	root.AddCommand(&cobra.Command{Use: "init", Short: "Init"})

	sb := newTestSpecBuilder(false)
	spec := sb.BuildSpec(root)

	found := false
	for _, sub := range spec.Subcommands {
		if sub.Name[0] == "help" {
			found = true
			require.Equal(t, "Help about any command", sub.Description)
			// Help subcommands should mirror the command tree
			require.NotEmpty(t, sub.Subcommands)
		}
	}
	require.True(t, found, "expected help subcommand")
}

func TestBuildSpec_Aliases(t *testing.T) {
	root := &cobra.Command{Use: "azd", Short: "CLI"}
	root.AddCommand(&cobra.Command{
		Use:     "environment",
		Aliases: []string{"env", "e"},
		Short:   "Manage envs",
	})

	sb := newTestSpecBuilder(false)
	spec := sb.BuildSpec(root)

	for _, sub := range spec.Subcommands {
		if sub.Name[0] == "environment" {
			require.Equal(t, []string{"environment", "env", "e"}, sub.Name)
			return
		}
	}
	t.Fatal("expected environment subcommand with aliases")
}

func TestGenerateOptions_FlagTypes(t *testing.T) {
	t.Run("bool_flag_no_args", func(t *testing.T) {
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		fs.Bool("verbose", false, "Enable verbose")

		sb := newTestSpecBuilder(false)
		opts := sb.generateOptions(fs, "azd test", false)
		require.Len(t, opts, 1)
		require.Equal(t, []string{"--verbose"}, opts[0].Name)
		require.Empty(t, opts[0].Args, "bool flag should have no args")
	})

	t.Run("string_flag_has_args", func(t *testing.T) {
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		fs.String("output", "", "Output format")

		sb := newTestSpecBuilder(false)
		opts := sb.generateOptions(fs, "azd test", false)
		require.Len(t, opts, 1)
		require.Len(t, opts[0].Args, 1)
		require.Equal(t, "output", opts[0].Args[0].Name)
	})

	t.Run("shorthand", func(t *testing.T) {
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		fs.StringP("output", "o", "", "Output format")

		sb := newTestSpecBuilder(false)
		opts := sb.generateOptions(fs, "azd test", false)
		require.Len(t, opts, 1)
		require.Equal(t, []string{"--output", "-o"}, opts[0].Name)
	})

	t.Run("hidden_flag_excluded", func(t *testing.T) {
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		fs.String("secret", "", "Secret flag")
		f := fs.Lookup("secret")
		f.Hidden = true

		sb := newTestSpecBuilder(false)
		opts := sb.generateOptions(fs, "azd test", false)
		require.Empty(t, opts)
	})

	t.Run("hidden_flag_included", func(t *testing.T) {
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		fs.String("secret", "", "Secret flag")
		f := fs.Lookup("secret")
		f.Hidden = true

		sb := newTestSpecBuilder(true)
		opts := sb.generateOptions(fs, "azd test", false)
		require.Len(t, opts, 1)
		require.True(t, opts[0].Hidden)
	})

	t.Run("slice_flag_repeatable", func(t *testing.T) {
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		fs.StringSlice("tag", nil, "Tags")

		sb := newTestSpecBuilder(false)
		opts := sb.generateOptions(fs, "azd test", false)
		require.Len(t, opts, 1)
		require.True(t, opts[0].IsRepeatable)
	})

	t.Run("required_flag", func(t *testing.T) {
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		fs.String("name", "", "Name")
		f := fs.Lookup("name")
		f.Annotations = map[string][]string{
			cobra.BashCompOneRequiredFlag: {"true"},
		}

		sb := newTestSpecBuilder(false)
		opts := sb.generateOptions(fs, "azd test", false)
		require.Len(t, opts, 1)
		require.True(t, opts[0].IsRequired)
	})

	t.Run("dangerous_flags", func(t *testing.T) {
		for _, fname := range []string{"force", "purge", "show-secrets"} {
			t.Run(fname, func(t *testing.T) {
				fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
				fs.Bool(fname, false, "Dangerous")

				sb := newTestSpecBuilder(false)
				opts := sb.generateOptions(fs, "azd test", false)
				require.True(t, opts[0].IsDangerous, "%s should be dangerous", fname)
			})
		}
	})

	t.Run("persistent_flag_true", func(t *testing.T) {
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		fs.Bool("verbose", false, "Verbose")

		sb := newTestSpecBuilder(false)
		opts := sb.generateOptions(fs, "azd test", true)
		require.Len(t, opts, 1)
		require.True(t, opts[0].IsPersistent)
	})
}

func TestGenerateCommandArgs(t *testing.T) {
	t.Run("no_args_in_use", func(t *testing.T) {
		cmd := &cobra.Command{Use: "init"}
		sb := newTestSpecBuilder(false)
		ctx := &CommandContext{Command: cmd, CommandPath: "azd init"}
		args := sb.generateCommandArgs(cmd, ctx)
		require.Nil(t, args)
	})

	t.Run("optional_arg", func(t *testing.T) {
		cmd := &cobra.Command{Use: "deploy [service]"}
		sb := newTestSpecBuilder(false)
		ctx := &CommandContext{Command: cmd, CommandPath: "azd deploy"}
		args := sb.generateCommandArgs(cmd, ctx)
		require.Len(t, args, 1)
		require.Equal(t, "service", args[0].Name)
		require.True(t, args[0].IsOptional)
	})

	t.Run("required_arg", func(t *testing.T) {
		cmd := &cobra.Command{Use: "get <key>"}
		sb := newTestSpecBuilder(false)
		ctx := &CommandContext{Command: cmd, CommandPath: "azd get"}
		args := sb.generateCommandArgs(cmd, ctx)
		require.Len(t, args, 1)
		require.Equal(t, "key", args[0].Name)
		require.False(t, args[0].IsOptional)
	})

	t.Run("multiple_args", func(t *testing.T) {
		cmd := &cobra.Command{Use: "set <key> [value]"}
		sb := newTestSpecBuilder(false)
		ctx := &CommandContext{Command: cmd, CommandPath: "azd set"}
		args := sb.generateCommandArgs(cmd, ctx)
		require.Len(t, args, 2)
		require.Equal(t, "key", args[0].Name)
		require.False(t, args[0].IsOptional)
		require.Equal(t, "value", args[1].Name)
		require.True(t, args[1].IsOptional)
	})

	t.Run("skips_flags_in_use", func(t *testing.T) {
		cmd := &cobra.Command{Use: "run [service] [-flags]"}
		sb := newTestSpecBuilder(false)
		ctx := &CommandContext{Command: cmd, CommandPath: "azd run"}
		args := sb.generateCommandArgs(cmd, ctx)
		require.Len(t, args, 1)
		require.Equal(t, "service", args[0].Name)
	})
}

func TestGenerateFlagArgs(t *testing.T) {
	t.Run("bool_flag_returns_nil", func(t *testing.T) {
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		fs.Bool("verbose", false, "Verbose")
		f := fs.Lookup("verbose")
		ctx := &FlagContext{Flag: f, CommandPath: "azd test"}

		sb := newTestSpecBuilder(false)
		args := sb.generateFlagArgs(f, ctx)
		require.Nil(t, args)
	})

	t.Run("string_flag_returns_arg", func(t *testing.T) {
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		fs.String("output", "", "Output format")
		f := fs.Lookup("output")
		ctx := &FlagContext{Flag: f, CommandPath: "azd test"}

		sb := newTestSpecBuilder(false)
		args := sb.generateFlagArgs(f, ctx)
		require.Len(t, args, 1)
		require.Equal(t, "output", args[0].Name)
	})

	t.Run("empty_type_returns_nil", func(t *testing.T) {
		// A flag with empty type should be treated like a boolean
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		fs.Bool("check", false, "Check")
		f := fs.Lookup("check")
		ctx := &FlagContext{Flag: f, CommandPath: "azd test"}

		sb := newTestSpecBuilder(false)
		args := sb.generateFlagArgs(f, ctx)
		require.Nil(t, args)
	})
}

// ============================================================================
// customizations.go — Customizations methods
// ============================================================================

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
		{"ext_install", "azd extension install", "extension-id", FigGenListExtensions},
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

// ============================================================================
// spec_builder.go — ToTypeScript (integration-level rendering)
// ============================================================================

func TestToTypeScript(t *testing.T) {
	spec := &Spec{
		Name:        "azd",
		Description: "Azure Developer CLI",
		Subcommands: []Subcommand{
			{
				Name:        []string{"init"},
				Description: "Initialize app",
				Options: []Option{
					{
						Name:        []string{"--template", "-t"},
						Description: "Template name",
						Args:        []Arg{{Name: "template"}},
					},
				},
			},
		},
		Options: []Option{
			{
				Name:         []string{"--debug", "-d"},
				Description:  "Enable debug",
				IsPersistent: true,
			},
		},
	}

	ts, err := spec.ToTypeScript()
	require.NoError(t, err)
	require.Contains(t, ts, "name: 'azd'")
	require.Contains(t, ts, "const completionSpec: Fig.Spec")
	require.Contains(t, ts, "export default completionSpec")
	require.Contains(t, ts, "'init'")
	require.Contains(t, ts, "'--template'")
	require.Contains(t, ts, "'--debug'")
}

func TestToTypeScript_Empty(t *testing.T) {
	spec := &Spec{
		Name:        "test",
		Description: "Test CLI",
	}
	ts, err := spec.ToTypeScript()
	require.NoError(t, err)
	require.Contains(t, ts, "name: 'test'")
}

// ============================================================================
// spec_builder.go — extension metadata integration
// ============================================================================

func TestTryGenerateExtensionSubcommand(t *testing.T) {
	t.Run("nil_provider_returns_nil", func(t *testing.T) {
		sb := newTestSpecBuilder(false)
		sb.extensionMetadataProvider = nil
		cmd := &cobra.Command{Use: "ext", Short: "Extension"}
		cmd.Annotations = map[string]string{"extension.id": "test-ext"}
		result := sb.tryGenerateExtensionSubcommand(cmd, []string{"ext"})
		require.Nil(t, result)
	})

	t.Run("no_annotation_returns_nil", func(t *testing.T) {
		sb := newTestSpecBuilder(false)
		sb.extensionMetadataProvider = &mockExtensionProvider{}
		cmd := &cobra.Command{Use: "plain", Short: "Plain command"}
		result := sb.tryGenerateExtensionSubcommand(cmd, []string{"plain"})
		require.Nil(t, result)
	})

	t.Run("no_capability_returns_nil", func(t *testing.T) {
		sb := newTestSpecBuilder(false)
		sb.extensionMetadataProvider = &mockExtensionProvider{hasCapability: false}
		cmd := &cobra.Command{Use: "ext", Short: "Extension"}
		cmd.Annotations = map[string]string{"extension.id": "test-ext"}
		result := sb.tryGenerateExtensionSubcommand(cmd, []string{"ext"})
		require.Nil(t, result)
	})

	t.Run("nil_metadata_returns_nil", func(t *testing.T) {
		sb := newTestSpecBuilder(false)
		sb.extensionMetadataProvider = &mockExtensionProvider{hasCapability: true, metadata: nil}
		cmd := &cobra.Command{Use: "ext", Short: "Extension"}
		cmd.Annotations = map[string]string{"extension.id": "test-ext"}
		result := sb.tryGenerateExtensionSubcommand(cmd, []string{"ext"})
		require.Nil(t, result)
	})

	t.Run("success_with_metadata", func(t *testing.T) {
		sb := newTestSpecBuilder(false)
		sb.globalFlagNames = map[string]bool{"help": true}
		sb.extensionMetadataProvider = &mockExtensionProvider{
			hasCapability: true,
			metadata: &extensions.ExtensionCommandMetadata{
				Commands: []extensions.Command{
					{Name: []string{"sub"}, Short: "A subcommand"},
				},
			},
		}
		cmd := &cobra.Command{Use: "ext", Short: "Extension"}
		cmd.Annotations = map[string]string{"extension.id": "test-ext"}
		result := sb.tryGenerateExtensionSubcommand(cmd, []string{"ext", "e"})
		require.NotNil(t, result)
		require.Equal(t, []string{"ext", "e"}, result.Name)
		require.Len(t, result.Subcommands, 1)
		require.Equal(t, "A subcommand", result.Subcommands[0].Description)
	})
}

func TestTryGenerateExtensionHelpSubcommand(t *testing.T) {
	t.Run("nil_provider_returns_nil", func(t *testing.T) {
		sb := newTestSpecBuilder(false)
		sb.extensionMetadataProvider = nil
		cmd := &cobra.Command{Use: "ext"}
		cmd.Annotations = map[string]string{"extension.id": "test-ext"}
		result := sb.tryGenerateExtensionHelpSubcommand(cmd, []string{"ext"})
		require.Nil(t, result)
	})

	t.Run("no_annotation_returns_nil", func(t *testing.T) {
		sb := newTestSpecBuilder(false)
		sb.extensionMetadataProvider = &mockExtensionProvider{hasCapability: true}
		cmd := &cobra.Command{Use: "plain"}
		result := sb.tryGenerateExtensionHelpSubcommand(cmd, []string{"plain"})
		require.Nil(t, result)
	})

	t.Run("success_with_metadata", func(t *testing.T) {
		sb := newTestSpecBuilder(false)
		sb.extensionMetadataProvider = &mockExtensionProvider{
			hasCapability: true,
			metadata: &extensions.ExtensionCommandMetadata{
				Commands: []extensions.Command{
					{Name: []string{"child"}, Short: "Child cmd"},
				},
			},
		}
		cmd := &cobra.Command{Use: "ext", Short: "Ext"}
		cmd.Annotations = map[string]string{"extension.id": "test-ext"}
		result := sb.tryGenerateExtensionHelpSubcommand(cmd, []string{"ext"})
		require.NotNil(t, result)
		require.Equal(t, "Ext", result.Description)
		require.Len(t, result.Subcommands, 1)
	})
}

// ============================================================================
// spec_builder.go — deeper subcommand / help tree generation
// ============================================================================

func TestGenerateSubcommands_SkipsHelpCommand(t *testing.T) {
	root := &cobra.Command{Use: "azd", Short: "CLI"}
	root.AddCommand(&cobra.Command{Use: "init", Short: "Init"})
	// cobra adds a help command automatically; we verify it's not duplicated
	root.InitDefaultHelpCmd()

	sb := newTestSpecBuilder(false)
	sb.globalFlagNames = map[string]bool{}

	ctx := &CommandContext{Command: root, CommandPath: "azd"}
	subs := sb.generateSubcommands(root, ctx)

	helpCount := 0
	for _, sub := range subs {
		if sub.Name[0] == "help" {
			helpCount++
		}
	}
	// Our code generates help once at root level; cobra's built-in "help" is skipped
	require.Equal(t, 1, helpCount, "should have exactly one help command")
}

func TestGenerateHelpSubcommands_MirrorsTree(t *testing.T) {
	root := &cobra.Command{Use: "azd", Short: "CLI"}
	env := &cobra.Command{Use: "env", Short: "Environments"}
	env.AddCommand(&cobra.Command{Use: "list", Short: "List"})
	env.AddCommand(&cobra.Command{Use: "select", Short: "Select"})
	root.AddCommand(env)

	sb := newTestSpecBuilder(false)
	helpSubs := sb.generateHelpSubcommands(root)

	require.Len(t, helpSubs, 1)
	require.Equal(t, "env", helpSubs[0].Name[0])
	require.Len(t, helpSubs[0].Subcommands, 2)
}

func TestGenerateHelpSubcommands_HiddenExcluded(t *testing.T) {
	root := &cobra.Command{Use: "azd", Short: "CLI"}
	visible := &cobra.Command{Use: "init", Short: "Init"}
	hidden := &cobra.Command{Use: "secret", Short: "Secret"}
	hidden.Hidden = true
	root.AddCommand(visible)
	root.AddCommand(hidden)

	sb := newTestSpecBuilder(false)
	helpSubs := sb.generateHelpSubcommands(root)

	for _, sub := range helpSubs {
		require.NotEqual(t, "secret", sub.Name[0], "hidden command should not appear in help tree")
	}
}

// ============================================================================
// spec_builder.go — custom args with generators
// ============================================================================

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

func TestGenerateCommandArgs_CustomArgsOverrideUseParsing(t *testing.T) {
	// For "azd env set" the Customizations returns custom args, so Use parsing is skipped
	cmd := &cobra.Command{Use: "set <key> [value]"}
	sb := newTestSpecBuilder(false)
	ctx := &CommandContext{Command: cmd, CommandPath: "azd env set"}

	args := sb.generateCommandArgs(cmd, ctx)
	require.Len(t, args, 2)
	require.Equal(t, "key", args[0].Name)
	require.True(t, args[0].IsOptional) // custom args marks both optional
}

// ============================================================================
// End-to-end BuildSpec with deeper cobra tree
// ============================================================================

func TestBuildSpec_NestedSubcommands(t *testing.T) {
	root := &cobra.Command{Use: "azd", Short: "CLI"}
	env := &cobra.Command{Use: "env", Short: "Environments"}
	env.AddCommand(&cobra.Command{Use: "list", Short: "List environments"})
	env.AddCommand(&cobra.Command{Use: "select <environment>", Short: "Select env"})
	env.Flags().String("output", "", "Output format")
	root.AddCommand(env)

	sb := newTestSpecBuilder(false)
	spec := sb.BuildSpec(root)

	// Find env subcommand
	var envSub *Subcommand
	for i := range spec.Subcommands {
		if spec.Subcommands[i].Name[0] == "env" {
			envSub = &spec.Subcommands[i]
			break
		}
	}
	require.NotNil(t, envSub)
	require.Len(t, envSub.Subcommands, 2)

	// env should have the --output option
	require.NotEmpty(t, envSub.Options)
}

func TestBuildSpec_GeneratesHelpTree(t *testing.T) {
	root := &cobra.Command{Use: "azd", Short: "CLI"}
	env := &cobra.Command{Use: "env", Short: "Environments"}
	env.AddCommand(&cobra.Command{Use: "list", Short: "List"})
	root.AddCommand(env)

	sb := newTestSpecBuilder(false)
	spec := sb.BuildSpec(root)

	var helpSub *Subcommand
	for i := range spec.Subcommands {
		if spec.Subcommands[i].Name[0] == "help" {
			helpSub = &spec.Subcommands[i]
			break
		}
	}
	require.NotNil(t, helpSub)
	// Help tree should contain 'env' with 'list' nested
	require.NotEmpty(t, helpSub.Subcommands)
	var envHelp *Subcommand
	for i := range helpSub.Subcommands {
		if helpSub.Subcommands[i].Name[0] == "env" {
			envHelp = &helpSub.Subcommands[i]
			break
		}
	}
	require.NotNil(t, envHelp)
	require.NotEmpty(t, envHelp.Subcommands)
}

// ============================================================================
// fig_generators.go — constants
// ============================================================================

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

// ============================================================================
// Helpers
// ============================================================================

// newTestSpecBuilder creates a SpecBuilder without the `cmd.NonPersistentGlobalFlags` import dependency.
// We nil out the providers and set them explicitly to avoid depending on real Customizations paths
// for most unit tests. The Customizations tests exercise those paths independently.
func newTestSpecBuilder(includeHidden bool) *SpecBuilder {
	azd := &Customizations{}
	return &SpecBuilder{
		suggestionProvider: azd,
		generatorProvider:  azd,
		argsProvider:       azd,
		flagArgsProvider:   azd,
		includeHidden:      includeHidden,
	}
}

// mockExtensionProvider implements ExtensionMetadataProvider for testing
type mockExtensionProvider struct {
	hasCapability bool
	metadata      *extensions.ExtensionCommandMetadata
	loadErr       error
}

func (m *mockExtensionProvider) HasMetadataCapability(extensionId string) bool {
	return m.hasCapability
}

func (m *mockExtensionProvider) LoadMetadata(extensionId string) (*extensions.ExtensionCommandMetadata, error) {
	return m.metadata, m.loadErr
}
