// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestLooksLikeFoundryAzureYaml(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name: "unified azure.yaml with split foundry hosts",
			content: `name: foundry-simple
services:
  ai-project:
    host: azure.ai.project
  assistant:
    host: azure.ai.agent
    kind: hosted
`,
			want: true,
		},
		{
			name: "legacy microsoft.foundry host",
			content: `name: foundry-legacy
services:
  agents:
    host: microsoft.foundry
`,
			want: true,
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
			want: false,
		},
		{
			name: "azure.yaml with only non-foundry services",
			content: `name: web-app
services:
  web:
    host: containerapp
    language: js
`,
			want: false,
		},
		{
			name:    "empty content",
			content: "",
			want:    false,
		},
		{
			name:    "malformed yaml",
			content: "name: [unterminated",
			want:    false,
		},
		{
			name: "services present but not a map",
			content: `name: broken
services: just-a-string
`,
			want: false,
		},
		{
			name: "service without host",
			content: `name: foundry-noisy
services:
  ai-project:
    deployments: []
`,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, looksLikeFoundryAzureYaml([]byte(tt.content)))
		})
	}
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

// TestAdoptFlow_WritesEndpointForExistingProject verifies that when the adopt
// flow selects an existing Foundry project, the endpoint is stamped onto the
// azure.ai.project service in azure.yaml via SetServiceConfigValue.
func TestAdoptFlow_WritesEndpointForExistingProject(t *testing.T) {
	t.Parallel()

	// Simulate an azure.yaml that already has an ai-project service (from the
	// adopted template) but without an endpoint.
	server := &endpointRecordingProjectServer{
		existing: map[string]*azdext.ServiceConfig{
			"ai-project": {Name: "ai-project", Host: AiProjectHost},
		},
	}
	client := newEndpointRecorderClient(t, server)

	// Simulate what the adopt flow does: look up the existing project service
	// key and stamp the endpoint.
	projectSvcKey := existingProjectServiceKey(t.Context(), client)
	require.Equal(t, "ai-project", projectSvcKey)

	endpoint := "https://myaccount.services.ai.azure.com/api/projects/myproject"
	endpointVal, err := structpb.NewValue(endpoint)
	require.NoError(t, err)

	_, err = client.Project().SetServiceConfigValue(t.Context(), &azdext.SetServiceConfigValueRequest{
		ServiceName: projectSvcKey,
		Path:        "endpoint",
		Value:       endpointVal,
	})
	require.NoError(t, err)

	server.mu.Lock()
	defer server.mu.Unlock()
	require.Equal(t, "ai-project", server.endpointService)
	require.Equal(t, endpoint, server.endpointValue)
}

// TestAdoptFlow_SkipsEndpointWhenNoExistingProject verifies the endpoint is NOT
// written when configureFoundryProject returns nil (user chose "Create new").
func TestAdoptFlow_SkipsEndpointWhenNoExistingProject(t *testing.T) {
	t.Parallel()

	// A nil FoundryProject means "create new" — endpoint should be "".
	var project *FoundryProjectInfo
	require.Equal(t, "", project.Endpoint())
}

// endpointRecordingProjectServer captures SetServiceConfigValue calls for the
// "endpoint" path, in addition to the standard Get behavior.
type endpointRecordingProjectServer struct {
	azdext.UnimplementedProjectServiceServer

	mu              sync.Mutex
	existing        map[string]*azdext.ServiceConfig
	endpointService string
	endpointValue   string
}

func (s *endpointRecordingProjectServer) Get(
	_ context.Context, _ *azdext.EmptyRequest,
) (*azdext.GetProjectResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &azdext.GetProjectResponse{
		Project: &azdext.ProjectConfig{Services: s.existing},
	}, nil
}

func (s *endpointRecordingProjectServer) SetServiceConfigValue(
	_ context.Context, req *azdext.SetServiceConfigValueRequest,
) (*azdext.EmptyResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if req.Path == "endpoint" && req.Value != nil {
		s.endpointService = req.ServiceName
		if str, ok := req.Value.AsInterface().(string); ok {
			s.endpointValue = str
		}
	}
	return &azdext.EmptyResponse{}, nil
}

// newEndpointRecorderClient spins up an in-process gRPC server for endpoint
// recording and returns a client wired to it.
func newEndpointRecorderClient(t *testing.T, server azdext.ProjectServiceServer) *azdext.AzdClient {
	t.Helper()

	grpcServer := grpc.NewServer()
	azdext.RegisterProjectServiceServer(grpcServer, server)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(func() {
		grpcServer.Stop()
		_ = listener.Close()
	})

	client, err := azdext.NewAzdClient(azdext.WithAddress(listener.Addr().String()))
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	return client
}
