// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"regexp"
	"strings"
	"testing"

	"azureaiagent/internal/featureflags"

	"github.com/azure/azure-dev/cli/azd/test/snapshot"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

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
		{[]string{"eval", "run", "--no-wait"}, none},
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
		{featureflags.Voice, []string{"add"}, []string{"Voice and prompt-voice Agent definitions"}, nil},
		{featureflags.ManagedHarness, []string{"init"}, nil, []string{"--harness string", "--voice string"}},
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

func TestPreviewHelpReadsHostAndSelectedEnvironment(t *testing.T) {
	clearPreviewProcess(t)
	t.Chdir(t.TempDir())
	_, env := startPreviewCommandServer(t, map[string]any{
		"agents": map[string]any{"long_running": "on"},
	}, nil, nil)
	text, err := executeRoot(t, "invoke", "-e", "help-env", "--help")
	require.NoError(t, err)
	require.Contains(t, text, "--long-running")
	require.NotContains(t, text, "Voice Agents")
	env.mu.Lock()
	require.Equal(t, "help-env", env.selected)
	env.mu.Unlock()

	t.Setenv("AZD_ENVIRONMENT", "host-env")
	_, err = executeRoot(t, "invoke", "--help")
	require.NoError(t, err)
	env.mu.Lock()
	require.Equal(t, "host-env", env.selected)
	env.mu.Unlock()
}

func TestPreviewHelpFallsBackToProcessOnReadError(t *testing.T) {
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

// TestAgentHelpPreviewsOffHidesPreviewTerms walks every command so help
// text outside the preview annotations cannot name a disabled preview.
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
