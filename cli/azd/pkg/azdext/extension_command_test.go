// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"os"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
)

func TestExtensionCommand_CreatesCommandWithExpectedFlags(t *testing.T) {
	cmd, _ := NewExtensionRootCommand(ExtensionCommandOptions{
		Name:    "test-ext",
		Version: "0.1.0",
		Short:   "A test extension",
	})

	require.Equal(t, "test-ext", cmd.Use)
	require.Equal(t, "0.1.0", cmd.Version)
	require.Equal(t, "A test extension", cmd.Short)

	flags := cmd.PersistentFlags()
	require.NotNil(t, flags.Lookup("debug"))
	require.NotNil(t, flags.Lookup("no-prompt"))
	require.NotNil(t, flags.Lookup("cwd"))
	require.NotNil(t, flags.Lookup("environment"))
	require.NotNil(t, flags.Lookup("output"))
	require.NotNil(t, flags.Lookup("trace-log-file"))
	require.NotNil(t, flags.Lookup("trace-log-url"))

	// Verify shorthand flags resolve to the correct flag
	require.Equal(t, "cwd", flags.ShorthandLookup("C").Name)
	require.Equal(t, "environment", flags.ShorthandLookup("e").Name)
	require.Equal(t, "output", flags.ShorthandLookup("o").Name)

	// Verify hidden flags
	require.True(t, flags.Lookup("trace-log-file").Hidden)
	require.True(t, flags.Lookup("trace-log-url").Hidden)
}

func TestExtensionCommand_UseOverridesName(t *testing.T) {
	cmd, _ := NewExtensionRootCommand(ExtensionCommandOptions{
		Name: "test-ext",
		Use:  "custom-use",
	})

	require.Equal(t, "custom-use", cmd.Use)
}

func TestExtensionCommand_PersistentPreRunE_ReadsFlagsIntoContext(t *testing.T) {
	cmd, extCtx := NewExtensionRootCommand(ExtensionCommandOptions{
		Name: "test-ext",
	})

	// Add a dummy subcommand to run
	sub := &cobra.Command{Use: "sub", RunE: func(cmd *cobra.Command, args []string) error { return nil }}
	cmd.AddCommand(sub)

	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{"sub", "--debug", "--no-prompt", "--environment", "dev"})

	err := cmd.Execute()
	require.NoError(t, err)

	require.True(t, extCtx.Debug)
	require.True(t, extCtx.NoPrompt)
	require.Equal(t, "dev", extCtx.Environment)
}

func TestExtensionCommand_EnvVarFallback(t *testing.T) {
	t.Setenv("AZD_DEBUG", "true")
	t.Setenv("AZD_NO_PROMPT", "1")
	t.Setenv("AZD_ENVIRONMENT", "staging")

	cmd, extCtx := NewExtensionRootCommand(ExtensionCommandOptions{
		Name: "test-ext",
	})

	sub := &cobra.Command{Use: "sub", RunE: func(cmd *cobra.Command, args []string) error { return nil }}
	cmd.AddCommand(sub)

	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{"sub"})

	err := cmd.Execute()
	require.NoError(t, err)

	require.True(t, extCtx.Debug)
	require.True(t, extCtx.NoPrompt)
	require.Equal(t, "staging", extCtx.Environment)
}

func TestExtensionCommand_FlagOverridesEnvVar(t *testing.T) {
	t.Setenv("AZD_ENVIRONMENT", "from-env")

	cmd, extCtx := NewExtensionRootCommand(ExtensionCommandOptions{
		Name: "test-ext",
	})

	sub := &cobra.Command{Use: "sub", RunE: func(cmd *cobra.Command, args []string) error { return nil }}
	cmd.AddCommand(sub)

	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{"sub", "--environment", "from-flag"})

	err := cmd.Execute()
	require.NoError(t, err)

	require.Equal(t, "from-flag", extCtx.Environment)
}

func TestExtensionCommand_OTelTraceContextExtraction(t *testing.T) {
	// A valid W3C traceparent header
	t.Setenv("TRACEPARENT", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	t.Setenv("TRACESTATE", "rojo=00f067aa0ba902b7")

	cmd, extCtx := NewExtensionRootCommand(ExtensionCommandOptions{
		Name: "test-ext",
	})

	sub := &cobra.Command{Use: "sub", RunE: func(cmd *cobra.Command, args []string) error { return nil }}
	cmd.AddCommand(sub)

	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{"sub"})

	err := cmd.Execute()
	require.NoError(t, err)

	ctx := extCtx.Context()
	require.NotNil(t, ctx)

	sc := trace.SpanContextFromContext(ctx)
	require.True(t, sc.HasTraceID(), "expected trace ID to be set from TRACEPARENT")
	require.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", sc.TraceID().String())
}

func TestExtensionCommand_ContextMethodReturnsBackground(t *testing.T) {
	extCtx := &ExtensionContext{}
	ctx := extCtx.Context()
	require.NotNil(t, ctx)
	require.Equal(t, context.Background(), ctx)
}

func TestExtensionCommand_CwdEnvVarFallback(t *testing.T) {
	// Save and restore cwd
	origDir, err := os.Getwd()
	require.NoError(t, err)

	tmpDir := t.TempDir()
	t.Setenv("AZD_CWD", tmpDir)

	cmd, extCtx := NewExtensionRootCommand(ExtensionCommandOptions{
		Name: "test-ext",
	})

	sub := &cobra.Command{Use: "sub", RunE: func(cmd *cobra.Command, args []string) error { return nil }}
	cmd.AddCommand(sub)

	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{"sub"})

	err = cmd.Execute()

	// Restore cwd before any assertions so TempDir cleanup can succeed
	_ = os.Chdir(origDir)

	require.NoError(t, err)
	require.Equal(t, tmpDir, extCtx.Cwd)
}
