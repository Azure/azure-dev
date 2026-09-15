// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agentkind

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

func mustStruct(t *testing.T, m map[string]any) *structpb.Struct {
	t.Helper()
	s, err := structpb.NewStruct(m)
	require.NoError(t, err)
	return s
}

func TestKind_InlineOnEntry(t *testing.T) {
	t.Parallel()
	svc := &azdext.ServiceConfig{
		Name:                 "voice",
		AdditionalProperties: mustStruct(t, map[string]any{"kind": "prompt-voice"}),
	}
	isVoice, err := IsPromptVoice(svc, t.TempDir(), "")
	require.NoError(t, err)
	assert.True(t, isVoice)
}

func TestKind_LegacyConfigOnEntry(t *testing.T) {
	t.Parallel()
	svc := &azdext.ServiceConfig{
		Name:   "voice",
		Config: mustStruct(t, map[string]any{"kind": "prompt-voice"}),
	}
	isVoice, err := IsPromptVoice(svc, t.TempDir(), "")
	require.NoError(t, err)
	assert.True(t, isVoice)
}

// TestKind_ManifestFallback is the regression for the legacy shape: the service
// entry carries no kind, so the kind must be read from the on-disk agent.yaml.
// This is the case where the deploy path (which reads the manifest) and the
// endpoint/next-step readers previously disagreed.
func TestKind_ManifestFallback(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	svcDir := filepath.Join(root, "svc")
	require.NoError(t, os.MkdirAll(svcDir, 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(svcDir, "agent.yaml"),
		[]byte("kind: prompt-voice\nname: concierge\n"), 0o600))

	svc := &azdext.ServiceConfig{Name: "voice", RelativePath: "svc"}
	isVoice, err := IsPromptVoice(svc, root, "")
	require.NoError(t, err)
	assert.True(t, isVoice, "kind must be resolved from the on-disk manifest")
}

func TestKind_OverridePathWins(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	override := filepath.Join(root, "custom-def.yaml")
	require.NoError(t, os.WriteFile(override, []byte("kind: prompt-voice\n"), 0o600))

	// Entry declares hosted, but the explicit override file declares voice and
	// must win, matching the deploy-time AGENT_DEFINITION_PATH precedence.
	svc := &azdext.ServiceConfig{
		Name:                 "voice",
		AdditionalProperties: mustStruct(t, map[string]any{"kind": "hosted"}),
	}
	isVoice, err := IsPromptVoice(svc, root, override)
	require.NoError(t, err)
	assert.True(t, isVoice)
}

func TestKind_HostedIsNotVoice(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	svcDir := filepath.Join(root, "svc")
	require.NoError(t, os.MkdirAll(svcDir, 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(svcDir, "agent.yaml"),
		[]byte("kind: hosted\nname: worker\n"), 0o600))

	svc := &azdext.ServiceConfig{
		Name:                 "worker",
		RelativePath:         "svc",
		AdditionalProperties: mustStruct(t, map[string]any{"kind": "hosted"}),
	}
	isVoice, err := IsPromptVoice(svc, root, "")
	require.NoError(t, err)
	assert.False(t, isVoice)
	isHosted, err := IsHosted(svc, root, "")
	require.NoError(t, err)
	assert.True(t, isHosted)
}

func TestKind_AbsentReturnsEmpty(t *testing.T) {
	t.Parallel()
	svc := &azdext.ServiceConfig{Name: "worker", RelativePath: "svc"}
	kind, err := Kind(svc, t.TempDir(), "")
	require.NoError(t, err)
	assert.Empty(t, kind)
}
