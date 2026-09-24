// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKnownCommandFlagValidationEmitsOneJSONError(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"run wait conflict", []string{"run", "start", "--wait", "--no-wait"}, "wait no-wait"},
		{"generate wait conflict", []string{"generate", "--wait", "--no-wait"}, "wait no-wait"},
		{"instruction conflict", []string{"generate", "--agent-instruction", "synthetic",
			"--agent-instruction-file", "does-not-exist.txt"}, "agent-instruction agent-instruction-file"},
		{"job kind missing", []string{"job", "show", "job_1"}, "dataset evaluator"},
		{"job kind conflict", []string{"job", "show", "job_1", "--dataset", "--evaluator"}, "dataset evaluator"},
		{"unknown flag", []string{"run", "start", "--not-a-flag"}, "unknown flag"},
		{"invalid flag value", []string{"run", "start", "--max-samples", "not-a-number"}, "invalid argument"},
		{"unexpected positional argument", []string{"run", "start", "extra"}, "unknown command"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			t.Setenv("AZD_CONFIG_DIR", filepath.Join(dir, "config-not-created"))
			t.Setenv("AZURE_DEV_COLLECT_TELEMETRY", "no")
			root := NewRootCommand()
			var out, stderr bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&stderr)
			root.SetArgs(append([]string{"-o", "json", "--no-prompt"}, tc.args...))
			err := root.ExecuteContext(t.Context())
			require.ErrorContains(t, err, tc.want)
			var doc jsonError
			require.NoError(t, json.Unmarshal(out.Bytes(), &doc), "exactly one JSON document, not prose or empty stdout")
			assert.Contains(t, doc.Error.Message, tc.want)
			assert.NotContains(t, out.String()+stderr.String(), "\x1b[")
			entries, err := os.ReadDir(dir)
			require.NoError(t, err)
			assert.Empty(t, entries, "reject invalid flags before creating local state or generated artifacts")
		})
	}
}

func TestUnknownRootCommandStillFailsBeforeJSONCommandResolution(t *testing.T) {
	root := NewRootCommand()
	var out, stderr bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&stderr)
	root.SetArgs([]string{"typo", "-o", "json"})
	require.ErrorContains(t, root.ExecuteContext(t.Context()), "unknown command",
		"unresolved root command names must not turn into successful help output")
	assert.Empty(t, out.String())
	assert.Nil(t, root.Args)
	group, _, err := root.Find([]string{"run", "output"})
	require.NoError(t, err)
	assert.Nil(t, group.Args, "non-runnable groups retain Cobra's existing command-resolution behavior")
}

func TestFlagValidationPrecedesHooksAndPreservesValidBehavior(t *testing.T) {
	for _, kind := range []string{"required", "together", "exclusive", "one-required"} {
		for _, valid := range []bool{false, true} {
			t.Run(kind+"/"+boolText(valid), func(t *testing.T) {
				var hooks, runs int
				root := &cobra.Command{Use: "eval", SilenceUsage: true, SilenceErrors: true}
				root.PersistentFlags().String("output", "json", "")
				root.PersistentPreRunE = func(*cobra.Command, []string) error { hooks++; return nil }
				child := &cobra.Command{
					Use: "action",
					RunE: func(*cobra.Command, []string) error {
						runs++
						return nil
					},
				}
				child.Flags().Bool("first", false, "")
				child.Flags().Bool("second", false, "")
				args := []string{"action"}
				switch kind {
				case "required":
					require.NoError(t, child.MarkFlagRequired("first"))
				case "together":
					child.MarkFlagsRequiredTogether("first", "second")
					args = append(args, "--second")
				case "exclusive":
					child.MarkFlagsMutuallyExclusive("first", "second")
					args = append(args, "--first")
					if !valid {
						args = append(args, "--second")
					}
				case "one-required":
					child.MarkFlagsOneRequired("first", "second")
				}
				if valid && kind != "exclusive" {
					args = append(args, "--first")
				}
				root.AddCommand(child)
				reportFailuresAsJSON(root)
				var out bytes.Buffer
				root.SetOut(&out)
				root.SetErr(&bytes.Buffer{})
				root.SetArgs(args)
				err := root.ExecuteContext(t.Context())
				if valid {
					require.NoError(t, err)
					assert.Equal(t, 1, hooks)
					assert.Equal(t, 1, runs)
					assert.Empty(t, out.String())
				} else {
					require.Error(t, err)
					assert.Zero(t, hooks, "flag validation must precede side-effecting hooks")
					assert.Zero(t, runs)
					var doc jsonError
					require.NoError(t, json.Unmarshal(out.Bytes(), &doc))
					assert.NotEmpty(t, doc.Error.Message)
				}
			})
		}
	}
}
