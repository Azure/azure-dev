// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"azure.ai.toolboxes/internal/definition"
	"azure.ai.toolboxes/internal/exterrors"
	"azure.ai.toolboxes/internal/foundry/projectctx"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildToolboxVersionRequestAllowsSkillsOnly(t *testing.T) {
	request, err := buildToolboxVersionRequest(
		t.Context(), newStubConnectionResolver(), "https://example.test",
		&definition.Definition{Skills: []definition.SkillReference{{Name: "triage"}}},
	)
	require.NoError(t, err)
	assert.Empty(t, request.Tools)
	assert.Len(t, request.Skills, 1)
}

func TestCreateLocalValidationBeforeEndpointAndAuthentication(t *testing.T) {
	previousRead := projectctx.ReadAzdHostedSourcesFunc
	projectctx.ReadAzdHostedSourcesFunc = func(context.Context) (projectctx.AzdHostedSources, error) {
		t.Fatal("local validation must precede endpoint lookup and authentication")
		return projectctx.AzdHostedSources{}, nil
	}
	t.Cleanup(func() { projectctx.ReadAzdHostedSourcesFunc = previousRead })
	writes := stubToolboxEndpointEnv(t)
	for _, tt := range []struct {
		name string
		body string
		code string
	}{
		{"missing tool type", "tools:\n  - name: web\n", exterrors.CodeMissingToolType},
		{"invalid skill", "skills:\n  - name: INVALID\n", exterrors.CodeInvalidSkillName},
		{"empty connection", "connections:\n  - name: ' '\n", exterrors.CodeInvalidParameter},
		{"invalid connection name", "connections:\n  - name: invalid.name\n", exterrors.CodeInvalidToolboxName},
		{"duplicate raw names", "tools:\n  - {type: web_search, name: dup}\n  - {type: mcp, name: dup}\n", exterrors.CodeDuplicateToolName},
		{"mixed raw and connection names", "connections:\n  - name: dup\ntools:\n  - {type: web_search, name: dup}\n", exterrors.CodeDuplicateToolName},
		{"duplicate connections", "connections:\n  - name: dup\n  - name: dup\n", exterrors.CodeDuplicateConnection},
		{"conflicting connection options", "connections:\n  - {name: search, index: idx, instanceName: inst}\n", exterrors.CodeConflictingArguments},
		{"invalid policy", "tools:\n  - {type: web_search}\npolicies:\n  raiConfig: {}\n", exterrors.CodeInvalidParameter},
		{"unknown field", "tools:\n  - {type: web_search}\nunsupported: true\n", exterrors.CodeInvalidParameter},
		{"name mismatch", "name: different\ntools:\n  - {type: web_search}\n", exterrors.CodeInvalidToolboxName},
		{"empty definition", "{}\n", exterrors.CodeInvalidToolboxName},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "toolbox.yaml")
			require.NoError(t, os.WriteFile(path, []byte(tt.body), 0o600))
			err := runToolboxCreate(t.Context(), "research", toolboxCreateFlags{fromFile: path}, toolboxFlags{}, newStubConnectionResolver())
			requireLocalError(t, err, tt.code)
			// #nosec G304 -- path is the definition created in this test's temporary directory.
			content, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, tt.body, string(content))
		})
	}
	assert.Empty(t, *writes)
}

func TestCreateRejectsExplicitEmptyFlagsBeforeAuthentication(t *testing.T) {
	for _, args := range [][]string{
		{"create", "research", "--from-file="},
		{"create", "research", "--from-file=   "},
		{"create", "research", "--from-file=missing.yaml", "--project-endpoint="},
		{"create", "research", "--from-file=missing.yaml", "--project-endpoint=  "},
	} {
		t.Run(args[len(args)-1], func(t *testing.T) {
			root := NewRootCommand()
			var stderr bytes.Buffer
			root.SetErr(&stderr)
			root.SetOut(io.Discard)
			root.SetArgs(args)
			err := root.ExecuteContext(t.Context())
			requireLocalError(t, err, exterrors.CodeInvalidParameter)
		})
	}
}

func TestCreateRejectsDefinitionNameMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), definition.DefaultPath)
	require.NoError(t, definition.Save(path, &definition.Definition{
		Name:  "from-file",
		Tools: []map[string]any{{"type": "web_search"}},
	}))

	err := runToolboxCreateWith(
		t.Context(), newMockToolboxClient("https://example.test"),
		newStubConnectionResolver(), "https://example.test", "from-command",
		toolboxCreateFlags{fromFile: path}, toolboxFlags{output: "json"},
	)
	requireLocalError(t, err, exterrors.CodeInvalidToolboxName)
}
