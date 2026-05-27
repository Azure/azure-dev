// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package helpformat

import (
	"bytes"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// withColorEnabled toggles color.NoColor for one test only and restores
// the previous value via t.Cleanup. Tests that use this MUST NOT call
// t.Parallel(): color.NoColor is process-global state.
func withColorEnabled(t *testing.T) {
	t.Helper()
	prev := color.NoColor
	color.NoColor = false
	t.Cleanup(func() { color.NoColor = prev })
}

// withColorDisabled is the inverse helper. Same parallelism caveat.
func withColorDisabled(t *testing.T) {
	t.Helper()
	prev := color.NoColor
	color.NoColor = true
	t.Cleanup(func() { color.NoColor = prev })
}

func TestDescription_TitleOnly(t *testing.T) {
	t.Parallel()
	got := Description("Initialize a new application.")
	require.Equal(t, "Initialize a new application.\n\n", got)
}

func TestDescription_WithNotes(t *testing.T) {
	t.Parallel()
	got := Description(
		"Initialize a new application.",
		Note("Running init prompts the user."),
		Note("When using --template, a new directory is created."),
	)
	want := "Initialize a new application.\n\n" +
		"  * Running init prompts the user.\n" +
		"  * When using --template, a new directory is created.\n\n"
	require.Equal(t, want, got)
}

func TestNote_AsciiBullet(t *testing.T) {
	t.Parallel()
	require.Equal(t, "  * hello", Note("hello"))
	// Confirm no non-ASCII glyph snuck in (regression guard for the
	// repo-wide ASCII rule).
	for _, r := range Note("hello") {
		require.Less(t, r, rune(128), "Note must emit ASCII only; saw rune %U", r)
	}
}

func TestExamples_Empty(t *testing.T) {
	t.Parallel()
	require.Equal(t, "", Examples(map[string]string{}))
	require.Equal(t, "", Examples(nil))
}

func TestExamples_DeterministicOrder(t *testing.T) {
	// No t.Parallel: withColorDisabled mutates color.NoColor which is
	// process-global. Parallel tests in the same package would race.
	withColorDisabled(t) // suppress ANSI so substring asserts are stable

	samples := map[string]string{
		"Zebra example": "azd ai agent zebra",
		"Alpha example": "azd ai agent alpha",
		"Mango example": "azd ai agent mango",
	}
	out := Examples(samples)
	// "Alpha" < "Mango" < "Zebra" alphabetically.
	alphaIdx := strings.Index(out, "Alpha example")
	mangoIdx := strings.Index(out, "Mango example")
	zebraIdx := strings.Index(out, "Zebra example")
	require.Positive(t, alphaIdx, "Alpha example missing from output")
	require.Positive(t, mangoIdx, "Mango example missing from output")
	require.Positive(t, zebraIdx, "Zebra example missing from output")
	require.Less(t, alphaIdx, mangoIdx, "Alpha must appear before Mango")
	require.Less(t, mangoIdx, zebraIdx, "Mango must appear before Zebra")
}

func TestExamples_HeaderUnderlined(t *testing.T) {
	// Force color ON so the ANSI escape is asserted to render.
	withColorEnabled(t)

	out := Examples(map[string]string{"One": "azd one"})
	// Underline escape is ESC [4m; bold escape is ESC [1m. Either order
	// (cobra/fatih may pick either composition). Assert the underline
	// code is present regardless.
	require.Contains(t, out, "\x1b[", "expected ANSI escape sequences when color enabled")
	require.Contains(t, out, "4m", "expected underline (ESC[4m) attribute")
	require.Contains(t, out, "Examples:")
}

// helper: build a minimal command with two flags and one subcommand.
// Returns the cmd ready for Install.
func makeTestCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "demo",
		Short: "A demo command.",
		Run:   func(cmd *cobra.Command, args []string) {},
	}
	root.Flags().StringP("name", "n", "", "Name to use.")
	root.Flags().Bool("force", false, "Force the operation.")

	sub := &cobra.Command{
		Use:   "child",
		Short: "A child subcommand.",
		Run:   func(cmd *cobra.Command, args []string) {},
	}
	root.AddCommand(sub)
	return root
}

func TestInstall_RenderableWithoutOptions(t *testing.T) {
	withColorDisabled(t)

	cmd := makeTestCmd()
	Install(cmd, Options{})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	require.NoError(t, cmd.Help())

	out := buf.String()
	require.Contains(t, out, "Usage:")
	require.Contains(t, out, "Flags:")
	require.Contains(t, out, "Available Commands:")
	require.Contains(t, out, "child")
	require.Contains(t, out, "--name")
	require.Contains(t, out, "--force")
}

func TestInstall_WithDescription(t *testing.T) {
	withColorDisabled(t)

	cmd := makeTestCmd()
	Install(cmd, Options{
		Description: func(c *cobra.Command) string {
			return Description(
				"My custom title.",
				Note("First bullet."),
				Note("Second bullet."),
			)
		},
	})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	require.NoError(t, cmd.Help())

	out := buf.String()
	require.Contains(t, out, "My custom title.")
	require.Contains(t, out, "  * First bullet.")
	require.Contains(t, out, "  * Second bullet.")
}

func TestInstall_WithFooter(t *testing.T) {
	withColorDisabled(t)

	cmd := makeTestCmd()
	Install(cmd, Options{
		Footer: func(c *cobra.Command) string {
			return Examples(map[string]string{
				"Do a thing": "demo --name foo",
			})
		},
	})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	require.NoError(t, cmd.Help())

	out := buf.String()
	require.Contains(t, out, "Examples:")
	require.Contains(t, out, "Do a thing")
	require.Contains(t, out, "demo --name foo")
}

func TestInstall_NoSubcommandsOmitsAvailableCommands(t *testing.T) {
	withColorDisabled(t)

	leaf := &cobra.Command{
		Use:   "leaf",
		Short: "A leaf command.",
		Run:   func(cmd *cobra.Command, args []string) {},
	}
	leaf.Flags().Bool("opt", false, "An option.")
	Install(leaf, Options{})

	var buf bytes.Buffer
	leaf.SetOut(&buf)
	require.NoError(t, leaf.Help())

	require.NotContains(t, buf.String(), "Available Commands:")
}

// TestInstall_PreservesFlagOverrides is the regression test for
// rubber-duck #1: SetUsageTemplate + SetHelpTemplate must keep the
// SDK's per-command flag-option enrichments visible in --help.
//
// We build a real SDK root via azdext.NewExtensionRootCommand, add a
// subcommand whose --output flag has registered allowed values, install
// styled help on it, render --help, and assert the "(supported: ...)"
// text appears.
func TestInstall_PreservesFlagOverrides(t *testing.T) {
	withColorDisabled(t)

	root, _ := azdext.NewExtensionRootCommand(azdext.ExtensionCommandOptions{Name: "ext"})
	root.SilenceUsage = true
	root.SilenceErrors = true

	sub := azdext.RegisterFlagOptions(&cobra.Command{
		Use:   "show",
		Short: "Show something.",
		Run:   func(cmd *cobra.Command, args []string) {},
	}, azdext.FlagOptions{
		Name:          "output",
		AllowedValues: []string{"json", "yaml"},
	})
	root.AddCommand(sub)
	Install(sub, Options{})

	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"show", "--help"})
	require.NoError(t, root.Execute())

	out := buf.String()
	require.Contains(t, out, "supported:", "expected SDK flag-option override to render via wrapped UsageFunc")
	require.Contains(t, out, "json")
	require.Contains(t, out, "yaml")
}

func TestInstall_GlobalFlagsSection(t *testing.T) {
	withColorDisabled(t)

	root := &cobra.Command{Use: "root"}
	root.PersistentFlags().String("inherited", "", "An inherited flag.")
	sub := &cobra.Command{
		Use:   "leaf",
		Short: "Leaf.",
		Run:   func(cmd *cobra.Command, args []string) {},
	}
	sub.Flags().String("local", "", "A local flag.")
	root.AddCommand(sub)
	Install(sub, Options{})

	var buf bytes.Buffer
	sub.SetOut(&buf)
	require.NoError(t, sub.Help())

	out := buf.String()
	require.Contains(t, out, "Global Flags:")
	require.Contains(t, out, "--inherited")
	require.Contains(t, out, "--local")
}

// TestInstall_ForcedGlobalFlagsAreFiltered is the regression test for
// rubber-duck #8: --docs is in nonPersistentGlobalFlags but should NOT
// appear in Global Flags unless actually registered on the command.
// --help, in contrast, is registered by cobra at Execute() time and
// MUST appear in Global Flags.
func TestInstall_ForcedGlobalFlagsAreFiltered(t *testing.T) {
	withColorDisabled(t)

	root := &cobra.Command{Use: "root"}
	cmd := &cobra.Command{
		Use:   "leaf",
		Short: "Leaf.",
		Run:   func(cmd *cobra.Command, args []string) {},
	}
	cmd.Flags().String("opt", "", "An option.")
	root.AddCommand(cmd)
	Install(cmd, Options{})

	// Drive --help via Execute so cobra's InitDefaultHelpFlag runs and
	// the local --help flag is registered. cmd.Help() called directly
	// bypasses that init, leaving Global Flags empty.
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"leaf", "--help"})
	require.NoError(t, root.Execute())

	out := buf.String()
	require.Contains(t, out, "Global Flags:")
	require.Contains(t, out, "--help", "--help should appear in Global Flags after Execute auto-registration")
	require.NotContains(t, out, "--docs", "--docs is forced-global but not registered; must not appear")
}

// TestInstall_UseLineNoDuplicateCommandToken is the regression for
// rubber-duck #5: verbose `Use:` strings on parent commands must not
// produce duplicated `[command]` suffixes.
func TestInstall_UseLineNoDuplicateCommandToken(t *testing.T) {
	withColorDisabled(t)

	parent := &cobra.Command{
		Use:   "agent <command> [options]",
		Short: "Parent command.",
	}
	child := &cobra.Command{
		Use:   "do",
		Short: "Do something.",
		Run:   func(cmd *cobra.Command, args []string) {},
	}
	parent.AddCommand(child)
	Install(parent, Options{})

	var buf bytes.Buffer
	parent.SetOut(&buf)
	require.NoError(t, parent.Help())

	out := buf.String()
	// The Use string already mentions `<command>`; cobra appends
	// `agent [command]` because HasAvailableSubCommands is true and
	// parent is not Runnable (no Run func). That produces ONE
	// `[command]` token total. Two would be a regression.
	count := strings.Count(out, "[command]")
	require.Equal(t, 1, count, "expected exactly one [command] token in Usage section, got %d. Output:\n%s", count, out)
}

// TestInstall_DescriptionWithTemplateLiterals is the regression for
// rubber-duck-impl #2: description / footer text containing the Go
// text/template delimiters `{{` and `}}` (e.g. a GitHub Actions example
// like `${{ secrets.FOO }}`) must render as literal characters, not
// be interpreted by the template parser.
func TestInstall_DescriptionWithTemplateLiterals(t *testing.T) {
	withColorDisabled(t)

	hostile := "Use ${{ secrets.FOO }} for the token. {{not a directive}}"

	cmd := &cobra.Command{
		Use:   "leaf",
		Short: "Leaf.",
		Run:   func(c *cobra.Command, args []string) {},
	}
	Install(cmd, Options{
		Description: func(c *cobra.Command) string {
			return Description(hostile, Note("And ${{ ANOTHER }} in a bullet."))
		},
		Footer: func(c *cobra.Command) string {
			return Examples(map[string]string{ //nolint:gosec // not a credential
				"Use a workflow secret.": "demo --token ${{ secrets.FOO }}",
			})
		},
	})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	require.NoError(t, cmd.Help())

	out := buf.String()
	require.Contains(t, out, "${{ secrets.FOO }}", "template literal must render verbatim in description")
	require.Contains(t, out, "{{not a directive}}", "free-standing {{...}} must render verbatim")
	require.Contains(t, out, "${{ ANOTHER }}", "template literal in a Note bullet must render verbatim")
	require.Contains(t, out, "${{ secrets.FOO }}", "template literal in Examples must render verbatim")
}

// TestInstall_NoEmptyLocalFlagsBlockWhenOnlyHelpRegistered is the
// regression for rubber-duck-impl #1: cobra registers --help as a
// LOCAL flag on every command at Execute() time. Our renderer filters
// forced-globals (--help, --docs) out of the Local Flags section. If
// the template's "show Local Flags?" guard uses cobra's
// .HasAvailableLocalFlags, it would return true (because --help is
// local), and we'd render an empty "Flags:" header with no body.
//
// helpformatHasLocalFlags() correctly returns false for this case.
func TestInstall_NoEmptyLocalFlagsBlockWhenOnlyHelpRegistered(t *testing.T) {
	withColorDisabled(t)

	root := &cobra.Command{Use: "root"}
	leaf := &cobra.Command{
		Use:   "leaf",
		Short: "Leaf with no real local flags.",
		Run:   func(c *cobra.Command, args []string) {},
	}
	root.AddCommand(leaf)
	Install(leaf, Options{})

	// Drive --help via Execute so cobra registers it on the leaf's
	// LocalFlags. Without Execute, --help is never added and the bug
	// would not reproduce.
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"leaf", "--help"})
	require.NoError(t, root.Execute())

	out := buf.String()
	require.NotContains(t, out, "Flags:\n\nGlobal Flags:",
		"expected Local Flags section to be entirely omitted (no empty Flags: header). Output:\n%s", out)
	require.Contains(t, out, "Global Flags:", "--help should still appear in Global Flags")
	require.Contains(t, out, "--help")
}

// TestInstall_AutoMigratesExampleFieldWhenFooterAbsent verifies that
// commands which leave the legacy cobra.Command.Example field set
// (and don't supply Options.Footer) get their examples auto-promoted
// into a styled Examples block AND have cmd.Example cleared so cobra's
// default template doesn't double-render an unstyled section.
func TestInstall_AutoMigratesExampleFieldWhenFooterAbsent(t *testing.T) {
	withColorDisabled(t)

	cmd := &cobra.Command{
		Use:   "leaf",
		Short: "Leaf.",
		Run:   func(c *cobra.Command, args []string) {},
		Example: `  # First scenario
  demo --flag value

  # Second scenario with a placeholder
  demo <path>`,
	}
	Install(cmd, Options{})

	require.Equal(t, "", cmd.Example, "cmd.Example must be cleared after auto-migration to avoid double-render")

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	require.NoError(t, cmd.Help())

	out := buf.String()
	require.Contains(t, out, "Examples:")
	require.Contains(t, out, "First scenario")
	require.Contains(t, out, "Second scenario with a placeholder")
	require.Contains(t, out, "demo --flag value")
	require.Contains(t, out, "demo <path>")
}

// TestInstall_FooterTakesPrecedenceOverAutoMigration confirms that
// when a caller supplies an explicit Footer AND cmd.Example is also
// set, the explicit Footer wins (and cmd.Example is left alone).
func TestInstall_FooterTakesPrecedenceOverAutoMigration(t *testing.T) {
	withColorDisabled(t)

	cmd := &cobra.Command{
		Use:     "leaf",
		Short:   "Leaf.",
		Run:     func(c *cobra.Command, args []string) {},
		Example: "  # Auto title\n  auto cmd",
	}
	Install(cmd, Options{
		Footer: func(c *cobra.Command) string {
			return Examples(map[string]string{"Explicit title": "explicit cmd"})
		},
	})

	// cmd.Example is left intact since the explicit Footer overrode
	// the auto-migration path -- callers may still inspect it.
	require.Equal(t, "  # Auto title\n  auto cmd", cmd.Example)

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	require.NoError(t, cmd.Help())

	out := buf.String()
	require.Contains(t, out, "Explicit title")
	require.Contains(t, out, "explicit cmd")
	require.NotContains(t, out, "Auto title", "explicit Footer must override auto-migration")
}

func TestParseExampleText_StylesFlagsAndPlaceholders(t *testing.T) {
	// Force color on to assert the tokens get wrapped in ANSI escapes.
	withColorEnabled(t)

	in := `  # Scenario
  demo --flag value <placeholder> [optional] plainArg`

	samples := parseExampleText(in)
	require.Contains(t, samples, "Scenario.")
	body := samples["Scenario."]
	// --flag and the bracketed/angle-bracketed tokens should be wrapped
	// in ANSI escapes; plainArg should not.
	require.Contains(t, body, "\x1b[", "expected ANSI escape sequences on at least one token")
	require.Contains(t, body, "plainArg")
}

// TestInstallAll_RecursivelyStylesAndRespectsPreInstalled verifies the
// bulk wiring path used by root constructors. Pre-Installed commands
// keep their custom Description; un-Installed children get default
// styling; hidden commands stay un-Installed.
func TestInstallAll_RecursivelyStylesAndRespectsPreInstalled(t *testing.T) {
	withColorDisabled(t)

	root := &cobra.Command{Use: "root", Short: "Root."}
	visible := &cobra.Command{
		Use:   "visible",
		Short: "Visible leaf.",
		Run:   func(c *cobra.Command, args []string) {},
	}
	hidden := &cobra.Command{
		Use:    "hidden",
		Short:  "Hidden leaf.",
		Hidden: true,
		Run:    func(c *cobra.Command, args []string) {},
	}
	preStyled := &cobra.Command{
		Use:   "prestyled",
		Short: "Pre-styled leaf.",
		Run:   func(c *cobra.Command, args []string) {},
	}
	Install(preStyled, Options{
		Description: func(c *cobra.Command) string {
			return Description("CUSTOM-MARKER")
		},
	})

	root.AddCommand(visible, hidden, preStyled)
	InstallAll(root)

	// visible should now be installed by InstallAll.
	require.True(t, isInstalled(visible), "InstallAll should style visible leaf")

	// hidden should remain un-installed (no --help styling for hidden surfaces).
	require.False(t, isInstalled(hidden), "InstallAll should skip hidden leaves")

	// preStyled should retain its CUSTOM-MARKER description even after
	// InstallAll runs.
	var buf bytes.Buffer
	preStyled.SetOut(&buf)
	require.NoError(t, preStyled.Help())
	require.Contains(t, buf.String(), "CUSTOM-MARKER",
		"InstallAll must not clobber per-command customizations")
}
