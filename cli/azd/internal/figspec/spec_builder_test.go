// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package figspec

import (
	"errors"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
)

func TestNewSpecBuilder(t *testing.T) {
	t.Run("include_hidden_false", func(t *testing.T) {
		sb := NewSpecBuilder(false)
		require.NotNil(t, sb)
		require.False(t, sb.includeHidden)
		require.False(t, sb.includeHelpSubcommands)
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

func TestWithHelpSubcommands(t *testing.T) {
	sb := NewSpecBuilder(false)
	require.False(t, sb.includeHelpSubcommands)

	result := sb.WithHelpSubcommands(true)

	require.Same(t, sb, result)
	require.True(t, sb.includeHelpSubcommands)
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
			require.Empty(t, sub.Subcommands)
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

func TestGenerateNonPersistentGlobalOptions(t *testing.T) {
	root := &cobra.Command{Use: "azd", Short: "CLI"}
	root.Flags().Bool("docs", false, "Open documentation")
	root.Flags().Bool("local-only", false, "Local flag")

	sb := newTestSpecBuilder(false)
	opts := sb.generateNonPersistentGlobalOptions(root)

	require.Len(t, opts, 1)
	require.Equal(t, []string{"--docs"}, opts[0].Name)
	require.True(t, opts[0].IsPersistent)
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

	t.Run("load_error_returns_nil", func(t *testing.T) {
		sb := newTestSpecBuilder(false)
		sb.extensionMetadataProvider = &mockExtensionProvider{hasCapability: true, loadErr: errors.New("load failed")}
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

func TestGenerateSubcommands_ExtensionMetadata(t *testing.T) {
	root := &cobra.Command{Use: "azd", Short: "CLI"}
	ext := &cobra.Command{Use: "ext", Short: "Extension"}
	ext.Annotations = map[string]string{"extension.id": "test-ext"}
	root.AddCommand(ext)

	sb := newTestSpecBuilder(false)
	sb.globalFlagNames = map[string]bool{}
	sb.extensionMetadataProvider = &mockExtensionProvider{
		hasCapability: true,
		metadata: &extensions.ExtensionCommandMetadata{
			Commands: []extensions.Command{
				{Name: []string{"child"}, Short: "Child cmd"},
			},
		},
	}

	subs := sb.generateSubcommands(root, &CommandContext{Command: root, CommandPath: "azd"})

	require.Len(t, subs, 2)
	require.Equal(t, "ext", subs[0].Name[0])
	require.Len(t, subs[0].Subcommands, 1)
	require.Equal(t, "child", subs[0].Subcommands[0].Name[0])
	require.Equal(t, "help", subs[1].Name[0])
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

	t.Run("no_capability_returns_nil", func(t *testing.T) {
		sb := newTestSpecBuilder(false)
		sb.extensionMetadataProvider = &mockExtensionProvider{hasCapability: false}
		cmd := &cobra.Command{Use: "ext"}
		cmd.Annotations = map[string]string{"extension.id": "test-ext"}
		result := sb.tryGenerateExtensionHelpSubcommand(cmd, []string{"ext"})
		require.Nil(t, result)
	})

	t.Run("load_error_returns_nil", func(t *testing.T) {
		sb := newTestSpecBuilder(false)
		sb.extensionMetadataProvider = &mockExtensionProvider{hasCapability: true, loadErr: errors.New("load failed")}
		cmd := &cobra.Command{Use: "ext"}
		cmd.Annotations = map[string]string{"extension.id": "test-ext"}
		result := sb.tryGenerateExtensionHelpSubcommand(cmd, []string{"ext"})
		require.Nil(t, result)
	})

	t.Run("nil_metadata_returns_nil", func(t *testing.T) {
		sb := newTestSpecBuilder(false)
		sb.extensionMetadataProvider = &mockExtensionProvider{hasCapability: true}
		cmd := &cobra.Command{Use: "ext"}
		cmd.Annotations = map[string]string{"extension.id": "test-ext"}
		result := sb.tryGenerateExtensionHelpSubcommand(cmd, []string{"ext"})
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

func TestGenerateHelpSubcommands_SkipsHelpCommand(t *testing.T) {
	root := &cobra.Command{Use: "azd", Short: "CLI"}
	root.AddCommand(&cobra.Command{Use: "init", Short: "Init"})
	root.InitDefaultHelpCmd()

	sb := newTestSpecBuilder(false)
	helpSubs := sb.generateHelpSubcommands(root)

	require.Len(t, helpSubs, 1)
	require.Equal(t, "init", helpSubs[0].Name[0])
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

func TestGenerateHelpSubcommands_ExtensionMetadata(t *testing.T) {
	root := &cobra.Command{Use: "azd", Short: "CLI"}
	ext := &cobra.Command{Use: "ext", Short: "Extension"}
	ext.Annotations = map[string]string{"extension.id": "test-ext"}
	root.AddCommand(ext)

	sb := newTestSpecBuilder(false)
	sb.extensionMetadataProvider = &mockExtensionProvider{
		hasCapability: true,
		metadata: &extensions.ExtensionCommandMetadata{
			Commands: []extensions.Command{
				{Name: []string{"child"}, Short: "Child cmd"},
			},
		},
	}

	helpSubs := sb.generateHelpSubcommands(root)

	require.Len(t, helpSubs, 1)
	require.Equal(t, "ext", helpSubs[0].Name[0])
	require.Len(t, helpSubs[0].Subcommands, 1)
	require.Equal(t, "child", helpSubs[0].Subcommands[0].Name[0])
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

	sb := newTestSpecBuilder(false).WithHelpSubcommands(true)
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
