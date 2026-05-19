// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/connections/pkg/connections"
	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/stretchr/testify/require"
)

func TestExtractConnectionRefs(t *testing.T) {
	tests := []struct {
		name    string
		envVars []agent_yaml.EnvironmentVariable
		want    []connRef
	}{
		{
			name: "single connection ref",
			envVars: []agent_yaml.EnvironmentVariable{
				{
					Name:  "TAVILY_API_KEY",
					Value: "${{connections.my-tavily.credentials.x-api-key}}",
				},
			},
			want: []connRef{
				{
					EnvName:  "TAVILY_API_KEY",
					ConnName: "my-tavily",
					CredKey:  "x-api-key",
				},
			},
		},
		{
			name: "multiple refs",
			envVars: []agent_yaml.EnvironmentVariable{
				{
					Name:  "KEY1",
					Value: "${{connections.conn-a.credentials.key}}",
				},
				{
					Name:  "KEY2",
					Value: "${{connections.conn-b.credentials.token}}",
				},
			},
			want: []connRef{
				{EnvName: "KEY1", ConnName: "conn-a", CredKey: "key"},
				{EnvName: "KEY2", ConnName: "conn-b", CredKey: "token"},
			},
		},
		{
			name: "no refs — literal values",
			envVars: []agent_yaml.EnvironmentVariable{
				{Name: "PORT", Value: "8080"},
				{Name: "HOST", Value: "localhost"},
			},
			want: nil,
		},
		{
			name: "mixed — only refs extracted",
			envVars: []agent_yaml.EnvironmentVariable{
				{Name: "PORT", Value: "8080"},
				{
					Name:  "SECRET",
					Value: "${{connections.my-conn.credentials.api-key}}",
				},
				{Name: "ENV_REF", Value: "${SOME_VAR}"},
			},
			want: []connRef{
				{EnvName: "SECRET", ConnName: "my-conn", CredKey: "api-key"},
			},
		},
		{
			name:    "empty env vars",
			envVars: []agent_yaml.EnvironmentVariable{},
			want:    nil,
		},
		{
			name:    "nil env vars",
			envVars: nil,
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractConnectionRefs(tt.envVars)
			require.Equal(t, tt.want, result)
		})
	}
}

func TestLookupCredentialValue(t *testing.T) {
	tests := []struct {
		name      string
		conn      *connections.Connection
		credKey   string
		wantValue string
		wantFound bool
	}{
		{
			name: "api key lookup",
			conn: &connections.Connection{
				Credentials: &connections.ConnectionCredentials{
					Key: "my-api-key-value",
				},
			},
			credKey:   "key",
			wantValue: "my-api-key-value",
			wantFound: true,
		},
		{
			name: "custom key lookup",
			conn: &connections.Connection{
				Credentials: &connections.ConnectionCredentials{
					CustomKeys: map[string]string{
						"x-api-key": "tavily-secret",
						"token":     "bearer-token",
					},
				},
			},
			credKey:   "x-api-key",
			wantValue: "tavily-secret",
			wantFound: true,
		},
		{
			name: "key not found in custom keys",
			conn: &connections.Connection{
				Credentials: &connections.ConnectionCredentials{
					CustomKeys: map[string]string{
						"other": "value",
					},
				},
			},
			credKey:   "missing-key",
			wantValue: "",
			wantFound: false,
		},
		{
			name: "nil credentials",
			conn: &connections.Connection{
				Credentials: nil,
			},
			credKey:   "key",
			wantValue: "",
			wantFound: false,
		},
		{
			name:      "nil connection",
			conn:      nil,
			credKey:   "key",
			wantValue: "",
			wantFound: false,
		},
		{
			name: "empty key field — falls through to custom keys",
			conn: &connections.Connection{
				Credentials: &connections.ConnectionCredentials{
					Key:        "",
					CustomKeys: map[string]string{"key": "from-custom"},
				},
			},
			credKey:   "key",
			wantValue: "from-custom",
			wantFound: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value, found := lookupCredentialValue(tt.conn, tt.credKey)
			require.Equal(t, tt.wantFound, found)
			require.Equal(t, tt.wantValue, value)
		})
	}
}

func TestFindManifestInDir(t *testing.T) {
	t.Run("finds agent.yaml with connection refs", func(t *testing.T) {
		dir := t.TempDir()
		content := `environment_variables:
  - name: MY_KEY
    value: "${{connections.test.credentials.api-key}}"
`
		err := os.WriteFile(
			filepath.Join(dir, "agent.yaml"), []byte(content), 0600,
		)
		require.NoError(t, err)

		result := findManifestInDir(dir)
		require.Equal(t, filepath.Join(dir, "agent.yaml"), result)
	})

	t.Run("finds agent.manifest.yaml with connection refs", func(t *testing.T) {
		dir := t.TempDir()
		content := `template:
  environment_variables:
    - name: SECRET
      value: "${{connections.conn1.credentials.key}}"
`
		err := os.WriteFile(
			filepath.Join(dir, "agent.manifest.yaml"),
			[]byte(content), 0600,
		)
		require.NoError(t, err)

		result := findManifestInDir(dir)
		require.Equal(t,
			filepath.Join(dir, "agent.manifest.yaml"), result)
	})

	t.Run("prefers agent.yaml over agent.manifest.yaml", func(t *testing.T) {
		dir := t.TempDir()
		agentYAML := `environment_variables:
  - name: A
    value: "${{connections.c.credentials.k}}"
`
		manifestYAML := `template:
  environment_variables:
    - name: B
      value: "${{connections.c.credentials.k}}"
`
		require.NoError(t, os.WriteFile(
			filepath.Join(dir, "agent.yaml"),
			[]byte(agentYAML), 0600,
		))
		require.NoError(t, os.WriteFile(
			filepath.Join(dir, "agent.manifest.yaml"),
			[]byte(manifestYAML), 0600,
		))

		result := findManifestInDir(dir)
		require.Equal(t, filepath.Join(dir, "agent.yaml"), result)
	})

	t.Run("skips yaml without connection refs", func(t *testing.T) {
		dir := t.TempDir()
		content := `environment_variables:
  - name: PORT
    value: "8080"
`
		require.NoError(t, os.WriteFile(
			filepath.Join(dir, "agent.yaml"),
			[]byte(content), 0600,
		))

		result := findManifestInDir(dir)
		require.Empty(t, result)
	})

	t.Run("returns empty for empty directory", func(t *testing.T) {
		dir := t.TempDir()
		result := findManifestInDir(dir)
		require.Empty(t, result)
	})
}

func TestConnectionRefPattern(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantConn string
		wantKey  string
		wantNil  bool
	}{
		{
			name:     "standard ref",
			input:    "${{connections.my-conn.credentials.x-api-key}}",
			wantConn: "my-conn",
			wantKey:  "x-api-key",
		},
		{
			name:     "simple key name",
			input:    "${{connections.conn1.credentials.key}}",
			wantConn: "conn1",
			wantKey:  "key",
		},
		{
			name:    "not a connection ref",
			input:   "${SOME_ENV_VAR}",
			wantNil: true,
		},
		{
			name:    "partial pattern",
			input:   "${{connections.only-name}}",
			wantNil: true,
		},
		{
			name:    "empty string",
			input:   "",
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matches := connectionRefPattern.FindStringSubmatch(tt.input)
			if tt.wantNil {
				require.Nil(t, matches)
				return
			}
			require.Len(t, matches, 3)
			require.Equal(t, tt.wantConn, matches[1])
			require.Equal(t, tt.wantKey, matches[2])
		})
	}
}
