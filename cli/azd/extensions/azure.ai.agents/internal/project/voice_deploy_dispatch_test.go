// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const voiceOverrideYAML = `kind: prompt-voice
name: override-voice
model_type: managed
model:
  id: gpt-realtime
voice: alloy
`

const hostedOverrideYAML = `kind: hosted
name: override-hosted
image: myregistry.azurecr.io/agent:v1
`

// writeTempAgentFile writes content to a temp agent.yaml and returns its path.
func writeTempAgentFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// TestVoiceAgentFromDefinitionFile verifies that a prompt-voice override file is
// parsed as a voice agent, while a hosted override is not (found=false) so the
// caller falls through to the container path.
func TestVoiceAgentFromDefinitionFile(t *testing.T) {
	t.Parallel()

	t.Run("voice override is recognized", func(t *testing.T) {
		t.Parallel()
		path := writeTempAgentFile(t, voiceOverrideYAML)

		va, isVoice, err := voiceAgentFromDefinitionFile(path)
		require.NoError(t, err)
		assert.True(t, isVoice)
		assert.Equal(t, "override-voice", va.Name)
		require.NotNil(t, va.Model)
		assert.Equal(t, "gpt-realtime", va.Model.Id)
		require.NotNil(t, va.Voice)
		assert.Equal(t, "alloy", *va.Voice)
	})

	t.Run("hosted override falls through to container path", func(t *testing.T) {
		t.Parallel()
		path := writeTempAgentFile(t, hostedOverrideYAML)

		_, isVoice, err := voiceAgentFromDefinitionFile(path)
		require.NoError(t, err)
		assert.False(t, isVoice)
	})

	t.Run("missing file errors", func(t *testing.T) {
		t.Parallel()
		_, isVoice, err := voiceAgentFromDefinitionFile(filepath.Join(t.TempDir(), "nope.yaml"))
		assert.Error(t, err)
		assert.False(t, isVoice)
	})
}

// TestResolveVoiceAgentForDeploy_OverridePrecedence verifies that an explicit
// AGENT_DEFINITION_PATH override wins over the resolved service entry, so a voice
// override drives the voice dispatch even when the service entry is absent.
func TestResolveVoiceAgentForDeploy_OverridePrecedence(t *testing.T) {
	t.Parallel()

	t.Run("voice override wins", func(t *testing.T) {
		t.Parallel()
		path := writeTempAgentFile(t, voiceOverrideYAML)

		va, isVoice, err := resolveVoiceAgentForDeploy(path, nil, "")
		require.NoError(t, err)
		assert.True(t, isVoice)
		assert.Equal(t, "override-voice", va.Name)
	})

	t.Run("hosted override routes to container path", func(t *testing.T) {
		t.Parallel()
		path := writeTempAgentFile(t, hostedOverrideYAML)

		_, isVoice, err := resolveVoiceAgentForDeploy(path, nil, "")
		require.NoError(t, err)
		assert.False(t, isVoice)
	})
}
