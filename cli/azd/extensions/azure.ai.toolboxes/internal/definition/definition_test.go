// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package definition

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadSaveRoundTrip(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), DefaultPath)
	want := &Definition{
		Name:        "support-tools",
		Description: "Support toolbox",
		Connections: []ConnectionReference{{Name: "search", Index: "tickets"}},
		Skills:      []SkillReference{{Name: "triage", Version: "2"}},
		Tools:       []map[string]any{{"type": "web_search", "name": "web"}},
		Policies:    &Policies{RaiConfig: &RaiConfig{RaiPolicyName: "default"}},
		Metadata:    map[string]string{"owner": "support"},
	}

	require.NoError(t, Save(path, want))
	got, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), DefaultPath)
	require.NoError(t, os.WriteFile(path, []byte("name: tools\nunknown: value\n"), 0o600))

	_, err := Load(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown")
}

func TestDefinitionAddConnection(t *testing.T) {
	t.Parallel()

	definition := &Definition{}
	require.NoError(t, definition.AddConnection(ConnectionReference{
		Name: " search ", Index: " tickets ",
	}))
	require.Equal(t, []ConnectionReference{{Name: "search", Index: "tickets"}}, definition.Connections)

	err := definition.AddConnection(ConnectionReference{Name: "search"})
	require.ErrorIs(t, err, ErrDuplicateConnection)
}

func TestDefinitionAddSkill(t *testing.T) {
	t.Parallel()

	definition := &Definition{}
	require.NoError(t, definition.AddSkill(SkillReference{Name: " triage ", Version: " 2 "}))
	require.Equal(t, []SkillReference{{Name: "triage", Version: "2"}}, definition.Skills)

	err := definition.AddSkill(SkillReference{Name: "triage", Version: "3"})
	require.True(t, errors.Is(err, ErrDuplicateSkill))
}

func TestSaveRejectsUnsupportedExtension(t *testing.T) {
	t.Parallel()

	err := Save(filepath.Join(t.TempDir(), "toolbox.toml"), &Definition{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), ".toml")
}
