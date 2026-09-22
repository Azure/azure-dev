// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func TestInitVoiceFlagRejectsUnusedInputsBeforeHostAccess(t *testing.T) {
	t.Setenv("AZD_SERVER", "")
	t.Chdir(t.TempDir())
	for _, args := range [][]string{
		{"--voice", "Ava", "--kind", "hosted"},
		{"--voice=", "--kind", "hosted"},
		{"--voice", "Ava", "--kind", "prompt"},
		{"--voice", "Ava", "--image", "example.azurecr.io/agent:v1"},
		{"--voice", "Ava", "-m", "azure.yaml"},
		{"--voice=", "-m", "azure.yaml"},
		{"--voice", "Ava", "--kind", "prompt-voice", "-m", "azure.yaml"},
		{"--voice", "Ava", "-m", "https://example.com/azure.yaml"},
		{"--voice", "Ava", "azure.yaml"},
		{"--voice", "Ava"}, // no-prompt template default must not silently discard it
	} {
		t.Run(filepath.Join(args...), func(t *testing.T) {
			command := newInitCommand(&azdext.ExtensionContext{NoPrompt: true})
			var output bytes.Buffer
			command.SetOut(&output)
			command.SetErr(&output)
			command.SetArgs(args)
			require.ErrorContains(t, command.Execute(), "--voice is only supported when creating a new prompt voice agent")
			entries, err := os.ReadDir(".")
			require.NoError(t, err)
			require.Empty(t, entries, "invalid options must not generate project files")
		})
	}
}

func TestInitVoiceInputAllowsOnlySupportedNewVoiceFlows(t *testing.T) {
	t.Parallel()
	for _, flags := range []*initFlags{
		{kind: kindFlagPromptVoice, voice: "Ava", noPrompt: true},
		{voice: "Ava"}, // interactive selection remains possible
		{kind: kindFlagPromptVoice, voice: ""},
	} {
		require.NoError(t, validateInitVoiceInput(flags, true))
	}
	// No --voice flag means existing inputs are unaffected, even if otherwise invalid.
	require.NoError(t, validateInitVoiceInput(&initFlags{kind: "hosted", manifestPointer: "azure.yaml"}, false))
}

func TestVoiceDefinitionForInit(t *testing.T) {
	t.Parallel()

	defaulted := voiceDefinitionForInit(&initFlags{}, "voice-agent")
	require.Equal(t, agent_yaml.AgentKindPromptVoice, defaulted.Kind)
	require.Equal(t, agent_yaml.VoiceModelTypeManaged, defaulted.ModelType)
	require.Equal(t, defaultVoiceModel, defaulted.Model.Id)
	require.Nil(t, defaulted.Voice)

	configured := voiceDefinitionForInit(
		&initFlags{model: "gpt-realtime-preview", voice: "en-US-AvaNeural"},
		"configured-voice",
	)
	require.Equal(t, "configured-voice", configured.Name)
	require.Equal(t, "gpt-realtime-preview", configured.Model.Id)
	require.Equal(t, "en-US-AvaNeural", *configured.Voice)
}

func TestInitVoiceFlagInteractiveSelection(t *testing.T) {
	for _, nonempty := range []bool{false, true} {
		for _, specified := range []bool{false, true} {
			count := int32(2)
			if nonempty {
				count = 3
			}
			for index := range count {
				t.Run(fmt.Sprintf("nonempty=%t/voice=%t/choice=%d", nonempty, specified, index), func(t *testing.T) {
					t.Chdir(t.TempDir())
					if nonempty {
						require.NoError(t, os.WriteFile("main.py", []byte("# sample"), 0600))
					}
					prompts := &helpersPromptServer{selectIndex: index}
					client := newHelpersTestAzdClient(t, &helpersProjectServer{}, prompts)
					mode, err := promptInitModeForVoice(t.Context(), client, false, specified, nil)
					if specified && index != count-1 {
						require.ErrorContains(t, err, "--voice is only supported")
					} else {
						require.NoError(t, err)
						if index == count-1 {
							require.Equal(t, initModeVoice, mode)
						}
					}
					require.EqualValues(t, 1, prompts.selectCalls.Load())
				})
			}
		}
	}
}

func TestNewVoiceRejectsExplicitCodeInputsBeforeHostAccess(t *testing.T) {
	t.Setenv("AZD_SERVER", "")
	for _, input := range [][]string{
		{"--src", "./agent"}, {"--src="}, {"--protocol", "responses"}, {"--protocol="},
		{"--deploy-mode", "code"}, {"--deploy-mode", "container"},
		{"--runtime", "python_3_13"}, {"--entry-point", "app.py"}, {"--dep-resolution", "remote_build"},
		{"--model-deployment", "my-deployment"}, {"--description", "description"},
		{"--rai-policy", "none"}, {"--acr-connection", "registry"}, {"--registry-connection", "registry"},
		{"--harness", "github_copilot_preview"}, {"--instructions", "Be helpful"},
	} {
		for _, selector := range [][]string{
			{"--kind", "prompt-voice", "--agent-name", "voice-test"},
			{"--voice", "Ava"},
			{"--voice="},
		} {
			t.Run(fmt.Sprint(input, selector), func(t *testing.T) {
				t.Chdir(t.TempDir())
				command := newInitCommand(nil)
				var output bytes.Buffer
				command.SetOut(&output)
				command.SetErr(&output)
				command.SetArgs(append(append([]string{}, selector...), input...))
				require.ErrorContains(t, command.Execute(), "new prompt voice agents cannot use these init inputs")
				entries, err := os.ReadDir(".")
				require.NoError(t, err)
				require.Empty(t, entries)
			})
		}
	}
	for _, selector := range [][]string{{"--voice", "Ava"}, {"--kind", "prompt-voice", "--agent-name", "voice-test"}} {
		t.Run("positional/"+fmt.Sprint(selector), func(t *testing.T) {
			t.Chdir(t.TempDir())
			require.NoError(t, os.Mkdir("agent", 0700))
			require.NoError(t, os.WriteFile(filepath.Join("agent", "app.py"), []byte("# unchanged"), 0600))
			command := newInitCommand(nil)
			var output bytes.Buffer
			command.SetOut(&output)
			command.SetErr(&output)
			command.SetArgs(append(append([]string{}, selector...), "./agent"))
			require.ErrorContains(t, command.Execute(), "positional source directory")
			content, err := os.ReadFile(filepath.Join("agent", "app.py"))
			require.NoError(t, err)
			require.Equal(t, "# unchanged", string(content))
			entries, err := os.ReadDir(".")
			require.NoError(t, err)
			require.Len(t, entries, 1)
		})
	}
}

func TestInteractiveVoiceRejectsCodeInputsOnlyOnVoiceSelection(t *testing.T) {
	t.Chdir(t.TempDir())
	require.NoError(t, os.WriteFile("app.py", []byte("# existing code"), 0600))
	command := newInitCommand(nil)
	require.NoError(t, command.ParseFlags([]string{"--runtime", "python_3_13", "--protocol", "responses"}))
	inputErr := validateVoiceInitOptions(command, false)
	require.Error(t, inputErr)
	for _, choice := range []struct {
		index int32
		want  string
	}{
		{0, initModeFromCode}, {1, initModeTemplate}, {2, initModeVoice},
	} {
		prompts := &helpersPromptServer{selectIndex: choice.index}
		client := newHelpersTestAzdClient(t, &helpersProjectServer{}, prompts)
		mode, err := promptInitModeForVoice(t.Context(), client, false, false, inputErr)
		if choice.want == initModeVoice {
			require.ErrorIs(t, err, inputErr)
		} else {
			require.NoError(t, err)
			require.Equal(t, choice.want, mode)
		}
		require.EqualValues(t, 1, prompts.selectCalls.Load())
	}
}

func TestNewVoiceAllowsEffectiveOptions(t *testing.T) {
	t.Parallel()
	command := newInitCommand(nil)
	require.NoError(t, command.ParseFlags([]string{
		"--kind", "prompt-voice", "--agent-name", "voice-test", "--voice", "Ava", "--model", "gpt-realtime",
		"--project-id", "project", "--infra=bicep", "--force",
	}))
	require.NoError(t, validateVoiceInitOptions(command, false))
	// Defaults alone must never trigger the new check.
	require.NoError(t, validateVoiceInitOptions(newInitCommand(nil), false))
}
