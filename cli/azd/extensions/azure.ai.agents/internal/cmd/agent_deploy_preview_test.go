// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func dryRunOnlyDependencies(
	t *testing.T, runner dependencyCommandRunner, previewer standaloneAgentPreviewer,
) agentDeployDependencies {
	t.Helper()
	return agentDeployDependencies{
		runner: runner,
		preparer: func(context.Context, project.DirectDeployOptions) (*project.PreparedStandaloneHostedAgent, error) {
			t.Fatal("dry-run must not prepare packages or run local builds")
			return nil, nil
		},
		deployer: func(
			context.Context, *project.PreparedStandaloneHostedAgent, map[string]string,
		) (*project.DirectDeployResult, error) {
			t.Fatal("dry-run must not deploy an agent")
			return nil, nil
		},
		previewer: previewer,
	}
}

func TestAgentDeployDryRunCommand(t *testing.T) {
	t.Parallel()
	for _, format := range []string{"json", "table"} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			directory := t.TempDir()
			path := filepath.Join(directory, "custom-agent.yaml")
			codePath := filepath.Join(directory, "source")
			require.NoError(t, os.WriteFile(path, []byte("name: research-agent\nkind: hosted\n"), 0o600))
			runner := &recordingDependencyRunner{err: errors.New("dependency deployment must not run")}
			var gotOptions project.DirectDeployOptions
			calls := 0
			dependencies := dryRunOnlyDependencies(t, runner, func(
				_ context.Context, options project.DirectDeployOptions, pending []string,
			) (*project.DirectDeployPreviewResult, error) {
				calls++
				gotOptions = options
				assert.Empty(t, pending)
				return &project.DirectDeployPreviewResult{
					Name: "research-agent", CurrentVersion: "7", Operation: "create_version",
					Changes: []project.DeployPreviewChangeGroup{}, SourcePath: options.CodePath,
				}, nil
			})
			command := newAgentDeployCommandWithDependencies(&azdext.ExtensionContext{OutputFormat: format}, dependencies)
			var stdout, stderr bytes.Buffer
			command.SetOut(&stdout)
			command.SetErr(&stderr)
			command.SetArgs([]string{
				path, "--dry-run", "--code", codePath,
				"--project-endpoint", "https://account.services.ai.azure.com/api/projects/project/",
			})
			require.NoError(t, command.Execute())
			assert.Equal(t, 1, calls)
			assert.Equal(t, path, gotOptions.DefinitionPath)
			assert.Equal(t, codePath, gotOptions.CodePath)
			assert.Equal(t, "https://account.services.ai.azure.com/api/projects/project", gotOptions.ProjectEndpoint)
			assert.Empty(t, runner.args)
			assert.Empty(t, stderr.String())
			if format == "json" {
				decoder := json.NewDecoder(strings.NewReader(stdout.String()))
				var value map[string]any
				require.NoError(t, decoder.Decode(&value))
				assert.Equal(t, "research-agent", value["name"])
				assert.Equal(t, false, value["hasChanges"])
				assert.Equal(t, []any{}, value["changes"])
				assert.Equal(t, codePath, value["sourcePath"])
				assert.ErrorIs(t, decoder.Decode(&value), io.EOF, "stdout must contain only one JSON document")
			} else {
				assert.Contains(t, stdout.String(), "No changes to agent configuration.")
				assert.Contains(t, stdout.String(), "Dry run complete. No changes were made.")
			}
		})
	}
}

func TestAgentDeployWithoutDryRunStillDeploys(t *testing.T) {
	t.Parallel()
	for _, flags := range [][]string{nil, {"--dry-run=false"}} {
		t.Run(strings.Join(flags, ""), func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "agent.yaml")
			require.NoError(t, os.WriteFile(path, []byte("name: research-agent\nkind: hosted\n"), 0o600))
			var steps []string
			prepared := &project.PreparedStandaloneHostedAgent{}
			dependencies := agentDeployDependencies{
				runner: &recordingDependencyRunner{},
				preparer: func(
					_ context.Context, options project.DirectDeployOptions,
				) (*project.PreparedStandaloneHostedAgent, error) {
					steps = append(steps, "prepare")
					options.Progress("Packaging agent source code")
					return prepared, nil
				},
				deployer: func(
					_ context.Context, value *project.PreparedStandaloneHostedAgent, _ map[string]string,
				) (*project.DirectDeployResult, error) {
					steps = append(steps, "deploy")
					assert.Same(t, prepared, value)
					return &project.DirectDeployResult{Name: "research-agent", Version: "8", State: "active"}, nil
				},
				previewer: func(
					context.Context, project.DirectDeployOptions, []string,
				) (*project.DirectDeployPreviewResult, error) {
					t.Fatal("normal deploy must not use the preview path")
					return nil, nil
				},
			}
			command := newAgentDeployCommandWithDependencies(&azdext.ExtensionContext{OutputFormat: "table"}, dependencies)
			var stdout, stderr bytes.Buffer
			command.SetOut(&stdout)
			command.SetErr(&stderr)
			args := []string{path, "--project-endpoint", "https://account.services.ai.azure.com/api/projects/project"}
			command.SetArgs(append(args, flags...))
			require.NoError(t, command.Execute())
			assert.Equal(t, []string{"prepare", "deploy"}, steps)
			assert.Equal(t, "Name     research-agent\nVersion  8\nState    active\n", stdout.String())
			assert.Equal(t, "Packaging agent source code\n", stderr.String())
		})
	}
}

func TestAgentDeployDryRunToolboxPlanning(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		reference   string
		toolbox     string
		environment map[string]string
		pending     []string
		wantError   bool
	}{
		{name: "no reference ignores sibling", toolbox: "name: unused\n"},
		{name: "external toolbox", reference: "toolbox:\n  name: support-tools\n",
			environment: map[string]string{"TOOLBOX_NAME": "support-tools"}},
		{name: "pinned version ignores invalid sibling",
			reference: "toolbox:\n  name: support-tools\n  version: '3'\n", toolbox: "invalid: [",
			environment: map[string]string{
				"TOOLBOX_NAME": "support-tools", "TOOLBOX_VERSION": "3",
				"TOOLBOX_ENDPOINT": "https://account.services.ai.azure.com/api/projects/project/" +
					"toolboxes/support-tools/versions/3/mcp?api-version=v1",
			}},
		{name: "sibling deployment pending", reference: "toolbox:\n  name: support-tools\n",
			toolbox: "name: support-tools\n", environment: map[string]string{"TOOLBOX_NAME": "support-tools"},
			pending: []string{"TOOLBOX_VERSION", "TOOLBOX_ENDPOINT"}},
		{name: "mismatched sibling", reference: "toolbox:\n  name: support-tools\n",
			toolbox: "name: another-toolbox\n", wantError: true},
		{name: "invalid sibling", reference: "toolbox:\n  name: support-tools\n",
			toolbox: "name: [", wantError: true},
		{name: "empty reference name", reference: "toolbox:\n  name: ' '\n", wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			directory := t.TempDir()
			path := filepath.Join(directory, "agent.yaml")
			require.NoError(t, os.WriteFile(path, []byte("name: research-agent\nkind: hosted\n"+tt.reference), 0o600))
			if tt.toolbox != "" {
				require.NoError(t, os.WriteFile(filepath.Join(directory, "toolbox.yaml"), []byte(tt.toolbox), 0o600))
			}
			runner := &recordingDependencyRunner{}
			calls := 0
			dependencies := dryRunOnlyDependencies(t, runner, func(
				_ context.Context, options project.DirectDeployOptions, pending []string,
			) (*project.DirectDeployPreviewResult, error) {
				calls++
				assert.Equal(t, tt.environment, options.Environment)
				assert.Equal(t, tt.pending, pending)
				return &project.DirectDeployPreviewResult{
					Name: "research-agent", Operation: "create", HasChanges: true,
					Changes: []project.DeployPreviewChangeGroup{},
				}, nil
			})
			var stdout bytes.Buffer
			err := runAgentDeploy(t.Context(), path, agentDeployFlags{
				projectEndpoint: "https://account.services.ai.azure.com/api/projects/project", dryRun: true,
			}, "json", dependencies, &stdout, io.Discard)
			assert.Empty(t, runner.args, "toolbox deploy must never run during dry-run")
			if tt.wantError {
				require.Error(t, err)
				assert.Equal(t, 0, calls)
				assert.Empty(t, stdout.String())
			} else {
				require.NoError(t, err)
				assert.Equal(t, 1, calls)
				if len(tt.pending) > 0 {
					assert.Contains(t, stdout.String(), "toolbox.yaml would be deployed first")
				}
			}
		})
	}
}

func TestAgentDeployDryRunPropagatesErrors(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "agent.yaml")
	require.NoError(t, os.WriteFile(path, []byte("name: research-agent\nkind: hosted\n"), 0o600))
	expected := errors.New("failed to read deployed agent")
	runner := &recordingDependencyRunner{}
	dependencies := dryRunOnlyDependencies(t, runner, func(
		context.Context, project.DirectDeployOptions, []string,
	) (*project.DirectDeployPreviewResult, error) {
		return nil, expected
	})
	command := newAgentDeployCommandWithDependencies(&azdext.ExtensionContext{OutputFormat: "json"}, dependencies)
	var stdout bytes.Buffer
	command.SetOut(&stdout)
	command.SetErr(io.Discard)
	command.SilenceUsage, command.SilenceErrors = true, true
	command.SetArgs([]string{
		path, "--dry-run", "--project-endpoint", "https://account.services.ai.azure.com/api/projects/project",
	})
	assert.ErrorIs(t, command.Execute(), expected)
	assert.Empty(t, stdout.String(), "failed previews must not emit a success-shaped result")
	assert.Empty(t, runner.args)
}

func TestAgentDeployDryRunHelp(t *testing.T) {
	t.Parallel()
	command := newAgentDeployCommand(&azdext.ExtensionContext{})
	var stdout bytes.Buffer
	command.SetOut(&stdout)
	command.SetErr(io.Discard)
	command.SetArgs([]string{"--help"})
	require.NoError(t, command.Execute())
	for _, text := range []string{
		"--dry-run", "--code", "--project-endpoint", "hosted source-code",
		"without building, packaging, uploading", "Source contents are not compared", "--output json",
	} {
		assert.Contains(t, stdout.String(), text)
	}
}

func TestWriteAgentDeployPreview(t *testing.T) {
	t.Parallel()
	result := &project.DirectDeployPreviewResult{
		Name: "research-agent", Operation: "create", HasChanges: true, SourcePath: "source",
		Changes: []project.DeployPreviewChangeGroup{
			{Group: "metadata", Changes: []project.DeployPreviewChange{
				{Field: "description", Kind: "add", After: "Research assistant"},
				{Field: "metadata.tags", Kind: "modify", Before: []any{"Streaming"}, After: []any{"Streaming", "Test"}},
			}},
			{Group: "protocols", Changes: []project.DeployPreviewChange{
				{Field: "protocols", Kind: "modify", Before: "responses 1.0.0", After: "responses 2.0.0"},
			}},
			{Group: "resources", Changes: []project.DeployPreviewChange{
				{Field: "cpu", Kind: "modify", Before: "0.5", After: "1"},
			}},
			{Group: "environmentVariables", Changes: []project.DeployPreviewChange{
				{Field: "SECRET", Kind: "remove", Before: "[redacted]", Sensitive: true},
				{Field: "TOOLBOX_VERSION", Kind: "pending", Sensitive: true},
			}},
			{Group: "modelDeployment", Changes: []project.DeployPreviewChange{
				{Field: "AZURE_AI_MODEL_DEPLOYMENT_NAME", Kind: "modify", Before: "old-model", After: "new-model"},
			}},
			{Group: "containerImage", Changes: []project.DeployPreviewChange{
				{Field: "image", Kind: "modify", Before: "registry.example.com/agent:v1", After: "registry.example.com/agent:v2"},
			}},
		},
		Notes: []string{"Source contents are not compared."},
	}
	var stdout bytes.Buffer
	require.NoError(t, writeAgentDeployPreview(&stdout, "table", result))
	for _, text := range []string{
		"Agent does not exist; would be created.", "Metadata", "Resources", "Environment variables (values redacted)",
		"Model deployment reference", `+ description: "Research assistant"`, `~ cpu: "0.5" -> "1"`,
		`~ metadata.tags: ["Streaming"] -> ["Streaming","Test"]`, "Protocols", "Container image",
		`- SECRET: "[redacted]"`, "? TOOLBOX_VERSION: (known after deployment)",
		"Source contents are not compared.", "Dry run complete. No changes were made.",
	} {
		assert.Contains(t, stdout.String(), text)
	}
	stdout.Reset()
	require.NoError(t, writeAgentDeployPreview(&stdout, "json", result))
	want, err := json.Marshal(result)
	require.NoError(t, err)
	assert.JSONEq(t, string(want), stdout.String())
}

type deployPreviewErrorWriter struct {
	err error
}

func (w deployPreviewErrorWriter) Write([]byte) (int, error) {
	return 0, w.err
}

func TestWriteAgentDeployPreviewPropagatesWriteError(t *testing.T) {
	t.Parallel()
	expected := errors.New("output is not writable")
	for _, format := range []string{"json", "table"} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			err := writeAgentDeployPreview(deployPreviewErrorWriter{err: expected}, format, &project.DirectDeployPreviewResult{
				Name: "agent", Operation: "create",
			})
			assert.ErrorIs(t, err, expected)
		})
	}
}

func TestAgentDeployDryRunDiscoversManifestWithoutDefinition(t *testing.T) {
	directory := t.TempDir()
	t.Chdir(directory)
	require.NoError(t, os.WriteFile("agent.manifest.yaml", []byte(`
name: agent
metadata:
  tags: [Test]
template:
  kind: hosted
`), 0o600))
	dependencies := dryRunOnlyDependencies(t, &recordingDependencyRunner{}, func(
		_ context.Context, options project.DirectDeployOptions, _ []string,
	) (*project.DirectDeployPreviewResult, error) {
		assert.Equal(t, "agent.manifest.yaml", options.DefinitionPath)
		require.NotNil(t, options.PreviewDefinition)
		require.NotNil(t, options.PreviewDefinition.Definition.Metadata)
		assert.Equal(t, []any{"Test"}, (*options.PreviewDefinition.Definition.Metadata)["tags"])
		return &project.DirectDeployPreviewResult{
			Name: "agent", Operation: "create", HasChanges: true,
			Changes: []project.DeployPreviewChangeGroup{{Group: "metadata", Changes: []project.DeployPreviewChange{
				{Field: "metadata.tags", Kind: "add", After: []any{"Test"}},
			}}},
		}, nil
	})
	dependencies.projectPreviewer = func(context.Context, agentDeployFlags) (*project.DirectDeployPreviewResult, error) {
		t.Fatal("a standalone manifest must not fall through to project loading")
		return nil, nil
	}
	command := newAgentDeployCommandWithDependencies(&azdext.ExtensionContext{OutputFormat: "json"}, dependencies)
	var stdout bytes.Buffer
	command.SetOut(&stdout)
	command.SetErr(io.Discard)
	command.SetArgs([]string{"--dry-run", "--project-endpoint", "https://account.services.ai.azure.com/api/projects/project"})
	require.NoError(t, command.Execute())
	assert.Contains(t, stdout.String(), "Test")
	assert.True(t, json.Valid(stdout.Bytes()))
	require.NoFileExists(t, filepath.Join(directory, "agent.yaml"))
}

func TestWriteAgentDeployPreviewSourceConflictsAndImagePlan(t *testing.T) {
	t.Parallel()
	result := &project.DirectDeployPreviewResult{
		Name: "agent", Operation: "create_version", CurrentVersion: "1", HasChanges: true,
		Sources: []string{"agent.manifest.yaml", "agent.yaml"},
		Image:   &project.DeployPreviewImage{Mode: "build", Build: true, Push: true},
		SourceConflicts: []project.DeployPreviewSourceConflict{{
			Source: "agent.manifest.yaml",
			Differences: []project.DeployPreviewChangeGroup{{Group: "resources", Changes: []project.DeployPreviewChange{
				{Field: "cpu", Kind: "modify", Before: "0.5", After: "1"},
			}}},
		}},
	}
	var stdout bytes.Buffer
	require.NoError(t, writeAgentDeployPreview(&stdout, "table", result))
	for _, expected := range []string{
		"agent.manifest.yaml", "agent.yaml", "Build: true", "Push: true", "known after build/provisioning",
		"Source precedence", "lower-priority values", "are overridden",
	} {
		assert.Contains(t, stdout.String(), expected)
	}
	stdout.Reset()
	require.NoError(t, writeAgentDeployPreview(&stdout, "json", result))
	var actual project.DirectDeployPreviewResult
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &actual))
	require.NotNil(t, actual.Image)
	assert.True(t, actual.Image.Build)
	require.Len(t, actual.SourceConflicts, 1)
	assert.Equal(t, "agent.manifest.yaml", actual.SourceConflicts[0].Source)
}
