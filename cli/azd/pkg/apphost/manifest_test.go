// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package apphost

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_resolvePublishMode(t *testing.T) {
	tests := []struct {
		name     string
		manifest *Manifest
		expected apphostPublishMode
	}{
		{
			name: "project.v0 returns full azd mode",
			manifest: &Manifest{
				Resources: map[string]*Resource{
					"api": {
						Type: "project.v0",
					},
				},
			},
			expected: publishModeFullAzd,
		},
		{
			name: "container.v0 returns full azd mode",
			manifest: &Manifest{
				Resources: map[string]*Resource{
					"web": {
						Type: "container.v0",
					},
				},
			},
			expected: publishModeFullAzd,
		},
		{
			name: "dockerfile.v0 returns full azd mode",
			manifest: &Manifest{
				Resources: map[string]*Resource{
					"app": {
						Type: "dockerfile.v0",
					},
				},
			},
			expected: publishModeFullAzd,
		},
		{
			name: "mixed v0 and v1 returns full azd mode",
			manifest: &Manifest{
				Resources: map[string]*Resource{
					"api": {
						Type: "project.v0",
					},
					"web": {
						Type: "project.v1",
					},
				},
			},
			expected: publishModeFullAzd,
		},
		{
			name: "manifest with global outputs reference returns hybrid mode",
			manifest: &Manifest{
				Resources: map[string]*Resource{
					"api": {
						Type: "project.v1",
						Env: map[string]string{
							"CONNECTION_STRING": "{.outputs.connectionString}",
						},
					},
				},
			},
			expected: publishModeHybrid,
		},
		{
			name: "project.v1 without global outputs returns full apphost mode",
			manifest: &Manifest{
				Resources: map[string]*Resource{
					"api": {
						Type: "project.v1",
						Env: map[string]string{
							"CONNECTION_STRING": "{infra.outputs.connectionString}",
						},
					},
				},
			},
			expected: publishModeFullApphost,
		},
		{
			name: "container.v1 without global outputs returns full apphost mode",
			manifest: &Manifest{
				Resources: map[string]*Resource{
					"web": {
						Type: "container.v1",
					},
				},
			},
			expected: publishModeFullApphost,
		},
		{
			name: "empty manifest returns full apphost mode",
			manifest: &Manifest{
				Resources: map[string]*Resource{},
			},
			expected: publishModeFullApphost,
		},
		{
			name: "manifest with only infrastructure resources returns full apphost mode",
			manifest: &Manifest{
				Resources: map[string]*Resource{
					"vault": {
						Type: "azure.keyvault.v0",
					},
				},
			},
			expected: publishModeFullApphost,
		},
		{
			name: "complex manifest with v1 and infra outputs returns full apphost mode",
			manifest: &Manifest{
				Resources: map[string]*Resource{
					"api": {
						Type: "project.v1",
						Env: map[string]string{
							"DB_CONNECTION": "{infra.outputs.dbConnectionString}",
						},
					},
					"vault": {
						Type: "azure.keyvault.v0",
					},
				},
			},
			expected: publishModeFullApphost,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := resolvePublishMode(tt.manifest)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestManifest_Warnings(t *testing.T) {
	tests := []struct {
		name        string
		publishMode apphostPublishMode
		expected    string
	}{
		{
			name:        "full azd mode shows limited mode warning",
			publishMode: publishModeFullAzd,
			//nolint:lll
			expected: "  Limited mode Warning: Your Aspire project is delegating the services' host infrastructure to azd.\n" +
				//nolint:lll
				"  This mode is limited. You will not be able to manage the host infrastructure from your AppHost. You'll need to use `azd infra gen` " +
				"to customize the Azure Container Environment and/or Azure Container Apps" +
				"  See more: https://aspire.dev/integrations/cloud/azure/configure-container-apps/",
		},
		{
			name:        "hybrid mode shows deprecation warning",
			publishMode: publishModeHybrid,
			expected: "  Deprecation Warning: " + "Your Aspire project is on hybrid mode. While you can use the AppHost" +
				" to define the Azure Container App, azd defines the Azure Container Environment.\n  This mode is " +
				"deprecated since Aspire 9.4.  " +
				//nolint:lll
				"See more: https://aspire.dev/whats-new/aspire-9-4/#-azure-container-apps-hybrid-mode-removal",
		},
		{
			name:        "full apphost mode shows no warnings",
			publishMode: publishModeFullApphost,
			expected:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := &Manifest{
				publishMode: tt.publishMode,
			}
			result := manifest.Warnings()
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestProjectPaths(t *testing.T) {
	m := &Manifest{Resources: map[string]*Resource{
		"api":   {Type: "project.v0", Path: new("/p/api.csproj")},
		"web":   {Type: "project.v1", Path: new("/p/web.csproj")},
		"other": {Type: "container.v0", Image: new("x")},
	}}
	paths := ProjectPaths(m)
	require.Equal(t, "/p/api.csproj", paths["api"])
	require.Equal(t, "/p/web.csproj", paths["web"])
	_, ok := paths["other"]
	require.False(t, ok)
}

func TestDockerfiles(t *testing.T) {
	m := &Manifest{Resources: map[string]*Resource{
		"df": {
			Type:      "dockerfile.v0",
			Path:      new("/p/Dockerfile"),
			Context:   new("/p"),
			Env:       map[string]string{"A": "1"},
			BuildArgs: map[string]string{"B": "2"},
			Args:      []string{"--flag"},
		},
		"ignored": {Type: "project.v0"},
	}}
	d := Dockerfiles(m)
	require.Len(t, d, 1)
	require.Equal(t, "/p/Dockerfile", d["df"].Path)
	require.Equal(t, "/p", d["df"].Context)
	require.Equal(t, "1", d["df"].Env["A"])
	require.Equal(t, "2", d["df"].BuildArgs["B"])
}

func TestContainers(t *testing.T) {
	m := &Manifest{Resources: map[string]*Resource{
		"c": {
			Type:  "container.v0",
			Image: new("nginx:latest"),
			Env:   map[string]string{"A": "1"},
			Args:  []string{"-x"},
		},
		"ignored": {Type: "project.v0"},
	}}
	c := Containers(m)
	require.Len(t, c, 1)
	require.Equal(t, "nginx:latest", c["c"].Image)
}

func TestBuildContainers_Image(t *testing.T) {
	m := &Manifest{Resources: map[string]*Resource{
		"c": {Type: "container.v1", Image: new("myimg:1.0")},
	}}
	got, err := BuildContainers(m)
	require.NoError(t, err)
	require.Equal(t, "myimg:1.0", got["c"].Image)
	require.Equal(t, 8080, got["c"].DefaultTargetPort)
}

func TestBuildContainerFromResource(t *testing.T) {
	t.Run("v0_image", func(t *testing.T) {
		r := &Resource{Type: "container.v0", Image: new("redis")}
		bc, err := buildContainerFromResource(r)
		require.NoError(t, err)
		require.Equal(t, "redis", bc.Image)
		require.Equal(t, 80, bc.DefaultTargetPort)
		require.Nil(t, bc.Build)
	})
	t.Run("v1_default_port", func(t *testing.T) {
		r := &Resource{Type: "container.v1", Image: new("x")}
		bc, err := buildContainerFromResource(r)
		require.NoError(t, err)
		require.Equal(t, 8080, bc.DefaultTargetPort)
	})
	t.Run("dockerfile_v0_with_context", func(t *testing.T) {
		r := &Resource{
			Type:    "dockerfile.v0",
			Path:    new("/abs/Dockerfile"),
			Context: new("/abs"),
		}
		bc, err := buildContainerFromResource(r)
		require.NoError(t, err)
		require.NotNil(t, bc.Build)
		require.Equal(t, "/abs", bc.Build.Context)
		require.Equal(t, "/abs/Dockerfile", bc.Build.Dockerfile)
	})
	t.Run("container_v1_build", func(t *testing.T) {
		r := &Resource{
			Type: "container.v1",
			Build: &ContainerV1Build{
				Context:    "/ctx",
				Dockerfile: "/ctx/Dockerfile",
				Args:       map[string]string{"K": "V"},
				BuildOnly:  true,
			},
		}
		bc, err := buildContainerFromResource(r)
		require.NoError(t, err)
		require.NotNil(t, bc.Build)
		require.Equal(t, "/ctx", bc.Build.Context)
		require.True(t, bc.Build.BuildOnly)
	})
	t.Run("error_no_image_no_build", func(t *testing.T) {
		r := &Resource{Type: "container.v1"}
		_, err := buildContainerFromResource(r)
		require.Error(t, err)
	})
	t.Run("with_deployment", func(t *testing.T) {
		r := &Resource{
			Type:  "container.v1",
			Image: new("x"),
			Deployment: &DeploymentMetadata{
				Type:   "azure.bicep.v0",
				Path:   new("/m/thing.bicep"),
				Params: map[string]any{"a": "b"},
			},
		}
		bc, err := buildContainerFromResource(r)
		require.NoError(t, err)
		require.Equal(t, "thing.bicep", bc.DeploymentSource)
		require.Equal(t, "b", bc.DeploymentParams["a"])
	})
}

func TestInputParameter_WithInputs(t *testing.T) {
	r := &Resource{
		Value: "{self.inputs.pw}",
		Inputs: map[string]Input{
			"pw": {Secret: true},
		},
	}
	in, err := InputParameter("self", r)
	require.NoError(t, err)
	require.NotNil(t, in)
	require.Equal(t, "string", in.Type)
	require.True(t, in.Secret)
}

func TestGenerateProjectArtifacts(t *testing.T) {
	tmp := t.TempDir()
	m := &Manifest{Resources: map[string]*Resource{}}
	appHostProject := tmp + "/MyApp/MyApp.AppHost.csproj"
	files, err := GenerateProjectArtifacts(t.Context(), tmp, "demo", m, appHostProject)
	require.NoError(t, err)
	require.Contains(t, files, "azure.yaml")
	require.Contains(t, files, "next-steps.md")
	require.Contains(t, files["azure.yaml"].Contents, "demo")
}

// Extra resolvePublishMode cases.
func TestResolvePublishMode(t *testing.T) {
	t.Run("container_v1_with_global_outputs_is_hybrid", func(t *testing.T) {
		m := &Manifest{Resources: map[string]*Resource{
			"c": {
				Type: "container.v1",
				Env:  map[string]string{"X": "{.outputs.foo}"},
			},
		}}
		require.Equal(t, publishModeHybrid, resolvePublishMode(m))
	})
}

func TestInfraGenerator_ContainerV0_ImageAndBuildError(t *testing.T) {
	g := newInfraGenerator()
	m := &Manifest{Resources: map[string]*Resource{
		"bad": {
			Type:  "container.v1",
			Image: new("x"),
			Build: &ContainerV1Build{Context: "/c", Dockerfile: "/c/Dockerfile"},
		},
	}}
	err := g.LoadManifest(m)
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot have both an image and a build")
}

func TestInfraGenerator_BicepV0_ScopeWithoutResourceGroup(t *testing.T) {
	g := newInfraGenerator()
	m := &Manifest{Resources: map[string]*Resource{
		"mod": {
			Type:  "azure.bicep.v0",
			Path:  new("mod/mod.bicep"),
			Scope: &BicepModuleScope{},
		},
	}}
	err := g.LoadManifest(m)
	require.Error(t, err)
	require.Contains(t, err.Error(), "scope without a resource group")
}
