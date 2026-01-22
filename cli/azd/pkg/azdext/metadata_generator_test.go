// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateExtensionMetadata(t *testing.T) {
	rootCmd := &cobra.Command{
		Use:   "test-ext",
		Short: "Test extension",
	}

	greetCmd := &cobra.Command{
		Use:   "greet [name]",
		Short: "Greet someone",
		Long:  "This command greets someone with a friendly message.",
	}
	greetCmd.Flags().StringP("format", "f", "text", "Output format")
	greetCmd.Flags().BoolP("verbose", "v", false, "Verbose output")

	rootCmd.AddCommand(greetCmd)

	metadata := GenerateExtensionMetadata("1.0", "test.extension", rootCmd)

	require.NotNil(t, metadata)
	assert.Equal(t, "1.0", metadata.SchemaVersion)
	assert.Equal(t, "test.extension", metadata.ID)
	assert.Len(t, metadata.Commands, 1)

	cmd := metadata.Commands[0]
	assert.Equal(t, []string{"greet"}, cmd.Name)
	assert.Equal(t, "Greet someone", cmd.Short)
	assert.Equal(t, "This command greets someone with a friendly message.", cmd.Long)
	assert.Len(t, cmd.Flags, 3) // format, verbose, and auto-generated help flag

	// Check help flag is included
	helpFlag := findFlag(cmd.Flags, "help")
	require.NotNil(t, helpFlag)
	assert.Equal(t, "h", helpFlag.Shorthand)
	assert.Equal(t, "bool", helpFlag.Type)

	// Check flags
	formatFlag := findFlag(cmd.Flags, "format")
	require.NotNil(t, formatFlag)
	assert.Equal(t, "f", formatFlag.Shorthand)
	assert.Equal(t, "string", formatFlag.Type)
	assert.Equal(t, "text", formatFlag.Default)

	verboseFlag := findFlag(cmd.Flags, "verbose")
	require.NotNil(t, verboseFlag)
	assert.Equal(t, "v", verboseFlag.Shorthand)
	assert.Equal(t, "bool", verboseFlag.Type)
}

func TestGenerateExtensionMetadata_NestedCommands(t *testing.T) {
	rootCmd := &cobra.Command{
		Use:   "test-ext",
		Short: "Test extension",
	}

	demoCmd := &cobra.Command{
		Use:   "demo",
		Short: "Demo commands",
	}

	greetCmd := &cobra.Command{
		Use:   "greet",
		Short: "Greet command",
	}

	farewellCmd := &cobra.Command{
		Use:   "farewell",
		Short: "Farewell command",
	}

	demoCmd.AddCommand(greetCmd)
	demoCmd.AddCommand(farewellCmd)
	rootCmd.AddCommand(demoCmd)

	metadata := GenerateExtensionMetadata("1.0", "test.extension", rootCmd)

	require.Len(t, metadata.Commands, 1)
	assert.Equal(t, []string{"demo"}, metadata.Commands[0].Name)
	assert.Len(t, metadata.Commands[0].Subcommands, 2)

	// Find subcommands by name (order may vary)
	var greet, farewell *extensions.Command
	for i := range metadata.Commands[0].Subcommands {
		if metadata.Commands[0].Subcommands[i].Name[1] == "greet" {
			greet = &metadata.Commands[0].Subcommands[i]
		} else if metadata.Commands[0].Subcommands[i].Name[1] == "farewell" {
			farewell = &metadata.Commands[0].Subcommands[i]
		}
	}

	require.NotNil(t, greet)
	assert.Equal(t, []string{"demo", "greet"}, greet.Name)

	require.NotNil(t, farewell)
	assert.Equal(t, []string{"demo", "farewell"}, farewell.Name)
}

func TestGenerateExtensionMetadata_HiddenCommands(t *testing.T) {
	rootCmd := &cobra.Command{
		Use:   "test-ext",
		Short: "Test extension",
	}

	visibleCmd := &cobra.Command{
		Use:   "visible",
		Short: "Visible command",
	}

	hiddenCmd := &cobra.Command{
		Use:    "hidden",
		Short:  "Hidden command",
		Hidden: true,
	}

	rootCmd.AddCommand(visibleCmd)
	rootCmd.AddCommand(hiddenCmd)

	metadata := GenerateExtensionMetadata("1.0", "test.extension", rootCmd)

	// Both commands should be included; hidden commands have Hidden=true
	assert.Len(t, metadata.Commands, 2)

	visibleFound := findCommand(metadata.Commands, "visible")
	require.NotNil(t, visibleFound)
	assert.False(t, visibleFound.Hidden)

	hiddenFound := findCommand(metadata.Commands, "hidden")
	require.NotNil(t, hiddenFound)
	assert.True(t, hiddenFound.Hidden)
}

func TestGenerateExtensionMetadata_HiddenFlags(t *testing.T) {
	rootCmd := &cobra.Command{
		Use:   "test-ext",
		Short: "Test extension",
	}

	testCmd := &cobra.Command{
		Use:   "test",
		Short: "Test command",
	}
	testCmd.Flags().String("visible", "", "Visible flag")
	testCmd.Flags().String("hidden", "", "Hidden flag")
	testCmd.Flags().MarkHidden("hidden")

	rootCmd.AddCommand(testCmd)

	metadata := GenerateExtensionMetadata("1.0", "test.extension", rootCmd)

	require.Len(t, metadata.Commands, 1)
	// Both flags should be included; hidden flags have Hidden=true
	// Also includes auto-generated help flag
	assert.Len(t, metadata.Commands[0].Flags, 3)

	helpFlag := findFlag(metadata.Commands[0].Flags, "help")
	require.NotNil(t, helpFlag)

	visibleFlag := findFlag(metadata.Commands[0].Flags, "visible")
	require.NotNil(t, visibleFlag)
	assert.False(t, visibleFlag.Hidden)

	hiddenFlag := findFlag(metadata.Commands[0].Flags, "hidden")
	require.NotNil(t, hiddenFlag)
	assert.True(t, hiddenFlag.Hidden)
}

func TestGenerateExtensionMetadata_DeprecatedCommands(t *testing.T) {
	rootCmd := &cobra.Command{
		Use:   "test-ext",
		Short: "Test extension",
	}

	deprecatedCmd := &cobra.Command{
		Use:        "old-command",
		Short:      "Old command",
		Deprecated: "use 'new-command' instead",
	}

	rootCmd.AddCommand(deprecatedCmd)

	metadata := GenerateExtensionMetadata("1.0", "test.extension", rootCmd)

	require.Len(t, metadata.Commands, 1)
	assert.Equal(t, "use 'new-command' instead", metadata.Commands[0].Deprecated)
}

func TestGenerateExtensionMetadata_Aliases(t *testing.T) {
	rootCmd := &cobra.Command{
		Use:   "test-ext",
		Short: "Test extension",
	}

	cmdWithAliases := &cobra.Command{
		Use:     "command",
		Short:   "Command with aliases",
		Aliases: []string{"cmd", "c"},
	}

	rootCmd.AddCommand(cmdWithAliases)

	metadata := GenerateExtensionMetadata("1.0", "test.extension", rootCmd)

	require.Len(t, metadata.Commands, 1)
	assert.Equal(t, []string{"cmd", "c"}, metadata.Commands[0].Aliases)
}

func TestGenerateExtensionMetadata_Examples(t *testing.T) {
	rootCmd := &cobra.Command{
		Use:   "test-ext",
		Short: "Test extension",
	}

	cmdWithExamples := &cobra.Command{
		Use:     "command",
		Short:   "Command with examples",
		Example: "azd x test-ext command --flag value",
	}

	rootCmd.AddCommand(cmdWithExamples)

	metadata := GenerateExtensionMetadata("1.0", "test.extension", rootCmd)

	require.Len(t, metadata.Commands, 1)
	require.Len(t, metadata.Commands[0].Examples, 1)
	assert.Equal(t, "azd x test-ext command --flag value", metadata.Commands[0].Examples[0].Command)
}

func TestGenerateExtensionMetadata_FlagTypes(t *testing.T) {
	rootCmd := &cobra.Command{
		Use:   "test-ext",
		Short: "Test extension",
	}

	testCmd := &cobra.Command{
		Use:   "test",
		Short: "Test command",
	}

	testCmd.Flags().String("string-flag", "", "A string flag")
	testCmd.Flags().Bool("bool-flag", false, "A bool flag")
	testCmd.Flags().Int("int-flag", 0, "An int flag")
	testCmd.Flags().StringSlice("string-slice-flag", []string{}, "A string slice flag")
	testCmd.Flags().IntSlice("int-slice-flag", []int{}, "An int slice flag")

	rootCmd.AddCommand(testCmd)

	metadata := GenerateExtensionMetadata("1.0", "test.extension", rootCmd)

	require.Len(t, metadata.Commands, 1)
	flags := metadata.Commands[0].Flags

	stringFlag := findFlag(flags, "string-flag")
	require.NotNil(t, stringFlag)
	assert.Equal(t, "string", stringFlag.Type)

	boolFlag := findFlag(flags, "bool-flag")
	require.NotNil(t, boolFlag)
	assert.Equal(t, "bool", boolFlag.Type)

	intFlag := findFlag(flags, "int-flag")
	require.NotNil(t, intFlag)
	assert.Equal(t, "int", intFlag.Type)

	stringSliceFlag := findFlag(flags, "string-slice-flag")
	require.NotNil(t, stringSliceFlag)
	assert.Equal(t, "stringArray", stringSliceFlag.Type)

	intSliceFlag := findFlag(flags, "int-slice-flag")
	require.NotNil(t, intSliceFlag)
	assert.Equal(t, "intArray", intSliceFlag.Type)
}

// Helper function to find a flag by name
func findFlag(flags []extensions.Flag, name string) *extensions.Flag {
	for i := range flags {
		if flags[i].Name == name {
			return &flags[i]
		}
	}
	return nil
}

// Helper function to find a command by name (first element of Name path)
func findCommand(commands []extensions.Command, name string) *extensions.Command {
	for i := range commands {
		if len(commands[i].Name) > 0 && commands[i].Name[0] == name {
			return &commands[i]
		}
	}
	return nil
}

func TestGenerateExtensionMetadata_SkipsEmptyCommands(t *testing.T) {
	rootCmd := &cobra.Command{
		Use:   "test-ext",
		Short: "Test extension",
	}

	// Add a normal command
	normalCmd := &cobra.Command{
		Use:   "normal",
		Short: "Normal command",
	}
	rootCmd.AddCommand(normalCmd)

	// Add a command with empty Use (simulating auto-generated help-like commands)
	emptyCmd := &cobra.Command{
		Use:    "",
		Short:  "Empty command",
		Hidden: true,
	}
	rootCmd.AddCommand(emptyCmd)

	metadata := GenerateExtensionMetadata("1.0", "test.extension", rootCmd)

	// Only the normal command should be included; empty Use commands are skipped
	assert.Len(t, metadata.Commands, 1)
	assert.Equal(t, []string{"normal"}, metadata.Commands[0].Name)

	// Verify no command has a nil/empty name
	for _, cmd := range metadata.Commands {
		assert.NotNil(t, cmd.Name, "Command name should not be nil")
		assert.NotEmpty(t, cmd.Name, "Command name should not be empty")
	}
}
