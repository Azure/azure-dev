// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestRegisterFlagOptions_HelpRendering(t *testing.T) {
	root, _ := NewExtensionRootCommand(ExtensionCommandOptions{Name: "test"})
	showCmd := RegisterFlagOptions(&cobra.Command{
		Use:   "show",
		Short: "show",
		RunE: func(cmd *cobra.Command, args []string) error {
			return nil
		},
	}, "output", []string{"json", "table"}, "json")
	versionCmd := RegisterFlagOptions(&cobra.Command{
		Use:   "version",
		Short: "version",
		RunE: func(cmd *cobra.Command, args []string) error {
			return nil
		},
	}, "output", []string{"json"}, "json")
	root.AddCommand(showCmd, versionCmd)

	showHelp := captureStdout(t, func() {
		root.SetArgs([]string{"show", "--help"})
		err := root.Execute()
		require.NoError(t, err)
	})
	require.Contains(t, string(showHelp), `The output format (supported: json, table)`)
	require.Contains(t, string(showHelp), `(default "json")`)

	versionHelp := captureStdout(t, func() {
		root.SetArgs([]string{"version", "--help"})
		err := root.Execute()
		require.NoError(t, err)
	})
	require.Contains(t, string(versionHelp), `The output format (supported: json)`)
	require.NotContains(t, string(versionHelp), `supported: json, table`)

	rootHelp := captureStdout(t, func() {
		root.SetArgs([]string{"--help"})
		err := root.Execute()
		require.NoError(t, err)
	})
	require.Contains(t, string(rootHelp), defaultOutputFlagUsage)
	require.NotContains(t, string(rootHelp), `supported: json, table`)
}

func TestRegisterFlagOptions_RejectsInvalidValue(t *testing.T) {
	root, extCtx := NewExtensionRootCommand(ExtensionCommandOptions{Name: "test"})
	root.SilenceUsage = true
	root.SilenceErrors = true

	root.AddCommand(RegisterFlagOptions(&cobra.Command{
		Use:  "list",
		RunE: func(cmd *cobra.Command, args []string) error { return nil },
	}, "output", []string{"json", "table"}, "json"))

	root.SetArgs([]string{"list", "--output", "yaml"})
	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), `invalid value "yaml" for --output`)
	require.Contains(t, err.Error(), "json, table")
	// Validation should not have mutated the bound variable past parsing.
	require.Equal(t, "yaml", extCtx.OutputFormat)
}

func TestRegisterFlagOptions_AppliesPerCommandDefault(t *testing.T) {
	root, extCtx := NewExtensionRootCommand(ExtensionCommandOptions{Name: "test"})
	root.SilenceUsage = true
	root.SilenceErrors = true

	var observed string
	root.AddCommand(RegisterFlagOptions(&cobra.Command{
		Use: "list",
		RunE: func(cmd *cobra.Command, args []string) error {
			observed = extCtx.OutputFormat
			return nil
		},
	}, "output", []string{"json", "table"}, "json"))

	root.SetArgs([]string{"list"})
	require.NoError(t, root.Execute())
	require.Equal(t, "json", observed)
	require.Equal(t, "json", extCtx.OutputFormat)
}

func TestRegisterFlagOptions_AcceptsExplicitAllowedValue(t *testing.T) {
	root, extCtx := NewExtensionRootCommand(ExtensionCommandOptions{Name: "test"})
	root.SilenceUsage = true
	root.SilenceErrors = true

	root.AddCommand(RegisterFlagOptions(&cobra.Command{
		Use:  "list",
		RunE: func(cmd *cobra.Command, args []string) error { return nil },
	}, "output", []string{"json", "table"}, "json"))

	root.SetArgs([]string{"list", "--output", "table"})
	require.NoError(t, root.Execute())
	require.Equal(t, "table", extCtx.OutputFormat)
}

func TestRegisterFlagOptions_RegistersShellCompletion(t *testing.T) {
	root, _ := NewExtensionRootCommand(ExtensionCommandOptions{Name: "test"})
	listCmd := RegisterFlagOptions(&cobra.Command{
		Use:  "list",
		RunE: func(cmd *cobra.Command, args []string) error { return nil },
	}, "output", []string{"json", "table"}, "json")
	root.AddCommand(listCmd)

	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{cobra.ShellCompRequestCmd, "list", "--output", ""})
	require.NoError(t, root.Execute())

	out := buf.String()
	require.Contains(t, out, "json")
	require.Contains(t, out, "table")
}

func TestRegisterFlagOptions_NilCommandIsNoOp(t *testing.T) {
	require.NotPanics(t, func() {
		RegisterFlagOptions(nil, "output", []string{"json"}, "json")
	})
}

func TestRegisterFlagOptions_OnlyDefaultStillSubstitutes(t *testing.T) {
	root, extCtx := NewExtensionRootCommand(ExtensionCommandOptions{Name: "test"})
	root.SilenceUsage = true
	root.SilenceErrors = true

	root.AddCommand(RegisterFlagOptions(&cobra.Command{
		Use:  "list",
		RunE: func(cmd *cobra.Command, args []string) error { return nil },
	}, "output", nil, "json"))

	root.SetArgs([]string{"list"})
	require.NoError(t, root.Execute())
	require.Equal(t, "json", extCtx.OutputFormat)

	// With no allowed values configured, any value is accepted.
	root.SetArgs([]string{"list", "--output", "anything"})
	require.NoError(t, root.Execute())
	require.Equal(t, "anything", extCtx.OutputFormat)
}

func TestExtensionCommands_NewListenCommand(t *testing.T) {
	t.Run("CreatesHiddenCommand", func(t *testing.T) {
		cmd := NewListenCommand(nil)
		require.Equal(t, "listen", cmd.Use)
		require.True(t, cmd.Hidden)
	})

	t.Run("NilConfiguratorDoesNotPanic", func(t *testing.T) {
		cmd := NewListenCommand(nil)
		require.NotNil(t, cmd)
		// We only verify command creation; execution requires a gRPC server.
	})

	t.Run("WithConfigurator", func(t *testing.T) {
		called := false
		cmd := NewListenCommand(func(host *ExtensionHost) {
			called = true
		})
		require.NotNil(t, cmd)
		// Configurator is only called during RunE, not during creation.
		require.False(t, called)
	})
}

func TestExtensionCommands_NewMetadataCommand(t *testing.T) {
	t.Run("CreatesHiddenCommand", func(t *testing.T) {
		cmd := NewMetadataCommand("1.0", "test.ext", func() *cobra.Command {
			return &cobra.Command{Use: "test"}
		})
		require.Equal(t, "metadata", cmd.Use)
		require.True(t, cmd.Hidden)
	})

	t.Run("OutputsValidJSON", func(t *testing.T) {
		rootProvider := func() *cobra.Command {
			root := &cobra.Command{Use: "myext"}
			root.AddCommand(&cobra.Command{Use: "hello", Short: "Says hello"})
			return root
		}

		cmd := NewMetadataCommand("1.0", "test.extension", rootProvider)

		// Capture stdout
		buf := new(bytes.Buffer)
		cmd.SetOut(buf)
		cmd.SetArgs([]string{})

		// Redirect fmt.Println output
		old := cmd.RunE
		cmd.RunE = func(c *cobra.Command, args []string) error {
			// Run the original but capture via root command output
			err := old(c, args)
			return err
		}

		// Execute and capture output via pipe
		outBuf := captureStdout(t, func() {
			err := cmd.Execute()
			require.NoError(t, err)
		})

		// Verify it's valid JSON
		var result map[string]any
		err := json.Unmarshal(outBuf, &result)
		require.NoError(t, err, "metadata output should be valid JSON")
		require.Equal(t, "test.extension", result["id"])
		require.Equal(t, "1.0", result["schemaVersion"])
	})
}

func TestExtensionCommands_NewVersionCommand(t *testing.T) {
	t.Run("CreatesVisibleCommand", func(t *testing.T) {
		format := ""
		cmd := NewVersionCommand("my.ext", "1.2.3", &format)
		require.Equal(t, "version", cmd.Use)
		require.False(t, cmd.Hidden)
		require.Equal(t, "Display the extension version", cmd.Short)
	})

	t.Run("DefaultFormat", func(t *testing.T) {
		format := ""
		cmd := NewVersionCommand("my.ext", "1.2.3", &format)
		cmd.SetArgs([]string{})

		output := captureStdout(t, func() {
			err := cmd.Execute()
			require.NoError(t, err)
		})

		require.Equal(t, "my.ext 1.2.3\n", string(output))
	})

	t.Run("JSONFormat", func(t *testing.T) {
		format := "json"
		cmd := NewVersionCommand("my.ext", "1.2.3", &format)
		cmd.SetArgs([]string{})

		output := captureStdout(t, func() {
			err := cmd.Execute()
			require.NoError(t, err)
		})

		var result map[string]string
		err := json.Unmarshal(output, &result)
		require.NoError(t, err, "version output should be valid JSON")
		require.Equal(t, "my.ext", result["name"])
		require.Equal(t, "1.2.3", result["version"])
	})
}

// captureStdout captures stdout output from a function.
func captureStdout(t *testing.T, fn func()) []byte {
	t.Helper()

	r, w, err := os.Pipe()
	require.NoError(t, err)

	oldStdout := os.Stdout
	os.Stdout = w

	fn()

	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	_, err = buf.ReadFrom(r)
	require.NoError(t, err)

	return buf.Bytes()
}
