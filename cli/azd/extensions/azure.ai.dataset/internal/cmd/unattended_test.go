// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// azd never sets this extension's --no-prompt flag. It folds CI detection,
// agent detection, --non-interactive and AZD_NON_INTERACTIVE into AZD_NO_PROMPT
// in the extension's environment instead, so a check that reads only the flag
// treats every unattended invocation as interactive: the confirm RPC returns
// its default of no, delete reports the dataset left alone, and the command
// exits 0 without ever asking for --force.
func TestNoPromptHonoursTheEnvironmentAzdActuallySets(t *testing.T) {
	newCmd := func() *cobra.Command {
		cmd := &cobra.Command{Use: "delete"}
		cmd.Flags().Bool("no-prompt", false, "")
		cmd.Flags().String("output", "", "")
		return cmd
	}

	t.Run("quiet by default", func(t *testing.T) {
		t.Setenv("AZD_NO_PROMPT", "")
		assert.False(t, noPrompt(newCmd()))
	})

	t.Run("env alone is enough", func(t *testing.T) {
		t.Setenv("AZD_NO_PROMPT", "true")
		assert.True(t, noPrompt(newCmd()),
			"an unattended run must reach the --force requirement, not the prompt")
	})

	t.Run("the flag still works on its own", func(t *testing.T) {
		t.Setenv("AZD_NO_PROMPT", "")
		cmd := newCmd()
		require.NoError(t, cmd.Flags().Set("no-prompt", "true"))
		assert.True(t, noPrompt(cmd))
	})
}

// The host consumes its own global --debug and forwards it as AZD_DEBUG, so
// reading only AZD_EXT_DEBUG left the documented `azd --debug ai dataset ...`
// writing no diagnostics at all.
func TestIsDebugReadsBothTheHostAndExtensionKeys(t *testing.T) {
	flags := func() *pflag.FlagSet {
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		fs.Bool("debug", false, "")
		return fs
	}

	t.Run("off by default", func(t *testing.T) {
		t.Setenv("AZD_DEBUG", "")
		t.Setenv("AZD_EXT_DEBUG", "")
		assert.False(t, isDebug(flags()))
	})

	for _, key := range []string{"AZD_DEBUG", "AZD_EXT_DEBUG"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv("AZD_DEBUG", "")
			t.Setenv("AZD_EXT_DEBUG", "")
			t.Setenv(key, "true")
			assert.True(t, isDebug(flags()))
		})
	}

	// A value that is not a bool is not a request to turn diagnostics on.
	t.Run("unparseable is off", func(t *testing.T) {
		t.Setenv("AZD_DEBUG", "sure")
		t.Setenv("AZD_EXT_DEBUG", "")
		assert.False(t, isDebug(flags()))
	})
}
