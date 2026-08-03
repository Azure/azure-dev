// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeProjectManifest(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestFindProjectManifest(t *testing.T) {
	t.Parallel()

	t.Run("returns empty when absent", func(t *testing.T) {
		t.Parallel()

		got, err := findProjectManifest(t.TempDir())
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("finds azure.yaml", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		want := writeProjectManifest(t, dir, "azure.yaml", "name: sample\n")

		got, err := findProjectManifest(dir)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	})

	t.Run("finds azure.yml", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		want := writeProjectManifest(t, dir, "azure.yml", "name: sample\n")

		got, err := findProjectManifest(dir)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	})

	t.Run("ignores a directory named azure.yaml", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(dir, "azure.yaml"), 0o750))

		got, err := findProjectManifest(dir)
		require.NoError(t, err)
		assert.Empty(t, got)
	})
}

func TestFindProjectAgentServices(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		want    []projectAgentService
	}{
		{
			name: "inline agent definition on the service entry",
			content: `name: sample
services:
  chat:
    host: azure.ai.agent
    project: .
    kind: hosted
    name: my-chat-agent
`,
			want: []projectAgentService{{ServiceName: "chat", AgentName: "my-chat-agent"}},
		},
		{
			name: "deprecated config-nested definition",
			content: `name: sample
services:
  chat:
    host: azure.ai.agent
    project: .
    config:
      kind: hosted
      name: legacy-agent
`,
			want: []projectAgentService{{ServiceName: "chat", AgentName: "legacy-agent"}},
		},
		{
			name: "falls back to the service key when the agent has no name",
			content: `name: sample
services:
  chat:
    host: azure.ai.agent
    project: .
    kind: hosted
`,
			want: []projectAgentService{{ServiceName: "chat", AgentName: "chat"}},
		},
		{
			name: "multiple agents are sorted by service name",
			content: `name: sample
services:
  zeta:
    host: azure.ai.agent
    kind: hosted
    name: zeta-agent
  alpha:
    host: azure.ai.agent
    kind: hosted
    name: alpha-agent
`,
			want: []projectAgentService{
				{ServiceName: "alpha", AgentName: "alpha-agent"},
				{ServiceName: "zeta", AgentName: "zeta-agent"},
			},
		},
		{
			name: "non-agent Foundry services are ignored",
			content: `name: sample
services:
  project:
    host: azure.ai.project
    name: my-project
  api:
    host: containerapp
`,
			want: nil,
		},
		{
			name: "project with no services",
			content: `name: sample
`,
			want: nil,
		},
		{
			name:    "malformed yaml yields no services rather than an error",
			content: "name: sample\nservices: [this is not a map\n",
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			path := writeProjectManifest(t, dir, "azure.yaml", tt.content)

			got, err := findProjectAgentServices(path)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFindProjectAgentServices_MissingFile(t *testing.T) {
	t.Parallel()

	_, err := findProjectAgentServices(filepath.Join(t.TempDir(), "azure.yaml"))
	require.Error(t, err)
}

func TestDescribeProjectAgentServices(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		services []projectAgentService
		want     string
	}{
		{
			name:     "agent name differs from the service key",
			services: []projectAgentService{{ServiceName: "chat", AgentName: "my-chat-agent"}},
			want:     `"chat" (agent: my-chat-agent)`,
		},
		{
			name:     "agent name matching the service key is not repeated",
			services: []projectAgentService{{ServiceName: "chat", AgentName: "chat"}},
			want:     `"chat"`,
		},
		{
			name: "multiple services are comma separated",
			services: []projectAgentService{
				{ServiceName: "alpha", AgentName: "alpha"},
				{ServiceName: "zeta", AgentName: "zeta-agent"},
			},
			want: `"alpha", "zeta" (agent: zeta-agent)`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, describeProjectAgentServices(tt.services))
		})
	}
}
