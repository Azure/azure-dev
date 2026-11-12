// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaiskills/internal/exterrors"

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
	assert.Equal(t, skillInstructions{Value: "Classify each issue."}, captured.Config.Instructions)
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
	assert.Equal(t, skillInstructions{Value: "Review code for correctness.\n"}, declaration.Config.Instructions)
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

func TestAddAction_RejectsIncompleteContentBeforeWriting(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		description  string
		instructions string
		wantErr      string
	}{
		{"missing description", "", "Review code.", "description"},
		{"blank description", " \t\n", "Review code.", "description"},
		{"missing instructions", "Review code", "", "instructions"},
		{"blank instructions", "Review code", " \t\n", "instructions"},
	}
	for _, tt := range tests {
		for _, source := range []string{"inline", "SKILL.md"} {
			t.Run(tt.name+"/"+source, func(t *testing.T) {
				t.Parallel()

				flags := &addFlags{name: "code-review"}
				if source == "inline" {
					flags.description = tt.description
					flags.instructions = tt.instructions
					flags.descriptionSet = true
					flags.instructionsSet = true
				} else {
					flags.file = filepath.Join(t.TempDir(), "SKILL.md")
					content := fmt.Sprintf(
						"---\nname: code-review\ndescription: %q\n---\n%s", tt.description, tt.instructions)
					require.NoError(t, os.WriteFile(flags.file, []byte(content), 0600))
				}
				var output bytes.Buffer
				called := false
				action := &addAction{
					flags: flags,
					upsert: func(
						context.Context,
						skillServiceDeclaration,
					) (*skillServiceUpsertResult, error) {
						called = true
						return &skillServiceUpsertResult{Name: flags.name, Host: aiSkillHost}, nil
					},
					writer: &output,
				}

				err := action.Run(t.Context())
				require.ErrorContains(t, err, tt.wantErr)
				localErr, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok)
				assert.Equal(t, exterrors.CodeMissingRequiredField, localErr.Code)
				assert.NotEmpty(t, localErr.Suggestion)
				assert.False(t, called)
				assert.Empty(t, output.String())
			})
		}
	}
}

func TestAddAction_InlineInstructionsRoundTrip(t *testing.T) {
	t.Parallel()

	for _, instructions := range []string{
		"README.md",
		"README.md\n",
		"Follow README.md",
		"docs/review instructions.md",
		"./skill files/rules.md",
		"Follow docs/README.md",
	} {
		for _, source := range []string{"inline", "SKILL.md"} {
			t.Run(instructions+"/"+source, func(t *testing.T) {
				t.Parallel()

				projectDir := t.TempDir()
				path := filepath.Join(projectDir, strings.TrimSpace(instructions))
				require.NoError(t, os.MkdirAll(filepath.Dir(path), 0750))
				require.NoError(t, os.WriteFile(path, []byte("This file must not be read."), 0600))

				flags := &addFlags{name: "code-review"}
				if source == "inline" {
					flags.description = "Review code"
					flags.instructions = instructions
					flags.descriptionSet = true
					flags.instructionsSet = true
				} else {
					flags.file = filepath.Join(projectDir, "SKILL.md")
					content := "---\nname: code-review\ndescription: Review code\n---\n" + instructions
					require.NoError(t, os.WriteFile(flags.file, []byte(content), 0600))
				}
				client := &recordingSkillProjectClient{project: &azdext.ProjectConfig{Path: projectDir}}
				action := &addAction{
					flags: flags,
					upsert: func(
						ctx context.Context,
						declaration skillServiceDeclaration,
					) (*skillServiceUpsertResult, error) {
						return upsertSkillService(ctx, client, declaration)
					},
				}
				require.NoError(t, action.Run(t.Context()))
				require.NotNil(t, client.addRequest)

				service := client.addRequest.GetService()
				cfg, err := parseSkillServiceConfig(service)
				require.NoError(t, err)
				resolved, err := resolveSkillInstructions(projectDir, service, cfg.Instructions)
				require.NoError(t, err)
				assert.Equal(t, instructions, resolved)
			})
		}
	}
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
	assert.Contains(t, cmd.Long, "require both a non-empty description and instructions")
}

func TestNewAddCommand_RejectsMissingInlineFlags(t *testing.T) {
	t.Parallel()

	for _, flag := range []string{"description", "instructions"} {
		t.Run(flag, func(t *testing.T) {
			t.Parallel()

			var output bytes.Buffer
			cmd := newAddCommand(&azdext.ExtensionContext{})
			cmd.SetArgs([]string{"code-review", "--" + flag, "Review code."})
			cmd.SetOut(&output)
			cmd.SetErr(&output)
			cmd.SilenceErrors = true
			cmd.SilenceUsage = true
			err := cmd.ExecuteContext(t.Context())
			require.Error(t, err)
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			assert.Equal(t, exterrors.CodeMissingRequiredField, localErr.Code)
			assert.Empty(t, output.String())
		})
	}
}

func TestRootCommand_RegistersAdd(t *testing.T) {
	command, _, err := NewRootCommand().Find([]string{"add"})
	require.NoError(t, err)
	assert.Equal(t, "add", command.Name())
}
