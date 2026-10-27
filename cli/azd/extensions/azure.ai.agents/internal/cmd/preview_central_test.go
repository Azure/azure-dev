// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/featureflags"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/test/snapshot"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

func TestPreviewEnforcementIgnoresEnvironmentSelection(t *testing.T) {
	for _, environment := range []string{"", " \t ", "selected", "missing"} {
		for _, value := range []string{"false", "true"} {
			t.Run(environment+"/"+value, func(t *testing.T) {
				clearPreviewProcess(t)
				t.Setenv("AZD_ENVIRONMENT", "default-env")
				_, env := startPreviewCommandServer(t, map[string]any{
					"agents": map[string]any{"voice": "on", "long_running": "on", "managed_harness": "on"},
				}, nil, nil)
				env.err = errors.New("host must not be read")
				for _, feature := range featureflags.All() {
					t.Setenv(feature.EnvVar, value)
					cmd := &cobra.Command{Use: "check"}
					cmd.SetContext(t.Context())
					cmd.Flags().String("environment", "", "")
					require.NoError(t, cmd.ParseFlags([]string{"--environment", environment}))
					previewCommand(cmd, feature)
					err := enforcePreviewFeatures(cmd, nil)
					if value == "true" {
						require.NoError(t, err)
					} else {
						require.ErrorContains(t, err, feature.ID)
					}
				}
				require.Zero(t, env.reads.Load(), "preview checks must not read the host")
			})
		}
	}
}

func TestPreviewHelpEmptyEnvironmentUsesOnlyProcess(t *testing.T) {
	for _, value := range []string{"", " \t "} {
		t.Run(value, func(t *testing.T) {
			clearPreviewProcess(t)
			t.Setenv("AZD_ENVIRONMENT", "default-env")
			t.Setenv(featureflags.Voice.EnvVar, "true")
			_, env := startPreviewCommandServer(t, map[string]any{
				"agents": map[string]any{"long_running": "on"},
			}, nil, nil)
			text, err := executeRoot(t, "invoke", "--environment", value, "--help")
			require.NoError(t, err)
			require.Contains(t, text, "Voice Agents")
			require.NotContains(t, text, "--long-running")
			require.Zero(t, env.reads.Load())
		})
	}
}

func TestPreviewLocalValidationPrecedesGate(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		code    string
		message string
	}{
		{[]string{"invoke", "--no-wait", "hi"}, exterrors.CodeConflictingArguments,
			"--no-wait requires --long-running"},
		{[]string{"invoke", "--long-running", "--local", "hi"}, exterrors.CodeInvalidParameter,
			"--long-running is supported only for remote Responses agents"},
		{[]string{"invoke", "--long-running", "--timeout", "5", "hi"}, exterrors.CodeConflictingArguments,
			"--timeout cannot be used with --long-running"},
		{[]string{"invoke", "--protocol", "bogus", "--long-running", "hi"}, exterrors.CodeInvalidParameter,
			`unsupported protocol "bogus" for invocation`},
		{[]string{"invoke", "--protocol", "a2a", "--long-running", "hi"}, exterrors.CodeInvalidParameter,
			"--long-running is not supported with the a2a protocol"},
		{[]string{"invoke", "--protocol", "invocations", "--long-running", "hi"}, exterrors.CodeInvalidParameter,
			"--long-running is not supported with the invocations protocol"},
		{[]string{"invoke", "--protocol", "a2a", "--long-running", "--no-wait", "hi"}, exterrors.CodeInvalidParameter,
			"--long-running is not supported with the a2a protocol"},
		{[]string{"invoke", "--protocol", "invocations", "--long-running", "--no-wait", "hi"},
			exterrors.CodeInvalidParameter, "--long-running is not supported with the invocations protocol"},
		{[]string{"invoke", "--long-running", "--output", "raw", "hi"}, exterrors.CodeInvalidParameter,
			"--output raw is not supported with --long-running"},
		{[]string{"init", "--voice", "x", "--kind", "hosted"}, exterrors.CodeInvalidParameter,
			"--voice is only supported when creating a new prompt voice agent"},
		{[]string{"init", "--kind", "prompt-voice", "--deploy-mode", "code"}, exterrors.CodeConflictingArguments,
			"new prompt voice agents cannot use these init inputs: --deploy-mode"},
		{[]string{"init", "--voice", "x", "-t", "a.yaml", "-m", "b.yaml"}, exterrors.CodeConflictingArguments,
			"cannot pass both --template and deprecated --manifest"},
		{[]string{"init", "--kind", "prompt-voice"}, exterrors.CodeInvalidParameter,
			"--kind prompt-voice requires --agent-name"},
		{[]string{"init", "--kind", "hosted", "--harness", "microsoft_copilot_preview"},
			exterrors.CodeInvalidParameter, "--harness is only valid with --kind prompt"},
		{[]string{"init", "--kind", "prompt", "--harness", "microsoft_copilot_preview", "--deploy-mode", "bogus"},
			exterrors.CodeInvalidParameter, "invalid --deploy-mode value \"bogus\"; must be 'container' or 'code'"},
		{[]string{"init", "--kind", "prompt-voice", "--agent-name", "agent", "--infra=bogus"},
			exterrors.CodeInvalidParameter, `unsupported --infra value "bogus"`},
		{[]string{"invoke", "--no-wait", "--environment", "", "hi"}, exterrors.CodeConflictingArguments,
			"--no-wait requires --long-running"},
		{[]string{"invoke", "--long-running", "hi"}, exterrors.CodeFeatureNotEnabled,
			`preview feature "agents.long_running" is disabled`},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			clearPreviewProcess(t)
			for _, feature := range featureflags.All() {
				require.NoError(t, os.Unsetenv(feature.EnvVar))
			}
			t.Chdir(t.TempDir())
			_, env := startPreviewCommandServer(t, nil, nil, nil)
			_, err := executeRoot(t, tc.args...)
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok, "%v", err)
			require.Equal(t, tc.code, localErr.Code)
			if tc.message != "" {
				require.Equal(t, tc.message, localErr.Message)
			}
			require.Zero(t, env.reads.Load())
			entries, err := os.ReadDir(".")
			require.NoError(t, err)
			require.Empty(t, entries)
		})
	}
}

func TestPreviewLocalValidatorRunsOnlyOnRejection(t *testing.T) {
	for _, tc := range []struct {
		name            string
		required        bool
		value           string
		environmentArgs []string
		wantCalls       int
	}{
		{name: "no feature"},
		{name: "no feature with empty environment", environmentArgs: []string{"--environment", ""}},
		{name: "enabled", required: true, value: "true"},
		{name: "disabled", required: true, wantCalls: 1},
		{name: "invalid value", required: true, value: "on", wantCalls: 1},
		{name: "empty environment", required: true, value: "true",
			environmentArgs: []string{"--environment", ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearPreviewProcess(t)
			t.Setenv(featureflags.Voice.EnvVar, tc.value)
			localErr := errors.New("local validation failed")
			calls, preRuns, runs := 0, 0, 0
			command := &cobra.Command{
				Use:     "check",
				PreRunE: func(*cobra.Command, []string) error { preRuns++; return nil },
				RunE:    func(*cobra.Command, []string) error { runs++; return nil },
			}
			if tc.required {
				previewCommand(command, featureflags.Voice)
			}
			validateBeforePreview(command, func(*cobra.Command, []string) error {
				calls++
				return localErr
			})
			root := &cobra.Command{
				Use: "root",
				PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
					return enforcePreviewFeatures(cmd, args)
				},
			}
			root.PersistentFlags().String("environment", "", "")
			root.AddCommand(command)
			root.SetArgs(append([]string{"check"}, tc.environmentArgs...))
			err := root.ExecuteContext(t.Context())
			if tc.wantCalls > 0 {
				require.ErrorIs(t, err, localErr)
				require.Zero(t, preRuns)
				require.Zero(t, runs)
			} else {
				require.NoError(t, err)
				require.Equal(t, 1, preRuns)
				require.Equal(t, 1, runs)
			}
			require.Equal(t, tc.wantCalls, calls)
		})
	}
}

func TestPreviewInitVoiceEnabledSkipsLocalValidator(t *testing.T) {
	clearPreviewProcess(t)
	t.Setenv(featureflags.Voice.EnvVar, "true")
	startPreviewCommandServer(t, nil, nil, nil)
	root := NewRootCommand()
	command, _, err := root.Find([]string{"init"})
	require.NoError(t, err)
	require.Nil(t, command.PreRunE)
	calls, runs := 0, 0
	validateBeforePreview(command, func(*cobra.Command, []string) error {
		calls++
		return errors.New("preview validator must not run")
	})
	command.RunE = func(*cobra.Command, []string) error { runs++; return nil }
	root.SetArgs([]string{"init", "--kind", "prompt-voice", "--agent-name", "voice-agent"})
	require.NoError(t, root.ExecuteContext(t.Context()))
	require.Zero(t, calls)
	require.Equal(t, 1, runs)
}

func TestPreviewLocalValidatorsPreserveFlags(t *testing.T) {
	invoke := newInvokeCommand(nil)
	require.NoError(t, invoke.ParseFlags([]string{"--version", " 3 "}))
	flags := invokeFlags{message: "hi", version: " 3 ", clientHeaders: []string{"x-client-test: value"}}
	before := flags
	for range 2 {
		require.NoError(t, validateInvokeArgs(invoke, &flags, false))
		require.Equal(t, before, flags)
		value, err := invoke.Flags().GetString("version")
		require.NoError(t, err)
		require.Equal(t, " 3 ", value)
		require.True(t, invoke.Flags().Changed("version"))
	}

	init := newInitCommand(nil)
	require.NoError(t, init.ParseFlags([]string{"--kind", "prompt-voice", "--agent-name", " agent "}))
	initFlags := initFlags{kind: kindFlagPromptVoice, agentName: " agent "}
	initBefore := initFlags
	for range 2 {
		require.NoError(t, validateInitPreviewArgs(init, nil, initFlags))
		require.Equal(t, initBefore, initFlags)
		value, err := init.Flags().GetString("agent-name")
		require.NoError(t, err)
		require.Equal(t, " agent ", value)
		require.True(t, init.Flags().Changed("agent-name"))
	}
}

func TestInitPreviewValidationDefersUnresolvedPaths(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{
			name: "positional template",
			args: []string{"--kind", "prompt-voice", "azure.yaml"},
		},
		{
			name: "infra may eject before kind checks",
			args: []string{"--kind", "hosted", "--harness", "microsoft_copilot_preview", "--infra", "bicep"},
		},
		{
			name: "infra may eject before template checks",
			args: []string{"--template", "azure.yaml", "--kind", "prompt-voice", "--infra", "terraform"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command := newInitCommand(nil)
			require.NoError(t, command.ParseFlags(tc.args))
			validator, ok := previewValidators.Load(command)
			require.True(t, ok)
			require.NoError(t, validator.(func(*cobra.Command, []string) error)(command, command.Flags().Args()))
		})
	}
}

func TestRequiredPreviewFeatures(t *testing.T) {
	none := []featureflags.Feature(nil)
	for _, tc := range []struct {
		args []string
		want []featureflags.Feature
	}{
		{[]string{"init", "--kind", "prompt-voice"}, []featureflags.Feature{featureflags.Voice}},
		{[]string{"init", "--kind", " PROMPT-VOICE "}, []featureflags.Feature{featureflags.Voice}},
		{[]string{"init", "--kind", "prompt"}, none},
		{[]string{"init", "--kind", "hosted"}, none},
		{[]string{"init", "--voice", "en-US-Ava"}, []featureflags.Feature{featureflags.Voice}},
		{[]string{"init", "--harness", "microsoft_copilot_preview"}, []featureflags.Feature{featureflags.ManagedHarness}},
		{[]string{"init", "--harness", " GitHub_Copilot_Preview"}, []featureflags.Feature{featureflags.ManagedHarness}},
		{[]string{"init", "--harness", "none"}, none},
		{[]string{"init", "--harness", "unknown"}, none},
		{[]string{"init", "--kind", "prompt-voice", "--voice", "v", "--harness", "microsoft_copilot_preview"},
			[]featureflags.Feature{featureflags.Voice, featureflags.ManagedHarness}},
		{[]string{"invoke", "--long-running"}, []featureflags.Feature{featureflags.LongRunning}},
		{[]string{"invoke", "--long-running=false"}, none},
		{[]string{"invoke", "--no-wait"}, []featureflags.Feature{featureflags.LongRunning}},
		{[]string{"invoke", "--long-running", "--no-wait"}, []featureflags.Feature{featureflags.LongRunning}},
		{[]string{"invoke", "--protocol", "responses"}, none},
		{[]string{"invocations", "show"}, none},
		{[]string{"invocations", "follow"}, none},
		{[]string{"invocations", "cancel"}, none},
		{[]string{"show"}, none},
		{[]string{"delete"}, none},
		{[]string{"endpoint", "show"}, none},
		{[]string{"eval", "run"}, none},
		{[]string{"optimize", "--no-wait"}, none},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			root := NewRootCommand()
			command, rest, err := root.Find(tc.args)
			require.NoError(t, err)
			require.NoError(t, command.ParseFlags(rest))
			got := requiredPreviewFeatures(command)
			if len(tc.want) == 0 {
				require.Empty(t, got)
				return
			}
			require.ElementsMatch(t, tc.want, got)
		})
	}
}

func TestPreviewCommandAnnotationIsInherited(t *testing.T) {
	parent := &cobra.Command{Use: "parent"}
	child := &cobra.Command{Use: "child", Run: func(*cobra.Command, []string) {}}
	parent.AddCommand(child)
	previewCommand(parent, featureflags.Voice)
	require.Equal(t, []featureflags.Feature{featureflags.Voice}, requiredPreviewFeatures(child))

	applyPreviewVisibility(parent, map[string]bool{})
	require.True(t, parent.Hidden)
}

func TestServiceGateAnnotations(t *testing.T) {
	root := NewRootCommand()
	for _, path := range [][]string{{"invoke"}, {"run"}, {"optimize", "deploy"}} {
		command, _, err := root.Find(path)
		require.NoError(t, err)
		require.NotEmpty(t, command.Annotations[serviceGateAnnotation], path)
	}
	for _, path := range [][]string{
		{"show"}, {"delete"}, {"endpoint", "show"}, {"invocations", "show"},
		{"invocations", "follow"}, {"invocations", "cancel"}, {"optimize", "list"}, {"eval", "run"},
	} {
		command, _, err := root.Find(path)
		require.NoError(t, err)
		require.Empty(t, command.Annotations[serviceGateAnnotation], path)
	}
}

func TestPreviewHelpHiddenWhenOff(t *testing.T) {
	setPreviewProcess(t, "")
	for _, tc := range []struct {
		path   []string
		absent []string
	}{
		{nil, []string{"\n  invocations"}},
		{[]string{"invoke"}, []string{
			"--long-running", "--no-wait", "Long-running Invocations", "Run the long task", "Voice Agents",
		}},
		{[]string{"init"}, []string{
			"--voice string", "--harness string", "'prompt-voice'", "--kind prompt-voice",
			"Voice Agents", "support-voice", "Preview opt-ins",
		}},
		{[]string{"add"}, []string{"alpha.agents"}},
	} {
		t.Run(strings.Join(tc.path, "_"), func(t *testing.T) {
			text, err := executeRoot(t, append(tc.path, "--help")...)
			require.NoError(t, err)
			for _, unwanted := range tc.absent {
				require.NotContains(t, text, unwanted)
			}
		})
	}
	for _, path := range [][]string{{"invocations"}, {"invocations", "show"}, {"invocations", "cancel"}} {
		text, err := executeRoot(t, append(path, "--help")...)
		require.NoError(t, err)
		require.Contains(t, text, "azd ai agent invocations", "hidden group stays reachable")
	}
}

func TestPreviewHelpShownPerFeature(t *testing.T) {
	for _, tc := range []struct {
		feature featureflags.Feature
		path    []string
		present []string
		absent  []string
	}{
		{featureflags.LongRunning, []string{"invoke"},
			[]string{"--long-running", "--no-wait", "Long-running Invocations", "Run the long task"},
			[]string{"Voice Agents"}},
		{featureflags.LongRunning, nil, []string{"\n  invocations"}, nil},
		{featureflags.Voice, []string{"init"},
			[]string{"--voice string", "prompt-voice", "Voice Agents", "support-voice"}, []string{"--harness string"}},
		{featureflags.Voice, []string{"invoke"}, []string{"Voice Agents"}, []string{"--long-running"}},
		{featureflags.Voice, []string{"init"},
			[]string{"--image, and prompt-voice agents", "For new managed prompt voice agents"}, nil},
		{featureflags.Voice, []string{"delete"}, []string{"telephony bindings"}, nil},
		{featureflags.Voice, []string{"endpoint", "show"}, []string{"Voice agents display"}, nil},
		{featureflags.Voice, []string{"add"},
			[]string{"deployment), voice, or prompt-voice Agent."},
			[]string{"Voice and prompt-voice Agent definitions are also supported."}},
		{featureflags.ManagedHarness, []string{"init"},
			[]string{"including one using --harness"}, []string{"--harness string", "--voice string"}},
	} {
		t.Run(tc.feature.ID+"/"+strings.Join(tc.path, "_"), func(t *testing.T) {
			setPreviewProcess(t, "")
			t.Setenv(tc.feature.EnvVar, "true")
			text, err := executeRoot(t, append(tc.path, "--help")...)
			require.NoError(t, err)
			for _, want := range tc.present {
				require.Contains(t, text, want)
			}
			for _, unwanted := range tc.absent {
				require.NotContains(t, text, unwanted)
			}
		})
	}
}

func TestPreviewHelpTextReplacement(t *testing.T) {
	command := &cobra.Command{
		Use: "check", Short: "preview caption", Long: "preview details", Example: "preview example",
	}
	previewHelpText(command, featureflags.A2A, "preview caption", "legacy caption")
	previewHelpText(command, featureflags.A2A, "preview details", "legacy details")
	previewHelpText(command, featureflags.A2A, "preview example", "legacy example")
	require.Panics(t, func() {
		previewHelpText(command, featureflags.A2A, "missing example", "replacement")
	})

	applyPreviewVisibility(command, map[string]bool{featureflags.A2A.ID: true})
	require.Equal(t, "preview caption", command.Short)
	require.Equal(t, "preview details", command.Long)
	require.Equal(t, "preview example", command.Example)
	applyPreviewVisibility(command, map[string]bool{})
	require.Equal(t, "legacy caption", command.Short)
	require.Equal(t, "legacy details", command.Long)
	require.Equal(t, "legacy example", command.Example)
}

func TestPreviewHelpEnabledPreservesStaticContent(t *testing.T) {
	root, original := NewRootCommand(), NewRootCommand()
	states := map[string]bool{}
	for _, feature := range featureflags.All() {
		states[feature.ID] = true
	}
	applyPreviewVisibility(root, states)

	var compare func(*cobra.Command, *cobra.Command)
	compare = func(actual, expected *cobra.Command) {
		require.Equal(t, expected.Short, actual.Short, actual.CommandPath())
		require.Equal(t, expected.Long, actual.Long, actual.CommandPath())
		require.Equal(t, expected.Example, actual.Example, actual.CommandPath())
		require.Equal(t, expected.Hidden, actual.Hidden, actual.CommandPath())
		expectedFlags, actualFlags := expected.LocalFlags(), actual.LocalFlags()
		require.Equal(t, expectedFlags.FlagUsages(), actualFlags.FlagUsages(), actual.CommandPath())
		expectedFlags.VisitAll(func(flag *pflag.Flag) {
			actualFlag := actualFlags.Lookup(flag.Name)
			require.NotNil(t, actualFlag, actual.CommandPath(), flag.Name)
			require.Equal(t, flag.Usage, actualFlag.Usage, actual.CommandPath(), flag.Name)
			require.Equal(t, flag.Hidden, actualFlag.Hidden, actual.CommandPath(), flag.Name)
		})
		require.Len(t, actual.Commands(), len(expected.Commands()), actual.CommandPath())
		for i, child := range actual.Commands() {
			compare(child, expected.Commands()[i])
		}
	}
	compare(root, original)
}

func TestPreviewHelpIgnoresHostAndEnvironmentSelection(t *testing.T) {
	clearPreviewProcess(t)
	t.Chdir(t.TempDir())
	_, env := startPreviewCommandServer(t, map[string]any{
		"agents": map[string]any{"long_running": "on"},
	}, nil, nil)
	text, err := executeRoot(t, "invoke", "-e", "help-env", "--help")
	require.NoError(t, err)
	require.NotContains(t, text, "--long-running")
	require.NotContains(t, text, "Voice Agents")
	require.Zero(t, env.reads.Load())

	t.Setenv("AZD_ENVIRONMENT", "host-env")
	t.Setenv(featureflags.LongRunning.EnvVar, "true")
	text, err = executeRoot(t, "invoke", "--help")
	require.NoError(t, err)
	require.Contains(t, text, "--long-running")
	require.Zero(t, env.reads.Load())
}

func TestPreviewHelpNeedsNoHost(t *testing.T) {
	clearPreviewProcess(t)
	t.Setenv("AZD_SERVER", "127.0.0.1:1")
	t.Setenv("AZD_ACCESS_TOKEN", "test-token")
	t.Setenv(featureflags.LongRunning.EnvVar, "true")
	text, err := executeRoot(t, "invoke", "--help")
	require.NoError(t, err)
	require.Contains(t, text, "--long-running")
	require.NotContains(t, text, "Voice Agents")
}

func TestAgentHelpPreviewsOff(t *testing.T) {
	setPreviewProcess(t, "")
	t.Setenv("NO_COLOR", "1")
	previous := color.NoColor
	color.NoColor = true
	t.Cleanup(func() { color.NoColor = previous })
	for _, path := range [][]string{nil, {"init"}, {"invoke"}} {
		name := strings.Join(path, "_")
		if name == "" {
			name = "root"
		}
		t.Run(name, func(t *testing.T) {
			text, err := executeRoot(t, append(path, "--help")...)
			require.NoError(t, err)
			lines := strings.Split(text, "\n")
			for i, line := range lines {
				lines[i] = strings.TrimRight(line, " \t\r")
			}
			snapshot.SnapshotT(t, strings.TrimRight(strings.Join(lines, "\n"), "\n"))
		})
	}
}

// Disabled help hides preview terms outside the root switch list.
func TestAgentHelpPreviewsOffHidesPreviewTerms(t *testing.T) {
	setPreviewProcess(t, "")
	t.Setenv("NO_COLOR", "1")
	previous := color.NoColor
	color.NoColor = true
	t.Cleanup(func() { color.NoColor = previous })
	terms := regexp.MustCompile(`(?i)\bvoice\b|prompt-voice|harness|long-running`)
	var walk func(*cobra.Command, []string)
	walk = func(command *cobra.Command, path []string) {
		t.Run(strings.Join(append([]string{"root"}, path...), "_"), func(t *testing.T) {
			text, err := executeRoot(t, append(append([]string{}, path...), "--help")...)
			require.NoError(t, err)
			if len(path) == 0 {
				require.Contains(t, text, "\nPreview Feature Switches\n")
				text, _, _ = strings.Cut(text, "\nPreview Feature Switches\n")
			}
			require.Empty(t, terms.FindAllString(text, -1))
		})
		for _, child := range command.Commands() {
			if child.IsAvailableCommand() {
				walk(child, append(append([]string{}, path...), child.Name()))
			}
		}
	}
	walk(NewRootCommand(), nil)
}
