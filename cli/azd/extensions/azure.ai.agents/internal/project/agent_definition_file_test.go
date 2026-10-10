// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func TestLoadAgentDefinitionFileSupportedKinds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		path        string
		content     string
		wantKind    agent_yaml.AgentKind
		wantName    string
		serviceName string
	}{
		{
			name: "prompt YAML",
			path: "definitions/prompt.yaml",
			content: `kind: prompt
name: prompt-agent
model: gpt-4.1-mini
instructions: Help the user.
`,
			wantKind: agent_yaml.AgentKindPrompt,
			wantName: "prompt-agent",
		},
		{
			name: "hosted YML",
			path: "definitions/hosted.yml",
			content: `kind: hosted
name: hosted-agent
`,
			wantKind: agent_yaml.AgentKindHosted,
			wantName: "hosted-agent",
		},
		{
			name: "hosted code JSON",
			path: "definitions/code.json",
			content: `{
  "kind": "hosted",
  "name": "code-agent",
  "codeConfiguration": {
    "runtime": "python_3_13",
    "entryPoint": "app.py"
  }
}`,
			wantKind: agent_yaml.AgentKindHosted,
			wantName: "code-agent",
		},
		{
			name: "voice YAML",
			path: "definitions/voice.yaml",
			content: `kind: voice
name: voice-agent
model:
  id: gpt-realtime
`,
			wantKind: agent_yaml.AgentKindVoice,
			wantName: "voice-agent",
		},
		{
			name: "prompt voice JSON",
			path: "definitions/prompt-voice.json",
			content: `{
  "kind": "prompt-voice",
  "name": "prompt-voice-agent",
  "model": {"id": "gpt-realtime"}
}`,
			wantKind: agent_yaml.AgentKindPromptVoice,
			wantName: "prompt-voice-agent",
		},
		{
			name: "prompt service-name fallback",
			path: "definitions/prompt-fallback.yaml",
			content: `kind: prompt
model: gpt-4.1-mini
instructions: Help the user.
`,
			serviceName: "planned-service",
			wantKind:    agent_yaml.AgentKindPrompt,
			wantName:    "planned-service",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			path := filepath.Join(root, filepath.FromSlash(test.path))
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
			require.NoError(t, os.WriteFile(path, []byte(test.content), 0o600))

			got, err := LoadAgentDefinitionFile(root, test.path, test.serviceName)

			require.NoError(t, err)
			require.Equal(t, test.path, got.Path)
			require.Equal(t, test.wantKind, got.Kind)
			require.Equal(t, test.wantName, got.Name)
			require.Equal(t, string(test.wantKind), got.Properties["kind"])
		})
	}
}

func TestLoadAgentDefinitionFileRequiresOneNonEmptyObject(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
	}{
		{name: "empty"},
		{name: "whitespace", content: " \n\t"},
		{name: "comment only", content: "# no definition\n"},
		{name: "empty YAML document", content: "---\n"},
		{name: "empty mapping", content: "{}\n"},
		{name: "null", content: "null\n"},
		{name: "scalar", content: "agent\n"},
		{name: "sequence", content: "- kind: hosted\n"},
		{
			name: "second mapping document",
			content: `kind: hosted
name: first
---
kind: hosted
name: second
`,
		},
		{
			name:    "second empty document",
			content: "kind: hosted\nname: first\n---\n",
		},
		{
			name:    "invalid trailing content",
			content: "kind: hosted\nname: first\n[not valid",
		},
		{
			name:    "trailing JSON object",
			content: `{"kind":"hosted","name":"first"} {"kind":"hosted","name":"second"}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			path := filepath.Join(root, "agent.yaml")
			require.NoError(t, os.WriteFile(path, []byte(test.content), 0o600))

			_, err := LoadAgentDefinitionFile(root, "agent.yaml", "agent-service")

			require.Error(t, err)
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			require.Equal(t, exterrors.CodeInvalidAgentManifest, localErr.Code)
		})
	}
}

func TestLoadAgentDefinitionFileRejectsLegacyAndCoreShapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
	}{
		{
			name: "azure yaml project",
			content: `name: sample
services:
  agent:
    host: azure.ai.agent
`,
		},
		{
			name: "AgentManifest template wrapper",
			content: `name: sample
template:
  kind: hosted
`,
		},
		{
			name: "AgentManifest kind",
			content: `kind: AgentManifest
name: sample
`,
		},
		{
			name: "nested config",
			content: `kind: hosted
name: sample
config: {}
`,
		},
	}
	for _, key := range coreServiceKeys {
		tests = append(tests, struct {
			name    string
			content string
		}{
			name:    "core field " + key,
			content: "kind: hosted\nname: sample\n" + key + ": null\n",
		})
	}
	for _, key := range []string{"pipeline", "requiredVersions"} {
		tests = append(tests, struct {
			name    string
			content string
		}{
			name:    "project field " + key,
			content: "kind: hosted\nname: sample\n" + key + ": {}\n",
		})
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			require.NoError(t, os.WriteFile(
				filepath.Join(root, "agent.yaml"),
				[]byte(test.content),
				0o600,
			))

			_, err := LoadAgentDefinitionFile(root, "agent.yaml", "agent-service")

			require.Error(t, err)
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			require.Equal(t, exterrors.CodeInvalidAgentManifest, localErr.Code)
		})
	}
}

func TestLoadAgentDefinitionFileValidatesSchemaAndRuntime(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
	}{
		{
			name: "unsupported kind",
			content: `kind: workflow
name: flow
`,
		},
		{
			name: "prompt missing model",
			content: `kind: prompt
name: incomplete
instructions: Help.
`,
		},
		{
			name: "prompt missing instructions",
			content: `kind: prompt
name: incomplete
model: gpt-4.1-mini
`,
		},
		{
			name: "hosted code entry point type",
			content: `kind: hosted
name: invalid-code
codeConfiguration:
  runtime: python_3_13
  entryPoint: [not, a, string]
`,
		},
		{
			name: "voice model object",
			content: `kind: voice
name: invalid-voice
model: gpt-realtime
`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			require.NoError(t, os.WriteFile(
				filepath.Join(root, "agent.yaml"),
				[]byte(test.content),
				0o600,
			))

			_, err := LoadAgentDefinitionFile(root, "agent.yaml", "agent-service")

			require.Error(t, err)
		})
	}
}

func TestLoadAgentDefinitionFileAllowsOpenToolPayload(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	content := `kind: prompt-voice
name: open-tool-agent
model:
  id: gpt-realtime
futureAgentProperty:
  nested: payload-value
tools:
  - type: future_tool
    futureField:
      project: payload-value
      host: payload-value
`
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "agent.yaml"),
		[]byte(content),
		0o600,
	))

	got, err := LoadAgentDefinitionFile(root, "agent.yaml", "")

	require.NoError(t, err)
	require.Equal(t, agent_yaml.AgentKindPromptVoice, got.Kind)
	tools, ok := got.Properties["tools"].([]any)
	require.True(t, ok)
	tool, ok := tools[0].(map[string]any)
	require.True(t, ok)
	require.Contains(t, tool, "futureField")
}

func TestLoadAgentDefinitionFileResolvesNestedReferencesWithSDK(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "harness.json"),
		[]byte(`{"type":"github_copilot_preview"}`),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "agent.yaml"),
		[]byte(`kind: prompt
name: referenced-agent
model: gpt-4.1-mini
instructions: Help the user.
harness:
  $ref: ./harness.json
`),
		0o600,
	))

	got, err := LoadAgentDefinitionFile(root, "agent.yaml", "agent-service")

	require.NoError(t, err)
	require.Equal(t, agent_yaml.AgentKindPrompt, got.Kind)
	require.Equal(t, "referenced-agent", got.Name)
	harness, ok := got.Properties["harness"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "./harness.json", harness["$ref"])
}

func TestLoadAgentDefinitionFilePathValidation(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	root := filepath.Join(parent, "project")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "definitions"), 0o750))
	content := []byte("kind: hosted\nname: path-agent\n")
	relativeFile := filepath.Join(root, "definitions", "agent.yaml")
	require.NoError(t, os.WriteFile(relativeFile, content, 0o600))

	t.Run("relative path", func(t *testing.T) {
		got, err := LoadAgentDefinitionFile(root, "definitions/agent.yaml", "")
		require.NoError(t, err)
		require.Equal(t, "definitions/agent.yaml", got.Path)
	})
	t.Run("relative project root", func(t *testing.T) {
		cwd, err := os.Getwd()
		require.NoError(t, err)
		relativeRoot, err := filepath.Rel(cwd, root)
		require.NoError(t, err)
		got, err := LoadAgentDefinitionFile(relativeRoot, "definitions/agent.yaml", "")
		require.NoError(t, err)
		require.Equal(t, "definitions/agent.yaml", got.Path)
	})
	t.Run("absolute path inside project", func(t *testing.T) {
		got, err := LoadAgentDefinitionFile(root, relativeFile, "")
		require.NoError(t, err)
		require.Equal(t, "definitions/agent.yaml", got.Path)
	})
	t.Run("parent traversal", func(t *testing.T) {
		_, err := LoadAgentDefinitionFile(root, "../outside.yaml", "")
		requireInvalidAgentFilePath(t, err)
	})
	t.Run("absolute path outside project", func(t *testing.T) {
		outside := filepath.Join(parent, "outside.yaml")
		require.NoError(t, os.WriteFile(outside, content, 0o600))
		_, err := LoadAgentDefinitionFile(root, outside, "")
		requireInvalidAgentFilePath(t, err)
	})
	t.Run("missing file", func(t *testing.T) {
		_, err := LoadAgentDefinitionFile(root, "missing.yaml", "")
		localErr, ok := errors.AsType[*azdext.LocalError](err)
		require.True(t, ok)
		require.Equal(t, exterrors.CodeAgentDefinitionNotFound, localErr.Code)
	})
	t.Run("directory", func(t *testing.T) {
		dir := filepath.Join(root, "not-a-file.yaml")
		require.NoError(t, os.Mkdir(dir, 0o750))
		_, err := LoadAgentDefinitionFile(root, "not-a-file.yaml", "")
		requireInvalidAgentFilePath(t, err)
	})
	t.Run("unsupported extension", func(t *testing.T) {
		path := filepath.Join(root, "agent.txt")
		require.NoError(t, os.WriteFile(path, content, 0o600))
		_, err := LoadAgentDefinitionFile(root, "agent.txt", "")
		requireInvalidAgentFilePath(t, err)
	})
	t.Run("uppercase extension", func(t *testing.T) {
		path := filepath.Join(root, "agent.JSON")
		require.NoError(t, os.WriteFile(path, []byte(
			`{"kind":"hosted","name":"json-agent"}`,
		), 0o600))
		got, err := LoadAgentDefinitionFile(root, "agent.JSON", "")
		require.NoError(t, err)
		require.Equal(t, "agent.JSON", got.Path)
	})
	t.Run("symlink escaping project", func(t *testing.T) {
		outside := filepath.Join(parent, "outside-link-target.yaml")
		require.NoError(t, os.WriteFile(outside, content, 0o600))
		link := filepath.Join(root, "escape.yaml")
		createAgentDefinitionSymlinkOrSkip(t, outside, link)
		_, err := LoadAgentDefinitionFile(root, "escape.yaml", "")
		requireInvalidAgentFilePath(t, err)
	})
	t.Run("symlink within project", func(t *testing.T) {
		link := filepath.Join(root, "inside.yaml")
		createAgentDefinitionSymlinkOrSkip(t, relativeFile, link)
		got, err := LoadAgentDefinitionFile(root, "inside.yaml", "")
		require.NoError(t, err)
		require.Equal(t, "inside.yaml", got.Path)
	})
}

func requireInvalidAgentFilePath(t *testing.T, err error) {
	t.Helper()

	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok, "expected LocalError, got %T: %v", err, err)
	require.Equal(t, exterrors.CodeInvalidFilePath, localErr.Code)
}

func createAgentDefinitionSymlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()

	if err := os.Symlink(target, link); err != nil {
		if errors.Is(err, os.ErrPermission) ||
			strings.Contains(strings.ToLower(err.Error()), "privilege") {
			t.Skipf("symlink creation not permitted: %v", err)
		}
		t.Fatalf("create symlink: %v", err)
	}
}
