// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestInspectAzureYaml(t *testing.T) {
	tests := []struct {
		name             string
		content          string
		wantServices     bool
		wantAgentService bool
	}{
		{
			name: "unified azure.yaml with split foundry hosts",
			content: `name: foundry-simple
services:
  ai-project:
    host: azure.ai.project
  summarize:
    host: azure.ai.skill
    instructions: Summarize the user's input.
  assistant:
    host: azure.ai.agent
    uses:
      - summarize
    kind: hosted
`,
			wantServices:     true,
			wantAgentService: true,
		},
		{
			name: "prompt-only azure.yaml",
			content: `name: prompt-only
services:
  agent:
    host: azure.ai.agent
    kind: prompt
    model: gpt-5.6-luna
`,
			wantServices:     true,
			wantAgentService: true,
		},
		{
			name: "unsupported microsoft.foundry host",
			content: `name: foundry-legacy
services:
  agents:
    host: microsoft.foundry
`,
			wantServices: true,
		},
		{
			name: "unified azure.yaml with only sibling Foundry hosts",
			content: `name: foundry-resources
services:
  ai-project:
    host: azure.ai.project
  search-connection:
    host: azure.ai.connection
  toolbox:
    host: azure.ai.toolbox
  summarize:
    host: azure.ai.skill
  daily-report:
    host: azure.ai.routine
`,
			wantServices: true,
		},
		{
			name: "agent manifest with top-level template",
			content: `name: my-agent
template:
  kind: hosted
  name: my-agent
parameters: {}
resources: []
`,
			wantServices: false,
		},
		{
			name: "azure.yaml with only non-foundry services",
			content: `name: web-app
services:
  web:
    host: containerapp
    language: js
`,
			wantServices: true,
		},
		{
			name:    "empty content",
			content: "",
		},
		{
			name:    "malformed yaml",
			content: "name: [unterminated",
		},
		{
			name: "services present but not a map",
			content: `name: broken
services: just-a-string
`,
		},
		{
			name: "service without host",
			content: `name: foundry-noisy
services:
  ai-project:
    deployments: []
`,
			wantServices: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := inspectAzureYaml([]byte(tt.content), "")
			require.NoError(t, err)
			require.Equal(t, tt.wantServices, info.hasServices)
			require.Equal(t, tt.wantAgentService, info.hasAgentService)
		})
	}
}

func TestInspectAzureYamlPromptOnly(t *testing.T) {
	t.Parallel()

	promptOnly, err := inspectAzureYaml([]byte(`services:
  prompt:
    host: azure.ai.agent
    kind: prompt
`), "")
	require.NoError(t, err)
	require.True(t, promptOnly.promptOnly())

	managed, err := inspectAzureYaml([]byte(`services:
  managed:
    host: azure.ai.agent
    kind: prompt
    harness:
      kind: github_copilot_preview
`), "")
	require.NoError(t, err)
	require.True(t, managed.promptOnly())

	mixed, err := inspectAzureYaml([]byte(`services:
  prompt:
    host: azure.ai.agent
    kind: prompt
  hosted:
    host: azure.ai.agent
    kind: hosted
`), "")
	require.NoError(t, err)
	require.False(t, mixed.promptOnly())

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "agent.yaml"), []byte(`
host: azure.ai.agent
kind: prompt
`), 0o600))
	resolved, err := inspectAzureYaml([]byte(`services:
  prompt:
    $ref: ./agent.yaml
`), root)
	require.NoError(t, err)
	require.True(t, resolved.promptOnly())
}

func TestConfigureExistingPromptProjectNoPromptRequiresProjectID(t *testing.T) {
	t.Parallel()

	_, err := configureExistingPromptProject(
		t.Context(), nil, &azdext.AzureContext{}, "test", "", true,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires an existing Foundry project")
}

func TestMissingPromptAdoptionProjectError(t *testing.T) {
	t.Parallel()

	err := missingPromptAdoptionProjectError()
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires an existing Foundry project")
}

func TestNoExistingPromptDeploymentsError(t *testing.T) {
	t.Parallel()

	err := noExistingPromptDeploymentsError()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no existing model deployments")
}

func TestPrintPromptInitNextSteps(t *testing.T) {
	stdout := withCapturedStdout(t, func() {
		printPromptInitNextSteps("prompt-agent")
	})

	require.Contains(t, stdout, `cd "prompt-agent"`)
	require.Contains(t, stdout, "azd up")
	require.Contains(t, stdout, "azd deploy")
	require.Contains(t, stdout, `azd ai agent invoke "hello"`)
	require.NotContains(t, stdout, "azd ai agent run")
	require.NotContains(t, stdout, "--local")
}

func TestDeclaresAgentService_LocalServiceRef(t *testing.T) {
	root := t.TempDir()
	refPath := filepath.Join(root, "services", "agent.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(refPath), 0700))
	require.NoError(t, os.WriteFile(refPath, []byte("host: azure.ai.agent\nkind: hosted\n"), 0600))

	content := []byte(`name: foundry-ref
services:
  ai-project:
    host: azure.ai.project
  assistant:
    $ref: ./services/agent.yaml
`)

	info, err := inspectAzureYaml(content, root)
	require.NoError(t, err)
	require.True(t, info.hasServices)
	require.True(t, info.hasAgentService)
	require.False(t, info.hasUnresolvedRefs)
}

func TestInspectAzureYaml_LocalServiceRefValidation(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "services"), 0700))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "services", "project.yaml"),
		[]byte("host: azure.ai.project\n"),
		0600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "services", "agent.yaml"),
		[]byte("host: azure.ai.agent\n"),
		0600,
	))

	t.Run("non-Agent ref", func(t *testing.T) {
		info, err := inspectAzureYaml([]byte(`services:
  project:
    $ref: ./services/project.yaml
`), root)
		require.NoError(t, err)
		require.True(t, info.hasServices)
		require.False(t, info.hasAgentService)
	})

	t.Run("inline host overrides referenced host", func(t *testing.T) {
		info, err := inspectAzureYaml([]byte(`services:
  project:
    $ref: ./services/agent.yaml
    host: azure.ai.project
`), root)
		require.NoError(t, err)
		require.True(t, info.hasServices)
		require.False(t, info.hasAgentService)
	})

	t.Run("missing ref is returned", func(t *testing.T) {
		_, err := inspectAzureYaml([]byte(`services:
  agent:
    $ref: ./services/missing.yaml
`), root)
		require.ErrorContains(t, err, "cannot read")
	})

	t.Run("remote ref is returned", func(t *testing.T) {
		_, err := inspectAzureYaml([]byte(`services:
  agent:
    $ref: https://example.com/agent.yaml
`), root)
		require.ErrorContains(t, err, "remote includes are not supported")
	})
}

func TestInspectAzureYaml_RemoteServiceRefIsDeferred(t *testing.T) {
	info, err := inspectAzureYaml([]byte(`services:
  agent:
    $ref: ./services/agent.yaml
`), "")
	require.NoError(t, err)
	require.True(t, info.hasServices)
	require.False(t, info.hasAgentService)
	require.True(t, info.hasUnresolvedRefs)
}

func TestValidateStagedAzureYamlRequiresAgentService(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "azure.yaml"),
		[]byte("services:\n  project:\n    host: azure.ai.project\n"),
		0600,
	))

	err := validateStagedAzureYaml(root, filepath.Join(root, "azure.yaml"))
	require.Error(t, err)
	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	require.Equal(t, exterrors.CodeInvalidManifestPointer, localErr.Code)
	require.Contains(t, localErr.Message, "does not declare an agent service")
}

func TestValidateStagedAzureYamlReturnsRefErrors(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "azure.yaml"),
		[]byte(`services:
  agent:
    $ref: ./services/missing.yaml
`),
		0600,
	))

	err := validateStagedAzureYaml(root, filepath.Join(root, "azure.yaml"))
	require.ErrorContains(t, err, "cannot read")
	require.ErrorContains(t, err, "missing.yaml")
}

func TestFoundryProjectName(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{name: "name present", content: "name: foundry-simple\nservices: {}\n", want: "foundry-simple"},
		{name: "name with surrounding spaces", content: "name: \"  spaced  \"\n", want: "spaced"},
		{name: "no name", content: "services: {}\n", want: ""},
		{name: "malformed yaml", content: "name: [oops", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, foundryProjectName([]byte(tt.content)))
		})
	}
}

func TestParentDirOf(t *testing.T) {
	tests := []struct {
		filePath string
		want     string
	}{
		{filePath: "azure.yaml", want: ""},
		{filePath: "samples/simple/azure.yaml", want: "samples/simple"},
		{filePath: "a/b/c/azure.yaml", want: "a/b/c"},
	}
	for _, tt := range tests {
		t.Run(tt.filePath, func(t *testing.T) {
			require.Equal(t, tt.want, parentDirOf(tt.filePath))
		})
	}
}

func TestAdoptTargetDir(t *testing.T) {
	t.Run("explicit src wins", func(t *testing.T) {
		dir, display := adoptTargetDir(&initFlags{src: "my-dir"}, "foundry-simple")
		require.Equal(t, "my-dir", dir)
		require.Equal(t, "my-dir", display)
	})

	t.Run("derives folder from project name", func(t *testing.T) {
		dir, display := adoptTargetDir(&initFlags{}, "Foundry Simple")
		require.Equal(t, "foundry-simple", dir)
		require.Equal(t, "foundry-simple", display)
	})

	t.Run("falls back to current dir when name empty", func(t *testing.T) {
		dir, display := adoptTargetDir(&initFlags{}, "")
		require.Equal(t, ".", dir)
		require.Empty(t, display)
	})
}

func TestFolderDisplayIfNew(t *testing.T) {
	t.Run("current dir is never a created folder", func(t *testing.T) {
		require.Empty(t, folderDisplayIfNew("."))
	})

	t.Run("non-existent dir is displayed", func(t *testing.T) {
		require.Equal(t, "brand-new-dir-does-not-exist-xyz", folderDisplayIfNew("brand-new-dir-does-not-exist-xyz"))
	})

	t.Run("existing dir is not displayed", func(t *testing.T) {
		existing := t.TempDir()
		require.Empty(t, folderDisplayIfNew(existing))
	})
}

func TestStagedAzureYamlExists(t *testing.T) {
	t.Run("azure.yaml present", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "azure.yaml"), []byte("name: x\n"), 0600))
		require.True(t, stagedAzureYamlExists(dir))
	})

	t.Run("azure.yml present", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "azure.yml"), []byte("name: x\n"), 0600))
		require.True(t, stagedAzureYamlExists(dir))
	})

	t.Run("absent", func(t *testing.T) {
		require.False(t, stagedAzureYamlExists(t.TempDir()))
	})
}

func TestProjectManifestExists(t *testing.T) {
	t.Run("azure.yaml present", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "azure.yaml"), []byte("name: x\n"), 0600))
		require.True(t, projectManifestExists(dir))
	})

	t.Run("azure.yml present", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "azure.yml"), []byte("name: x\n"), 0600))
		require.True(t, projectManifestExists(dir))
	})

	t.Run("absent", func(t *testing.T) {
		require.False(t, projectManifestExists(t.TempDir()))
	})
}

func TestEnsureStagedAzureYaml_NormalizesAzureYml(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "azure.yml"), []byte("name: foundry-simple\n"), 0600))

	ok, err := ensureStagedAzureYaml(dir)
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, fileExists(filepath.Join(dir, "azure.yaml")))
	require.False(t, fileExists(filepath.Join(dir, "azure.yml")))
}

func TestClearStagingDirectory(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "partial.txt"), []byte("partial"), 0600))

	require.NoError(t, clearStagingDirectory(dir))
	require.True(t, fileExists(dir))
	require.False(t, fileExists(filepath.Join(dir, "partial.txt")))
}

// TestStageAzureYamlTemplate_LocalAzureYaml verifies a local pointer named
// azure.yaml uses its parent directory directly as the template (no temp copy).
func TestStageAzureYamlTemplate_LocalAzureYaml(t *testing.T) {
	sampleDir := t.TempDir()
	azureYaml := filepath.Join(sampleDir, "azure.yaml")
	require.NoError(t, os.WriteFile(azureYaml, []byte("name: foundry-simple\nservices: {}\n"), 0600))

	flags := &initFlags{manifestPointer: azureYaml}
	staging, cleanup, err := stageAzureYamlTemplate(t.Context(), flags, nil, nil)
	require.NoError(t, err)
	defer cleanup()

	require.Equal(t, sampleDir, staging)
	require.True(t, stagedAzureYamlExists(staging))
}

// TestStageAzureYamlTemplate_LocalAzureYmlRenamed verifies azure.yml is staged
// as azure.yaml so azd-core adopts the sample manifest instead of generating a
// default azure.yaml.
func TestStageAzureYamlTemplate_LocalAzureYmlRenamed(t *testing.T) {
	sampleDir := t.TempDir()
	azureYml := filepath.Join(sampleDir, "azure.yml")
	require.NoError(t, os.WriteFile(azureYml, []byte("name: foundry-simple\nservices: {}\n"), 0600))

	flags := &initFlags{manifestPointer: azureYml}
	staging, cleanup, err := stageAzureYamlTemplate(t.Context(), flags, nil, nil)
	require.NoError(t, err)
	defer cleanup()

	require.NotEqual(t, sampleDir, staging)
	require.True(t, fileExists(filepath.Join(staging, "azure.yaml")))
	require.False(t, fileExists(filepath.Join(staging, "azure.yml")))
}

// TestStageAzureYamlTemplate_LocalRenamesToAzureYaml verifies a local pointer
// not named azure.yaml is staged into a temp dir with the manifest written as
// azure.yaml, while sibling files are preserved.
func TestStageAzureYamlTemplate_LocalRenamesToAzureYaml(t *testing.T) {
	sampleDir := t.TempDir()
	pointer := filepath.Join(sampleDir, "sample.yaml")
	require.NoError(t, os.WriteFile(pointer, []byte("name: foundry-simple\nservices: {}\n"), 0600))
	require.NoError(t, os.MkdirAll(filepath.Join(sampleDir, "agents"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(sampleDir, "agents", "main.py"), []byte("print('x')\n"), 0600))

	flags := &initFlags{manifestPointer: pointer}
	staging, cleanup, err := stageAzureYamlTemplate(t.Context(), flags, nil, nil)
	require.NoError(t, err)
	defer cleanup()

	require.NotEqual(t, sampleDir, staging)
	require.True(t, stagedAzureYamlExists(staging))
	require.False(t, fileExists(filepath.Join(staging, "sample.yaml")))
	// Sibling files are carried into the staging directory.
	require.True(t, fileExists(filepath.Join(staging, "agents", "main.py")))
}

func TestStageAzureYamlTemplate_PreservesSkillService(t *testing.T) {
	sampleDir := t.TempDir()
	pointer := filepath.Join(sampleDir, "sample.yaml")
	content := `name: foundry-simple
services:
  summarize:
    host: azure.ai.skill
    instructions: Summarize the user's input.
  assistant:
    host: azure.ai.agent
    uses:
      - summarize
    kind: hosted
`
	require.NoError(t, os.WriteFile(pointer, []byte(content), 0600))

	flags := &initFlags{manifestPointer: pointer}
	staging, cleanup, err := stageAzureYamlTemplate(t.Context(), flags, nil, nil)
	require.NoError(t, err)
	defer cleanup()

	staged, err := os.ReadFile(filepath.Join(staging, "azure.yaml"))
	require.NoError(t, err)
	require.YAMLEq(t, content, string(staged))
}

func TestAdoptedServiceHasCodeConfig(t *testing.T) {
	tests := []struct {
		name string
		svc  *azdext.ServiceConfig
		want bool
	}{
		{
			name: "nil additional properties",
			svc:  &azdext.ServiceConfig{},
			want: false,
		},
		{
			name: "empty additional properties",
			svc: &azdext.ServiceConfig{
				AdditionalProperties: &structpb.Struct{Fields: map[string]*structpb.Value{}},
			},
			want: false,
		},
		{
			name: "codeConfiguration present with struct value",
			svc: &azdext.ServiceConfig{
				AdditionalProperties: &structpb.Struct{Fields: map[string]*structpb.Value{
					"codeConfiguration": structpb.NewStructValue(&structpb.Struct{
						Fields: map[string]*structpb.Value{
							"runtime":    structpb.NewStringValue("python_3_13"),
							"entryPoint": structpb.NewStringValue("app.py"),
						},
					}),
				}},
			},
			want: true,
		},
		{
			name: "codeConfiguration present but null",
			svc: &azdext.ServiceConfig{
				AdditionalProperties: &structpb.Struct{Fields: map[string]*structpb.Value{
					"codeConfiguration": structpb.NewNullValue(),
				}},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, adoptedServiceHasCodeConfig(tt.svc))
		})
	}
}

func TestAdoptedServiceHasDocker(t *testing.T) {
	tests := []struct {
		name string
		svc  *azdext.ServiceConfig
		want bool
	}{
		{
			name: "nil additional properties",
			svc:  &azdext.ServiceConfig{},
			want: false,
		},
		{
			name: "empty additional properties",
			svc: &azdext.ServiceConfig{
				AdditionalProperties: &structpb.Struct{Fields: map[string]*structpb.Value{}},
			},
			want: false,
		},
		{
			name: "docker present with struct value",
			svc: &azdext.ServiceConfig{
				AdditionalProperties: &structpb.Struct{Fields: map[string]*structpb.Value{
					"docker": structpb.NewStructValue(&structpb.Struct{
						Fields: map[string]*structpb.Value{
							"remoteBuild": structpb.NewBoolValue(true),
						},
					}),
				}},
			},
			want: true,
		},
		{
			name: "docker present but null",
			svc: &azdext.ServiceConfig{
				AdditionalProperties: &structpb.Struct{Fields: map[string]*structpb.Value{
					"docker": structpb.NewNullValue(),
				}},
			},
			want: false,
		},
		{
			name: "non-nil GetDocker but no docker in additionalProperties",
			svc: &azdext.ServiceConfig{
				Docker:               &azdext.DockerProjectOptions{},
				AdditionalProperties: &structpb.Struct{Fields: map[string]*structpb.Value{}},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, adoptedServiceHasDocker(tt.svc))
		})
	}
}

func TestValidateImageFlagInAdoptionPath(t *testing.T) {
	t.Run("image with deploy-mode code is rejected", func(t *testing.T) {
		err := validateImageFlag("myacr.azurecr.io/agent:v1", "code")
		require.Error(t, err)
		require.Contains(t, err.Error(), "--image cannot be used with --deploy-mode code")
	})

	t.Run("image with deploy-mode container is allowed", func(t *testing.T) {
		err := validateImageFlag("myacr.azurecr.io/agent:v1", "container")
		require.NoError(t, err)
	})

	t.Run("image with no deploy-mode is allowed", func(t *testing.T) {
		err := validateImageFlag("myacr.azurecr.io/agent:v1", "")
		require.NoError(t, err)
	})

	t.Run("no image is always valid", func(t *testing.T) {
		require.NoError(t, validateImageFlag("", "code"))
		require.NoError(t, validateImageFlag("", "container"))
		require.NoError(t, validateImageFlag("", ""))
	})

	t.Run("invalid image format is rejected", func(t *testing.T) {
		err := validateImageFlag("not-a-valid-image", "")
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid image URL")
	})
}

func TestFoundryDeployments(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []foundryDeploymentEntry
	}{
		{
			name: "single deployment under ai-project",
			content: `name: foundry-simple
services:
  ai-project:
    host: azure.ai.project
    deployments:
      - name: gpt-4o-mini
        model:
          format: OpenAI
          name: gpt-4o-mini
          version: "2024-07-18"
        sku:
          name: GlobalStandard
          capacity: 50
  assistant:
    host: azure.ai.agent
`,
			want: []foundryDeploymentEntry{
				{
					ServiceName: "ai-project",
					Deployment: project.Deployment{
						Name:  "gpt-4o-mini",
						Model: project.DeploymentModel{Format: "OpenAI", Name: "gpt-4o-mini", Version: "2024-07-18"},
						Sku:   project.DeploymentSku{Name: "GlobalStandard", Capacity: 50},
					},
				},
			},
		},
		{
			name: "multiple deployments",
			content: `name: multi-model
services:
  ai-project:
    host: azure.ai.project
    deployments:
      - name: gpt-4o
        model:
          format: OpenAI
          name: gpt-4o
          version: "2024-08-06"
        sku:
          name: GlobalStandard
          capacity: 100
      - name: text-embedding
        model:
          format: OpenAI
          name: text-embedding-ada-002
          version: "2"
        sku:
          name: Standard
          capacity: 10
`,
			want: []foundryDeploymentEntry{
				{
					ServiceName: "ai-project",
					Deployment: project.Deployment{
						Name:  "gpt-4o",
						Model: project.DeploymentModel{Format: "OpenAI", Name: "gpt-4o", Version: "2024-08-06"},
						Sku:   project.DeploymentSku{Name: "GlobalStandard", Capacity: 100},
					},
				},
				{
					ServiceName: "ai-project",
					Deployment: project.Deployment{
						Name:  "text-embedding",
						Model: project.DeploymentModel{Format: "OpenAI", Name: "text-embedding-ada-002", Version: "2"},
						Sku:   project.DeploymentSku{Name: "Standard", Capacity: 10},
					},
				},
			},
		},
		{
			name: "no deployments section",
			content: `name: no-deploy
services:
  ai-project:
    host: azure.ai.project
`,
			want: nil,
		},
		{
			name: "non-project host ignored",
			content: `name: agent-only
services:
  assistant:
    host: azure.ai.agent
    deployments:
      - name: should-be-ignored
        model:
          name: gpt-4o
`,
			want: nil,
		},
		{
			name:    "empty content",
			content: "",
			want:    nil,
		},
		{
			name:    "malformed yaml",
			content: "name: [oops",
			want:    nil,
		},
		{
			name: "missing model and sku fields",
			content: `name: partial
services:
  ai-project:
    host: azure.ai.project
    deployments:
      - name: bare-deploy
`,
			want: []foundryDeploymentEntry{
				{
					ServiceName: "ai-project",
					Deployment: project.Deployment{
						Name:  "bare-deploy",
						Model: project.DeploymentModel{},
						Sku:   project.DeploymentSku{},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := foundryDeployments([]byte(tt.content))
			require.Equal(t, tt.want, got)
		})
	}
}

func TestAdoptedAgentNameConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		svc      *azdext.ServiceConfig
		wantName string
		wantPath string
	}{
		{
			name: "unified inline hosted agent",
			svc: &azdext.ServiceConfig{
				AdditionalProperties: &structpb.Struct{Fields: map[string]*structpb.Value{
					"kind": structpb.NewStringValue("hosted"),
					"name": structpb.NewStringValue("inline-agent"),
				}},
			},
			wantName: "inline-agent",
			wantPath: "name",
		},
		{
			name: "unified inline workflow agent",
			svc: &azdext.ServiceConfig{
				AdditionalProperties: &structpb.Struct{Fields: map[string]*structpb.Value{
					"kind": structpb.NewStringValue("workflow"),
					"name": structpb.NewStringValue("workflow-agent"),
				}},
			},
			wantName: "workflow-agent",
			wantPath: "name",
		},
		{
			name: "deprecated config-nested agent",
			svc: &azdext.ServiceConfig{
				AdditionalProperties: &structpb.Struct{Fields: map[string]*structpb.Value{
					"docker": structpb.NewStructValue(&structpb.Struct{}),
				}},
				Config: &structpb.Struct{Fields: map[string]*structpb.Value{
					"kind": structpb.NewStringValue("hosted"),
					"name": structpb.NewStringValue("legacy-agent"),
				}},
			},
			wantName: "legacy-agent",
			wantPath: "config.name",
		},
		{
			name: "inline definition remains authoritative during partial migration",
			svc: &azdext.ServiceConfig{
				AdditionalProperties: &structpb.Struct{Fields: map[string]*structpb.Value{
					"kind": structpb.NewStringValue("hosted"),
				}},
				Config: &structpb.Struct{Fields: map[string]*structpb.Value{
					"kind": structpb.NewStringValue("hosted"),
					"name": structpb.NewStringValue("legacy-agent"),
				}},
			},
			wantPath: "name",
		},
		{
			name: "service properties without agent definition",
			svc: &azdext.ServiceConfig{
				AdditionalProperties: &structpb.Struct{Fields: map[string]*structpb.Value{
					"name": structpb.NewStringValue("not-an-agent-definition"),
				}},
			},
		},
		{name: "nil service"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotName, gotPath := adoptedAgentNameConfig(tt.svc)
			require.Equal(t, tt.wantName, gotName)
			require.Equal(t, tt.wantPath, gotPath)
		})
	}
}

func TestAdoptedAgentNameConflictSuggestion(t *testing.T) {
	t.Parallel()

	suggestion := adoptedAgentNameConflictSuggestion()
	require.Contains(t, suggestion, "--agent-name")
	require.Contains(t, suggestion, "adopted azure.yaml")
}

func newAdoptedAgentNameTestClient(
	t *testing.T,
	projectServer azdext.ProjectServiceServer,
	promptServer azdext.PromptServiceServer,
) *azdext.AzdClient {
	t.Helper()

	grpcServer := grpc.NewServer()
	azdext.RegisterProjectServiceServer(grpcServer, projectServer)
	azdext.RegisterPromptServiceServer(grpcServer, promptServer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() {
		_ = grpcServer.Serve(listener)
	}()
	t.Cleanup(func() {
		grpcServer.Stop()
		_ = listener.Close()
	})

	client, err := azdext.NewAzdClient(azdext.WithAddress(listener.Addr().String()))
	require.NoError(t, err)
	t.Cleanup(func() {
		client.Close()
	})

	return client
}

func TestUpdateAdoptedAgentNames_PersistsReplacement(t *testing.T) {
	t.Parallel()

	server := &recordingProjectServer{
		existing: map[string]*azdext.ServiceConfig{
			"agent-service": {
				Name: "agent-service",
				Host: AiAgentHost,
				AdditionalProperties: &structpb.Struct{Fields: map[string]*structpb.Value{
					"kind": structpb.NewStringValue("hosted"),
					"name": structpb.NewStringValue("existing-agent"),
				}},
			},
			"ai-project": {
				Name: "ai-project",
				Host: AiProjectHost,
			},
		},
	}
	prompts := &testPromptServiceServer{
		confirmResponses: []bool{false},
		promptResponses:  []string{"replacement-agent"},
	}
	client := newAdoptedAgentNameTestClient(t, server, prompts)
	checker := &fakeConflictAgentChecker{
		exists: map[string]bool{"existing-agent": true},
	}

	err := updateAdoptedAgentNames(
		t.Context(),
		client,
		func(ctx context.Context, agentName string) (string, error) {
			return resolveExistingAgentNameConflictWithChecker(
				ctx,
				client,
				checker,
				false,
				agentName,
			)
		},
	)
	require.NoError(t, err)
	require.Equal(t, []string{"existing-agent", "replacement-agent"}, checker.calls)
	require.Len(t, prompts.confirmRequests, 1)
	require.Len(t, prompts.promptRequests, 1)

	server.mu.Lock()
	defer server.mu.Unlock()
	require.Equal(t, "agent-service", server.configValues["name"].serviceName)
	require.Equal(t, "replacement-agent", server.configValues["name"].value)
}

func TestUpdateAdoptedAgentNames_UsesLegacyConfigPath(t *testing.T) {
	t.Parallel()

	server := &recordingProjectServer{
		existing: map[string]*azdext.ServiceConfig{
			"agent-service": {
				Name: "agent-service",
				Host: AiAgentHost,
				Config: &structpb.Struct{Fields: map[string]*structpb.Value{
					"kind": structpb.NewStringValue("hosted"),
					"name": structpb.NewStringValue("existing-agent"),
				}},
			},
		},
	}
	client := newProjectRecorderClient(t, server)

	err := updateAdoptedAgentNames(
		t.Context(),
		client,
		func(_ context.Context, _ string) (string, error) {
			return "replacement-agent", nil
		},
	)
	require.NoError(t, err)

	server.mu.Lock()
	defer server.mu.Unlock()
	require.Equal(t, "agent-service", server.configValues["config.name"].serviceName)
	require.Equal(t, "replacement-agent", server.configValues["config.name"].value)
}

func TestUpdateAdoptedAgentNames_UnchangedNamesAreNotWritten(t *testing.T) {
	t.Parallel()

	server := &recordingProjectServer{
		existing: map[string]*azdext.ServiceConfig{
			"zeta-service": {
				Name: "zeta-service",
				Host: AiAgentHost,
				AdditionalProperties: &structpb.Struct{Fields: map[string]*structpb.Value{
					"kind": structpb.NewStringValue("hosted"),
					"name": structpb.NewStringValue("zeta-agent"),
				}},
			},
			"alpha-service": {
				Name: "alpha-service",
				Host: AiAgentHost,
				AdditionalProperties: &structpb.Struct{Fields: map[string]*structpb.Value{
					"kind": structpb.NewStringValue("hosted"),
					"name": structpb.NewStringValue("alpha-agent"),
				}},
			},
		},
	}
	client := newProjectRecorderClient(t, server)

	var checkedNames []string
	err := updateAdoptedAgentNames(
		t.Context(),
		client,
		func(_ context.Context, agentName string) (string, error) {
			checkedNames = append(checkedNames, agentName)
			return agentName, nil
		},
	)
	require.NoError(t, err)
	require.Equal(t, []string{"alpha-agent", "zeta-agent"}, checkedNames)

	server.mu.Lock()
	defer server.mu.Unlock()
	require.Empty(t, server.configValues)
}

func TestApplyAdoptedAgentNameOverride_PersistsFlagName(t *testing.T) {
	t.Parallel()

	server := &recordingProjectServer{
		existing: map[string]*azdext.ServiceConfig{
			"agent-service": {
				Name: "agent-service",
				Host: AiAgentHost,
				AdditionalProperties: &structpb.Struct{Fields: map[string]*structpb.Value{
					"kind": structpb.NewStringValue("hosted"),
					"name": structpb.NewStringValue("echo-activity"),
				}},
			},
		},
	}
	client := newProjectRecorderClient(t, server)

	err := applyAdoptedAgentNameOverride(t.Context(), client, "test0804")
	require.NoError(t, err)

	server.mu.Lock()
	defer server.mu.Unlock()
	require.Equal(t, "agent-service", server.configValues["name"].serviceName)
	require.Equal(t, "test0804", server.configValues["name"].value)
}

func TestApplyAdoptedAgentNameOverride_PersistsFlagNameForRefService(t *testing.T) {
	t.Parallel()

	server := &recordingProjectServer{
		existing: map[string]*azdext.ServiceConfig{
			"agent-service": {
				Name: "agent-service",
				Host: AiAgentHost,
				AdditionalProperties: &structpb.Struct{Fields: map[string]*structpb.Value{
					"$ref": structpb.NewStringValue("./agent.yaml"),
				}},
			},
		},
	}
	client := newProjectRecorderClient(t, server)

	err := applyAdoptedAgentNameOverride(t.Context(), client, "test0804")
	require.NoError(t, err)

	server.mu.Lock()
	defer server.mu.Unlock()
	require.Equal(t, "agent-service", server.configValues["name"].serviceName)
	require.Equal(t, "test0804", server.configValues["name"].value)
}

func TestApplyAdoptedAgentNameOverride_RejectsMultipleAgents(t *testing.T) {
	t.Parallel()

	server := &recordingProjectServer{
		existing: map[string]*azdext.ServiceConfig{
			"agent-a": {
				Name: "agent-a",
				Host: AiAgentHost,
				AdditionalProperties: &structpb.Struct{Fields: map[string]*structpb.Value{
					"kind": structpb.NewStringValue("hosted"),
					"name": structpb.NewStringValue("agent-a"),
				}},
			},
			"agent-b": {
				Name: "agent-b",
				Host: AiAgentHost,
				AdditionalProperties: &structpb.Struct{Fields: map[string]*structpb.Value{
					"kind": structpb.NewStringValue("hosted"),
					"name": structpb.NewStringValue("agent-b"),
				}},
			},
		},
	}
	client := newProjectRecorderClient(t, server)

	err := applyAdoptedAgentNameOverride(t.Context(), client, "test0804")
	require.Error(t, err)
	require.Contains(t, err.Error(), "multiple agent services")

	server.mu.Lock()
	defer server.mu.Unlock()
	require.Empty(t, server.configValues)
}

func TestApplyAdoptedAgentNameOverride_RejectsNoAgentService(t *testing.T) {
	t.Parallel()

	server := &recordingProjectServer{
		existing: map[string]*azdext.ServiceConfig{
			"ai-project": {
				Name: "ai-project",
				Host: AiProjectHost,
			},
		},
	}
	client := newProjectRecorderClient(t, server)

	err := applyAdoptedAgentNameOverride(t.Context(), client, "test0804")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no agent service")

	server.mu.Lock()
	defer server.mu.Unlock()
	require.Empty(t, server.configValues)
}

func TestAdoptedAgentNameOverride_IgnoresResolvedDefaultWhenFlagNotExplicit(t *testing.T) {
	t.Parallel()

	flags := &initFlags{agentName: "resolved-template-default"}

	got, err := adoptedAgentNameOverride(flags)
	require.NoError(t, err)
	require.Empty(t, got)
	require.Equal(t, "resolved-template-default", flags.agentName)
}

func TestAdoptedAgentNameOverride_UsesExplicitFlag(t *testing.T) {
	t.Parallel()

	flags := &initFlags{agentName: "test0804", agentNameExplicit: true}

	got, err := adoptedAgentNameOverride(flags)
	require.NoError(t, err)
	require.Equal(t, "test0804", got)
	require.Equal(t, "test0804", flags.agentName)
}

func TestValidateAdoptedAgentNameOverride_AllowsSingleNamedAgent(t *testing.T) {
	t.Parallel()

	content := []byte(`name: sample
services:
  agent:
    host: azure.ai.agent
    kind: hosted
    name: echo-activity
`)

	require.NoError(t, validateAdoptedAgentNameOverride(content, ""))
}

func TestValidateAdoptedAgentNameOverride_AllowsSingleRefAgent(t *testing.T) {
	t.Parallel()

	content := []byte(`name: sample
services:
  agent:
    host: azure.ai.agent
    $ref: ./agent.yaml
`)

	require.NoError(t, validateAdoptedAgentNameOverride(content, ""))
}

func TestValidateAdoptedAgentNameOverride_AllowsSingleRefOnlyAgent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	refPath := filepath.Join(root, "agent.yaml")
	require.NoError(t, os.WriteFile(refPath, []byte("host: azure.ai.agent\nkind: hosted\n"), 0o600))
	content := []byte(`name: sample
services:
  agent:
    $ref: ./agent.yaml
`)

	require.NoError(t, validateAdoptedAgentNameOverride(content, root))
}

func TestValidateAdoptedAgentNameOverride_AllowsSingleLegacyNamedAgent(t *testing.T) {
	t.Parallel()

	content := []byte(`name: sample
services:
  agent:
    host: azure.ai.agent
    config:
      kind: hosted
      name: echo-activity
`)

	require.NoError(t, validateAdoptedAgentNameOverride(content, ""))
}

func TestValidateAdoptedAgentNameOverride_RejectsMultipleAgents(t *testing.T) {
	t.Parallel()

	content := []byte(`name: sample
services:
  agent-a:
    host: azure.ai.agent
    kind: hosted
    name: agent-a
  agent-b:
    host: azure.ai.agent
    kind: hosted
    name: agent-b
`)

	err := validateAdoptedAgentNameOverride(content, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "multiple agent services")
}

func TestValidateAdoptedAgentNameOverride_RejectsInlineAndRefAgents(t *testing.T) {
	t.Parallel()

	content := []byte(`name: sample
services:
  agent-a:
    host: azure.ai.agent
    kind: hosted
    name: agent-a
  agent-b:
    host: azure.ai.agent
    $ref: ./agent.yaml
`)

	err := validateAdoptedAgentNameOverride(content, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "multiple agent services")
}

func TestValidateAdoptedAgentNameOverride_RejectsInlineAndRefOnlyAgents(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	refPath := filepath.Join(root, "agent-b.yaml")
	require.NoError(t, os.WriteFile(refPath, []byte("host: azure.ai.agent\nkind: hosted\n"), 0o600))
	content := []byte(`name: sample
services:
  agent-a:
    host: azure.ai.agent
    kind: hosted
    name: agent-a
  agent-b:
    $ref: ./agent-b.yaml
`)

	err := validateAdoptedAgentNameOverride(content, root)
	require.Error(t, err)
	require.Contains(t, err.Error(), "multiple agent services")
}

func TestValidateAdoptedAgentNameOverride_RejectsNoAgentService(t *testing.T) {
	t.Parallel()

	content := []byte(`name: sample
services:
  project:
    host: azure.ai.project
`)

	err := validateAdoptedAgentNameOverride(content, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no agent service")
}

// TestStampProjectEndpoint_WritesEndpoint verifies that stampProjectEndpoint
// writes the portable endpoint reference to the existing azure.ai.project
// service via SetServiceConfigValue.
func TestStampProjectEndpoint_WritesEndpoint(t *testing.T) {
	t.Parallel()

	server := &recordingProjectServer{
		existing: map[string]*azdext.ServiceConfig{
			"ai-project": {Name: "ai-project", Host: AiProjectHost},
		},
	}
	client := newProjectRecorderClient(t, server)

	err := stampProjectEndpoint(t.Context(), client, projectEndpointRef)
	require.NoError(t, err)

	server.mu.Lock()
	defer server.mu.Unlock()

	// azure.yaml gets the ${VAR} reference, never the literal URL: the concrete
	// endpoint lives in the azd environment so the project stays portable.
	require.Equal(t, "ai-project", server.configValues["endpoint"].serviceName)
	require.Equal(t, "${FOUNDRY_PROJECT_ENDPOINT}", server.configValues["endpoint"].value)
}

// TestStampProjectEndpoint_NilProject verifies stampProjectEndpoint is a no-op
// when there is no endpoint to stamp (user chose "Create new").
func TestStampProjectEndpoint_NilProject(t *testing.T) {
	t.Parallel()

	server := &recordingProjectServer{
		existing: map[string]*azdext.ServiceConfig{
			"ai-project": {Name: "ai-project", Host: AiProjectHost},
		},
	}
	client := newProjectRecorderClient(t, server)

	err := stampProjectEndpoint(t.Context(), client, "")
	require.NoError(t, err)

	server.mu.Lock()
	defer server.mu.Unlock()
	require.Empty(t, server.configValues, "no SetServiceConfigValue calls expected without an endpoint")
}

// TestStampProjectEndpoint_NoExistingService verifies stampProjectEndpoint is a
// no-op when no azure.ai.project service exists in the project yet.
func TestStampProjectEndpoint_NoExistingService(t *testing.T) {
	t.Parallel()

	server := &recordingProjectServer{
		existing: map[string]*azdext.ServiceConfig{
			"my-agent": {Name: "my-agent", Host: AiAgentHost},
		},
	}
	client := newProjectRecorderClient(t, server)

	err := stampProjectEndpoint(t.Context(), client, projectEndpointRef)
	require.NoError(t, err)

	server.mu.Lock()
	defer server.mu.Unlock()
	require.Empty(t, server.configValues, "no SetServiceConfigValue calls expected when no project service exists")
}
