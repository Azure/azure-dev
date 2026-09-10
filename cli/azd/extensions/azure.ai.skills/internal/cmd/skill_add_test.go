// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAddAction_RunWritesServiceOnly(t *testing.T) {
	t.Parallel()

	var captured skillServiceDeclaration
	var output bytes.Buffer
	action := &addAction{
		flags: &addFlags{
			name:            "triage-rules",
			description:     "Triage issues",
			instructions:    "Classify each issue.",
			descriptionSet:  true,
			instructionsSet: true,
			output:          outputTable,
		},
		upsert: func(
			_ context.Context,
			declaration skillServiceDeclaration,
		) (*skillServiceUpsertResult, error) {
			captured = declaration
			return &skillServiceUpsertResult{
				Name:    declaration.Name,
				Host:    aiSkillHost,
				Created: true,
			}, nil
		},
		writer:      &output,
		errorWriter: &bytes.Buffer{},
	}

	require.NoError(t, action.Run(t.Context()))
	assert.Equal(t, "triage-rules", captured.Name)
	assert.Equal(t, "Triage issues", captured.Config.Description)
	assert.Equal(t, "Classify each issue.", captured.Config.Instructions)
	assert.Empty(t, captured.ArchiveSource)
	assert.Equal(t, "Skill service \"triage-rules\" added in azure.yaml.\n", output.String())
}

func TestAddAction_BuildsCompleteSkillMdDeclaration(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "SKILL.md")
	require.NoError(t, os.WriteFile(path, []byte(`---
name: source-name
description: Review code
license: MIT
compatibility: gpt-5
metadata:
  owner: platform
allowed_tools:
  - code_interpreter
---
Review code for correctness.
`), 0600))

	var warnings bytes.Buffer
	action := &addAction{
		flags: &addFlags{
			name:     "code-review",
			file:     path,
			output:   outputTable,
			noPrompt: false,
		},
		errorWriter: &warnings,
	}
	declaration, err := action.buildDeclaration()
	require.NoError(t, err)

	assert.Equal(t, "code-review", declaration.Name)
	assert.Equal(t, "Review code", declaration.Config.Description)
	assert.Equal(t, "Review code for correctness.\n", declaration.Config.Instructions)
	assert.Equal(t, "MIT", declaration.Config.License)
	assert.Equal(t, "gpt-5", declaration.Config.Compatibility)
	assert.Equal(t, map[string]string{"owner": "platform"}, declaration.Config.Metadata)
	assert.Equal(t, []string{"code_interpreter"}, declaration.Config.Tools)
	assert.Contains(t, warnings.String(), "does not match positional argument")
}

func TestAddAction_BuildsArchiveDeclaration(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"skill.zip", "skill-dir"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			fullPath := filepath.Join(t.TempDir(), path)
			if filepath.Ext(fullPath) == ".zip" {
				require.NoError(t, os.WriteFile(fullPath, []byte("zip"), 0600))
			} else {
				require.NoError(t, os.MkdirAll(fullPath, 0750))
			}
			action := &addAction{
				flags:       &addFlags{name: "triage-rules", file: fullPath},
				errorWriter: &bytes.Buffer{},
			}

			declaration, err := action.buildDeclaration()
			require.NoError(t, err)
			assert.Equal(t, fullPath, declaration.ArchiveSource)
			assert.Empty(t, declaration.Config)
		})
	}
}

func TestAddAction_RequiresContent(t *testing.T) {
	t.Parallel()

	action := &addAction{
		flags:       &addFlags{name: "triage-rules"},
		errorWriter: &bytes.Buffer{},
	}
	_, err := action.buildDeclaration()
	require.ErrorContains(t, err, "no content supplied")
}

func TestWriteSkillServiceUpsertResult_JSON(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	require.NoError(t, writeSkillServiceUpsertResult(&output, &skillServiceUpsertResult{
		Name:        "triage-rules",
		Host:        aiSkillHost,
		ProjectPath: "C:/src/app",
		Created:     true,
	}, outputJSON))
	assert.JSONEq(t, `{
		"name": "triage-rules",
		"host": "azure.ai.skill",
		"projectPath": "C:/src/app",
		"created": true
	}`, output.String())
}

func TestNewAddCommand_RegistersFlags(t *testing.T) {
	t.Parallel()

	cmd := newAddCommand(&azdext.ExtensionContext{})
	assert.NotNil(t, cmd.Flags().Lookup("description"))
	assert.NotNil(t, cmd.Flags().Lookup("instructions"))
	assert.NotNil(t, cmd.Flags().Lookup("file"))
	assert.Contains(t, cmd.Long, "does not create or")
}

func TestRootCommand_RegistersAdd(t *testing.T) {
	command, _, err := NewRootCommand().Find([]string{"add"})
	require.NoError(t, err)
	assert.Equal(t, "add", command.Name())
}
