// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package helpformat

import (
	"bytes"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func setColor(t *testing.T, enabled bool) {
	t.Helper()
	previous := color.NoColor
	color.NoColor = !enabled
	t.Cleanup(func() { color.NoColor = previous })
}

func renderHelp(t *testing.T, cmd *cobra.Command) string {
	t.Helper()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.InitDefaultHelpFlag()
	require.NoError(t, cmd.Help())
	return buf.String()
}

func TestHelpPreservesSDKFlagOptions(t *testing.T) {
	setColor(t, false)
	root, _ := azdext.NewExtensionRootCommand(azdext.ExtensionCommandOptions{Name: "agent"})
	child := &cobra.Command{Use: "show [name]", Run: func(*cobra.Command, []string) {}}
	root.AddCommand(child)
	child.Flags().String("label", "50% ready", "The `label` to use")
	child.Flags().String("infra", "", "Infrastructure provider")
	child.Flags().Lookup("infra").NoOptDefVal = "bicep"
	child.Flags().Bool("hidden", false, "Hidden flag")
	require.NoError(t, child.Flags().MarkHidden("hidden"))
	azdext.RegisterFlagOptions(child, azdext.FlagOptions{
		Name: "output", Default: "json", AllowedValues: []string{"json", "table"},
	})
	Install(root, "azd ai", "")

	text := renderHelp(t, child)
	require.Contains(t, text, "azd ai agent show [name] [flags]")
	require.Contains(t, text, `--label label`)
	require.Contains(t, text, `(default "50% ready")`)
	require.Contains(t, text, `--infra string[="bicep"]`)
	require.Contains(t, text, "supported: json, table")
	require.Contains(t, text, `(default "json")`)
	require.NotContains(t, text, "--hidden")
	require.NotContains(t, text, "\x1b")
	require.Equal(t, "default", root.PersistentFlags().Lookup("output").DefValue)
	require.NotContains(t, renderHelp(t, root), "supported: json, table")
}

func TestHelpTemplatesReadLiveState(t *testing.T) {
	setColor(t, false)
	root := &cobra.Command{Use: "agent <command> [options]", Short: "Agent commands"}
	Install(root, "azd ai", "More Information:\n  ${VARIABLE} and ${{ secrets.VALUE }}")
	child := &cobra.Command{
		Use: "optimize [name]", Aliases: []string{"opt"}, Run: func(*cobra.Command, []string) {},
		Long: "Optimize an agent.\n\nReads the config and uploads new versions for:\n  - Datasets",
	}

	child.AddCommand(&cobra.Command{Use: "status", Short: "Show status", Run: func(*cobra.Command, []string) {}})
	root.AddCommand(child, &cobra.Command{Use: "hidden", Hidden: true, Run: func(*cobra.Command, []string) {}})
	root.PersistentFlags().StringP("environment", "e", "", "The environment")

	text := renderHelp(t, root)
	require.Contains(t, text, "Usage\n  azd ai agent [command]\n")
	require.NotContains(t, text, "[options]")
	require.NotContains(t, text, "\nFlags\n")
	require.Contains(t, text, "\nGlobal Flags\n")
	require.Contains(t, text, "--environment")
	require.Contains(t, text, "${VARIABLE} and ${{ secrets.VALUE }}")
	require.NotContains(t, text, "hidden")
	childHelp := renderHelp(t, child)
	require.Contains(t, childHelp, "azd ai agent optimize [name] [flags]\n  azd ai agent optimize [command]")
	require.Contains(t, childHelp, "Aliases\n  optimize, opt")
	require.Contains(t, childHelp, "Reads the config and uploads new versions for:")
	require.NotContains(t, childHelp, "More Information")
	require.NotContains(t, childHelp, "\nFlags\n")
}

func TestHelpOnHostSubtree(t *testing.T) {
	setColor(t, false)
	root := &cobra.Command{Use: "azd"}
	ai := &cobra.Command{Use: "ai", Short: "AI commands"}
	root.AddCommand(ai)
	child := &cobra.Command{Use: "models", Run: func(*cobra.Command, []string) {}}
	ai.AddCommand(child)
	Install(ai, "", "Context:\nUse the selected azd environment.")
	require.Contains(t, renderHelp(t, ai), "Usage\n  azd ai [command]")
	require.Contains(t, renderHelp(t, child), "Usage\n  azd ai models [flags]")
	require.NotContains(t, renderHelp(t, child), "Context")
	require.NotContains(t, renderHelp(t, root), "Context")
}

func TestHelpPreservesMetadataAndAliasDispatch(t *testing.T) {
	setColor(t, false)
	root := &cobra.Command{Use: "agent"}
	child := &cobra.Command{
		Use:     "show <name>",
		Short:   "Show an agent",
		Long:    "Show an agent.\n\nSee https://example.com for details.",
		Example: "  # Show\n  azd ai agent show \"two  spaces\"",
		Aliases: []string{"get"},
		RunE: func(*cobra.Command, []string) error {
			t.Fatal("help must not execute the command")
			return nil
		},
	}
	root.AddCommand(child)
	originalUse, originalShort, originalLong, originalExample := child.Use, child.Short, child.Long, child.Example
	Install(root, "azd ai", "")
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"get", "--help"})
	require.NoError(t, root.Execute())
	require.Contains(t, buf.String(), "azd ai agent show <name> [flags]")
	require.Contains(t, buf.String(), "Aliases\n  show, get")
	require.Contains(t, buf.String(), `"two  spaces"`)
	require.Equal(t, originalUse, child.Use)
	require.Equal(t, originalShort, child.Short)
	require.Equal(t, originalLong, child.Long)
	require.Equal(t, originalExample, child.Example)
	require.Equal(t, []string{"get"}, child.Aliases)
	require.False(t, child.Hidden)
}

func TestHelpColorAtRenderTime(t *testing.T) {
	setColor(t, false)
	root := &cobra.Command{Use: "agent", Short: "Agent commands"}
	child := &cobra.Command{Use: "show <name>", Run: func(*cobra.Command, []string) {}}
	root.AddCommand(child)
	child.Flags().String("name", "", "The name")
	Install(root, "azd ai", "")
	require.NotContains(t, renderHelp(t, child), "\x1b")

	color.NoColor = false
	text := renderHelp(t, child)
	require.Contains(t, text, heading("Usage"))
	require.Contains(t, text, output.WithHighLightFormat("%s", "--name"))
	require.Contains(t, text, output.WithWarningFormat("%s", "<name>"))
	require.Contains(t, renderHelp(t, root), output.WithHighLightFormat("%s", "show"))

	color.NoColor = true
	require.NotContains(t, renderHelp(t, child), "\x1b")
}

func TestExamplesPreserveShellText(t *testing.T) {
	setColor(t, false)
	for _, tt := range []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "captions and separate commands",
			raw:  "  # Apply and deploy\n  azd ai agent optimize apply --candidate <id>\n  azd deploy",
			want: "  Apply and deploy\n    azd ai agent optimize apply --candidate <id>\n    azd deploy",
		},
		{
			name: "no captions",
			raw:  "  azd ai agent eval generate\n  azd ai agent eval run",
			want: "    azd ai agent eval generate\n    azd ai agent eval run",
		},
		{
			name: "multiline quoting and templates",
			raw:  "  # Initialize\n  azd ai agent init \\\n    --instructions \"Keep  two spaces: ${{ secrets.VALUE }}\"",
			want: "  Initialize\n    azd ai agent init \\\n      --instructions \"Keep  two spaces: ${{ secrets.VALUE }}\"",
		},
		{
			name: "repeated captions",
			raw:  "  # Show\n  azd ai agent show\n\n  # Show\n  azd ai agent show other",
			want: "  Show\n    azd ai agent show\n\n  Show\n    azd ai agent show other",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, examples(tt.raw))
		})
	}
}

func TestSectionsAndInlineTokens(t *testing.T) {
	setColor(t, true)
	text := sections("Voice Agents:\nUse --voice with 'azd ai agent init'.\n\n" +
		"Long-running Invocations:\nSee https://example.com and <name>.\n\n" +
		"Reads the config and uploads new versions for:\n  - Datasets")
	require.Contains(t, text, heading("Voice Agents"))
	require.Contains(t, text, heading("Long-running Invocations"))
	require.Contains(t, text, output.WithHighLightFormat("%s", "--voice"))
	require.Contains(t, text, output.WithHighLightFormat("%s", "'azd ai agent init'"))
	require.Contains(t, text, output.WithLinkFormat("%s", "https://example.com"))
	require.Contains(t, text, "Reads the config and uploads new versions for:")
}
