// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/fatih/color"
	"github.com/kballard/go-shellquote"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setHelpColor(t *testing.T, enabled bool) {
	t.Helper()
	previous := color.NoColor
	color.NoColor = !enabled
	t.Cleanup(func() { color.NoColor = previous })
}

func renderHelp(t *testing.T, cmd *cobra.Command) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.InitDefaultHelpFlag()
	require.NoError(t, cmd.Help())
	assert.Empty(t, stderr.String())
	return stdout.String()
}

func TestHelpPreservesSDKFlagOptions(t *testing.T) {
	setHelpColor(t, false)
	root, _ := azdext.NewExtensionRootCommand(azdext.ExtensionCommandOptions{Name: "cu"})
	child := &cobra.Command{Use: "show [name]", Run: func(*cobra.Command, []string) {}}
	root.AddCommand(child)
	child.Flags().String("label", "50% ready", "The `label` to use")
	child.Flags().String("infra", "", "Infrastructure provider")
	child.Flags().Lookup("infra").NoOptDefVal = "bicep"
	child.Flags().Bool("hidden", false, "Hidden flag")
	require.NoError(t, child.Flags().MarkHidden("hidden"))
	azdext.RegisterFlagOptions(child, azdext.FlagOptions{
		Name: "output", Default: "json", AllowedValues: []string{"json", "none"},
	})
	installHelp(root)

	text := renderHelp(t, child)
	assert.Contains(t, text, "azd ai cu show [name] [flags]")
	assert.Contains(t, text, "--label label")
	assert.Contains(t, text, `(default "50% ready")`)
	assert.Contains(t, text, `--infra string[="bicep"]`)
	assert.Contains(t, text, "supported: json, none")
	assert.Contains(t, text, `(default "json")`)
	assert.Contains(t, text, "\nFlags\n")
	assert.Contains(t, text, "\nGlobal Flags\n")
	assert.NotContains(t, text, "--hidden")
	assert.NotContains(t, text, "\x1b")
	assert.Equal(t, "default", root.PersistentFlags().Lookup("output").DefValue)
	assert.NotContains(t, renderHelp(t, root), "supported: json, none")
}

func TestHelpColorAtRenderTime(t *testing.T) {
	setHelpColor(t, false)
	root := NewRootCommand()
	analyze, _, err := root.Find([]string{"analyze"})
	require.NoError(t, err)
	plain := renderHelp(t, analyze)
	assert.NotContains(t, plain, "\x1b")
	assert.Contains(t, plain, "azd ai cu analyze [INPUTS...] [flags]")

	color.NoColor = false
	styled := renderHelp(t, analyze)
	assert.Contains(t, styled, helpHeading("Examples"))
	assert.Contains(t, styled, output.WithHighLightFormat("%s", "--analyzer"))
	assert.Contains(t, styled, output.WithWarningFormat("%s", "[INPUTS...]"))
	assert.Contains(t, styled, output.WithHighLightFormat("%s", "  azd ai cu analyze invoice.pdf"))
	assert.Contains(t, styled, output.WithGrayFormat("%s",
		"    Analyze one file using the configured analyzer, or prebuilt-layout if unset."))
	assert.Contains(t, renderHelp(t, root), output.WithHighLightFormat("%s", "analyze"))

	color.NoColor = true
	assert.Equal(t, plain, renderHelp(t, analyze))
}

func TestHelpUsesLiveCommandTree(t *testing.T) {
	setHelpColor(t, false)
	root := NewRootCommand()
	child := &cobra.Command{
		Use: "probe <name>", Short: "A command added after help setup", Aliases: []string{"p"},
		Run: func(*cobra.Command, []string) {},
	}
	root.AddCommand(child)
	child.Flags().String("label", "", "A label")
	assert.Contains(t, renderHelp(t, root), "probe")
	text := renderHelp(t, child)
	assert.Contains(t, text, "azd ai cu probe <name> [flags]")
	assert.Contains(t, text, "Aliases\n  probe, p")
	assert.Contains(t, text, "--label")
	for _, hidden := range []string{"metadata", "_models", "_postprovision-v1"} {
		assert.NotContains(t, renderHelp(t, root), hidden)
	}
}

func TestHelpExamplesPreserveShellText(t *testing.T) {
	setHelpColor(t, false)
	text := `  azd ai cu analyze "file with spaces.pdf" \
    --output-file "${RESULT_DIR}/invoice.md" --llm-input
    Save Markdown without changing shell quoting or ${VARIABLE} expressions.

  azd ai cu analyze --url 'https://example.com/a.pdf?key=value&other=50%25'
    Analyze an HTTPS URL.`
	assert.Equal(t, text, helpExamples(text))
	setHelpColor(t, true)
	colored := helpExamples(text)
	assert.Contains(t, colored, output.WithHighLightFormat("%s", `  azd ai cu analyze "file with spaces.pdf" \`))
	assert.Contains(t, colored, output.WithHighLightFormat("%s",
		`    --output-file "${RESULT_DIR}/invoice.md" --llm-input`))
	assert.Contains(t, colored, "${VARIABLE}")
	assert.Contains(t, colored, "50%25")
}

func TestPublicHelpExamplesAreDescribedAndParse(t *testing.T) {
	setHelpColor(t, false)
	t.Setenv("AZD_SERVER", "")
	t.Setenv("AZD_ACCESS_TOKEN", "")
	root := NewRootCommand()
	var visit func(*cobra.Command)
	visit = func(cmd *cobra.Command) {
		if cmd.Hidden {
			return
		}
		t.Run(cmd.CommandPath(), func(t *testing.T) {
			text := renderHelp(t, cmd)
			assert.Contains(t, text, helpCommandPath(cmd))
			assert.NotContains(t, text, "\x1b")
			if cmd.Runnable() {
				require.NotEmpty(t, cmd.Example, "public commands need common-use examples")
			}
			if cmd.Example == "" {
				return
			}
			for block := range strings.SplitSeq(cmd.Example, "\n\n") {
				lines := strings.SplitN(strings.ReplaceAll(block, "\\\n", " "), "\n", 2)
				require.Len(t, lines, 2, "each command must have a description")
				require.NotEmpty(t, strings.TrimSpace(lines[1]))
				assert.Contains(t, text, lines[1])
				args, err := shellquote.Split(strings.TrimSpace(lines[0]))
				require.NoError(t, err)
				require.Greater(t, len(args), 3)
				require.Equal(t, []string{"azd", "ai", "cu"}, args[:3])
				exampleRoot := NewRootCommand()
				target, flags, err := exampleRoot.Find(args[3:])
				require.NoError(t, err)
				if cmd != root {
					assert.Equal(t, cmd.CommandPath(), target.CommandPath())
				}
				require.NoError(t, ValidateInvocation(exampleRoot, args[3:]))
				require.NoError(t, target.ParseFlags(flags), "example: %s", lines[0])
			}
		})
		for _, child := range cmd.Commands() {
			visit(child)
		}
	}
	visit(root)
}

func TestColoredHelpDoesNotColorJSON(t *testing.T) {
	setHelpColor(t, true)
	for _, args := range [][]string{{"metadata"}, {"version", "--output", "json"}} {
		stdout, stderr, err := executeRoot(t, args...)
		require.NoError(t, err)
		assert.Empty(t, stderr)
		assert.NotContains(t, stdout, "\x1b")
		assert.NotContains(t, stdout, "\\u001b")
		assert.True(t, json.Valid([]byte(stdout)))
	}
}
