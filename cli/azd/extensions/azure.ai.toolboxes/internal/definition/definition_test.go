// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package definition

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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
		Connections: []ConnectionReference{{Name: "search", Index: "tickets", InstanceName: "docs-config"}},
		Skills:      []SkillReference{{Name: "triage", Version: "2"}},
		Tools:       []map[string]any{{"type": "web_search", "name": "web"}},
		Policies:    &Policies{RaiConfig: &RaiConfig{RaiPolicyName: "default"}},
		Metadata:    map[string]string{"owner": "support"},
	}

	require.NoError(t, Save(path, want))
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	text := string(content)
	assert.Contains(t, text, "instanceName: docs-config")
	assert.Contains(t, text, "raiConfig:")
	assert.Contains(t, text, "raiPolicyName: default")
	assert.NotContains(t, text, "instance_name")
	assert.NotContains(t, text, "rai_config")
	assert.NotContains(t, text, "rai_policy_name")

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

func TestLoadRejectsLegacySnakeCaseFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		ext     string
		content string
		want    string
	}{
		{
			name: "yaml connection instance",
			ext:  ".yaml",
			content: `connections:
  - name: search
    instance_name: docs-config
`,
			want: "instance_name",
		},
		{
			name:    "json connection instance",
			ext:     ".json",
			content: `{"connections":[{"name":"search","instance_name":"docs-config"}]}`,
			want:    "instance_name",
		},
		{
			name: "yaml RAI config",
			ext:  ".yaml",
			content: `policies:
  rai_config:
    raiPolicyName: default
`,
			want: "rai_config",
		},
		{
			name:    "json RAI config",
			ext:     ".json",
			content: `{"policies":{"rai_config":{"raiPolicyName":"default"}}}`,
			want:    "rai_config",
		},
		{
			name: "yaml RAI policy name",
			ext:  ".yaml",
			content: `policies:
  raiConfig:
    rai_policy_name: default
`,
			want: "rai_policy_name",
		},
		{
			name:    "json RAI policy name",
			ext:     ".json",
			content: `{"policies":{"raiConfig":{"rai_policy_name":"default"}}}`,
			want:    "rai_policy_name",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "toolbox"+test.ext)
			require.NoError(t, os.WriteFile(path, []byte(strings.TrimSpace(test.content)), 0o600))

			_, err := Load(path)
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.want)
		})
	}
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
