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
	path, cleanup, err := synthesizeVoiceManifestFile("voice-test", "gpt-realtime", "en-US-AvaNeural")
	require.NoError(t, err)
	t.Cleanup(cleanup)
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	definition, err := agent_yaml.ExtractAgentDefinition(content)
	require.NoError(t, err)
	voice, ok := definition.(agent_yaml.VoiceAgent)
	require.True(t, ok)
	require.NotNil(t, voice.Voice)
	require.Equal(t, "en-US-AvaNeural", *voice.Voice)
}

func TestInitVoiceFlagInteractiveSelection(t *testing.T) {
	for _, nonempty := range []bool{false, true} {
		for _, specified := range []bool{false, true} {
			count := 2
			if nonempty {
				count = 3
			}
			for index := range count {
				t.Run(fmt.Sprintf("nonempty=%t/voice=%t/choice=%d", nonempty, specified, index), func(t *testing.T) {
					t.Chdir(t.TempDir())
					if nonempty {
						require.NoError(t, os.WriteFile("main.py", []byte("# sample"), 0600))
					}
					prompts := &helpersPromptServer{selectIndex: int32(index)}
					client := newHelpersTestAzdClient(t, &helpersProjectServer{}, prompts)
					mode, err := promptInitModeForVoice(t.Context(), client, false, specified)
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
