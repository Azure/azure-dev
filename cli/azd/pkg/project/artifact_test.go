// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
)

// hyperlinkPrefix is the OSC 8 escape sequence prefix used for terminal hyperlinks.
// The actual WithHyperlink function may not emit this in non-terminal environments,
// so we check for its absence to verify non-clickable behavior.
const hyperlinkPrefix = "\x1b]8;;"

func TestArtifactToString_Endpoint(t *testing.T) {
	tests := []struct {
		name              string
		artifact          *Artifact
		contains          []string
		shouldBeClickable bool
	}{
		{
			name: "remote endpoint is clickable by default",
			artifact: &Artifact{
				Kind:         ArtifactKindEndpoint,
				Location:     "https://example.com/api",
				LocationKind: LocationKindRemote,
			},
			contains: []string{
				"- Endpoint:",
				"https://example.com/api",
			},
			shouldBeClickable: true,
		},
		{
			name: "remote endpoint with clickable=false is not hyperlinked",
			artifact: &Artifact{
				Kind:         ArtifactKindEndpoint,
				Location:     "https://example.com/agents/myagent",
				LocationKind: LocationKindRemote,
				Metadata: map[string]string{
					MetadataKeyClickable: "false",
				},
			},
			contains: []string{
				"- Endpoint:",
				"https://example.com/agents/myagent",
			},
			shouldBeClickable: false,
		},
		{
			name: "agent endpoint with custom label and note",
			artifact: &Artifact{
				Kind:         ArtifactKindEndpoint,
				Location:     "https://example.com/agents/myagent/versions/1",
				LocationKind: LocationKindRemote,
				Metadata: map[string]string{
					"label":              "Agent endpoint",
					MetadataKeyClickable: "false",
					MetadataKeyNote:      "For information on invoking the agent, see https://aka.ms/azd-agents-invoke",
				},
			},
			contains: []string{
				"- Agent endpoint:",
				"https://example.com/agents/myagent/versions/1",
				"For information on invoking the agent, see https://aka.ms/azd-agents-invoke",
			},
			shouldBeClickable: false,
		},
		{
			name: "local endpoint is clickable by default",
			artifact: &Artifact{
				Kind:         ArtifactKindEndpoint,
				Location:     "http://localhost:8080",
				LocationKind: LocationKindLocal,
			},
			contains: []string{
				"- Endpoint:",
				"http://localhost:8080",
			},
			shouldBeClickable: true,
		},
		{
			name: "endpoint with discriminator",
			artifact: &Artifact{
				Kind:         ArtifactKindEndpoint,
				Location:     "https://example.com/api",
				LocationKind: LocationKindRemote,
				Metadata: map[string]string{
					"discriminator": "(primary)",
				},
			},
			contains: []string{
				"- Endpoint:",
				"https://example.com/api",
				"(primary)",
			},
			shouldBeClickable: true,
		},
		{
			name: "clickable=FALSE is case insensitive",
			artifact: &Artifact{
				Kind:         ArtifactKindEndpoint,
				Location:     "https://example.com/api",
				LocationKind: LocationKindRemote,
				Metadata: map[string]string{
					MetadataKeyClickable: "FALSE",
				},
			},
			contains: []string{
				"- Endpoint:",
				"https://example.com/api",
			},
			shouldBeClickable: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.artifact.ToString("")

			for _, expected := range tt.contains {
				require.True(t, strings.Contains(result, expected),
					"Expected output to contain %q, got: %s", expected, result)
			}

			// Check clickability by looking for hyperlink escape sequence
			hasHyperlink := strings.Contains(result, hyperlinkPrefix)
			if tt.shouldBeClickable {
				// In terminal environments, should have hyperlink; in non-terminal, won't have it
				// We can't directly test this without mocking terminal, so we just verify the URL is present
				require.Contains(t, result, tt.artifact.Location)
			} else {
				// Should NOT have hyperlink escape sequence
				require.False(t, hasHyperlink,
					"Expected output NOT to contain hyperlink escape sequence for non-clickable endpoint, got: %q", result)
			}
		})
	}
}

func TestArtifactToString_OtherKinds(t *testing.T) {
	tests := []struct {
		name     string
		artifact *Artifact
		contains string
	}{
		{
			name: "container remote",
			artifact: &Artifact{
				Kind:         ArtifactKindContainer,
				Location:     "myregistry.azurecr.io/myimage:latest",
				LocationKind: LocationKindRemote,
			},
			contains: "- Remote Image:",
		},
		{
			name: "container local",
			artifact: &Artifact{
				Kind:         ArtifactKindContainer,
				Location:     "myimage:latest",
				LocationKind: LocationKindLocal,
			},
			contains: "- Container:",
		},
		{
			name: "archive",
			artifact: &Artifact{
				Kind:         ArtifactKindArchive,
				Location:     "/path/to/output.zip",
				LocationKind: LocationKindLocal,
			},
			contains: "- Package Output:",
		},
		{
			name: "directory",
			artifact: &Artifact{
				Kind:         ArtifactKindDirectory,
				Location:     "/path/to/build",
				LocationKind: LocationKindLocal,
			},
			contains: "- Build Output:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.artifact.ToString("")
			require.Contains(t, result, tt.contains)
		})
	}
}

func TestArtifactToString_EmptyLocation(t *testing.T) {
	artifact := &Artifact{
		Kind:         ArtifactKindEndpoint,
		Location:     "",
		LocationKind: LocationKindRemote,
	}

	result := artifact.ToString("")
	require.Empty(t, result)
}

func Test_ArtifactToString(t *testing.T) {
	tests := []struct {
		name     string
		artifact Artifact
		contains string
	}{
		{
			"Endpoint_remote",
			Artifact{
				Kind:         ArtifactKindEndpoint,
				Location:     "https://app.azurewebsites.net",
				LocationKind: LocationKindRemote,
			},
			"https://app.azurewebsites.net",
		},
		{
			"Container_remote",
			Artifact{
				Kind:         ArtifactKindContainer,
				Location:     "myregistry.azurecr.io/app:latest",
				LocationKind: LocationKindRemote,
			},
			"Remote Image",
		},
		{
			"Container_local",
			Artifact{
				Kind:         ArtifactKindContainer,
				Location:     "app:latest",
				LocationKind: LocationKindLocal,
			},
			"Container",
		},
		{
			"Archive",
			Artifact{
				Kind:         ArtifactKindArchive,
				Location:     "/tmp/app.zip",
				LocationKind: LocationKindLocal,
			},
			"Package Output",
		},
		{
			"Directory",
			Artifact{
				Kind:         ArtifactKindDirectory,
				Location:     "/tmp/output",
				LocationKind: LocationKindLocal,
			},
			"Build Output",
		},
		{
			"Unknown",
			Artifact{
				Kind:         ArtifactKind("unknown"),
				Location:     "test",
				LocationKind: LocationKindLocal,
			},
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.artifact.ToString("")
			if tt.contains != "" {
				assert.Contains(t, result, tt.contains)
			} else {
				assert.Equal(t, "", result)
			}
		})
	}
}

func Test_ArtifactToString_Endpoint_WithNote(t *testing.T) {
	a := Artifact{
		Kind:         ArtifactKindEndpoint,
		Location:     "https://example.com",
		LocationKind: LocationKindRemote,
		Metadata:     map[string]string{MetadataKeyNote: "Primary endpoint"},
	}
	result := a.ToString("")
	assert.Contains(t, result, "https://example.com")
	assert.Contains(t, result, "Primary endpoint")
}

func Test_functionAppTarget_Package(t *testing.T) {
	t.Run("WithDirectoryArtifact_CreatesZip", func(t *testing.T) {
		tempDir := t.TempDir()
		// Create a file in the temp dir for the zip to contain
		require.NoError(t, os.WriteFile(filepath.Join(tempDir, "index.js"), []byte("exports.handler = () => {}"), 0600))

		target := &functionAppTarget{}
		svcConfig := &ServiceConfig{
			Name:     "func-svc",
			Language: ServiceLanguageJavaScript,
			Project:  &ProjectConfig{},
		}

		svcCtx := NewServiceContext()
		require.NoError(t, svcCtx.Package.Add(
			&Artifact{Kind: ArtifactKindDirectory, Location: tempDir, LocationKind: LocationKindLocal},
		))

		progress := async.NewProgress[ServiceProgress]()
		// Drain progress channel to prevent blocking
		go func() {
			for range progress.Progress() {
			}
		}()

		result, err := target.Package(t.Context(), svcConfig, svcCtx, progress)
		progress.Done()
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Len(t, result.Artifacts, 1)
		assert.Equal(t, ArtifactKindArchive, result.Artifacts[0].Kind)
		assert.Equal(t, LocationKindLocal, result.Artifacts[0].LocationKind)
		// Should end in .zip
		assert.Equal(t, ".zip", filepath.Ext(result.Artifacts[0].Location))
	})

	t.Run("WithZipArtifact_PassThrough", func(t *testing.T) {
		tempDir := t.TempDir()
		zipPath := filepath.Join(tempDir, "deploy.zip")
		require.NoError(t, os.WriteFile(zipPath, []byte("fake-zip"), 0600))

		target := &functionAppTarget{}
		svcCtx := NewServiceContext()
		require.NoError(t, svcCtx.Package.Add(
			&Artifact{Kind: ArtifactKindDirectory, Location: zipPath, LocationKind: LocationKindLocal},
		))

		progress := async.NewProgress[ServiceProgress]()
		go func() {
			for range progress.Progress() {
			}
		}()

		result, err := target.Package(t.Context(), nil, svcCtx, progress)
		progress.Done()
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Len(t, result.Artifacts, 1)
		assert.Equal(t, zipPath, result.Artifacts[0].Location)
	})

	t.Run("NoArtifact_Error", func(t *testing.T) {
		target := &functionAppTarget{}
		svcCtx := NewServiceContext()
		progress := async.NewProgress[ServiceProgress]()
		go func() {
			for range progress.Progress() {
			}
		}()

		_, err := target.Package(t.Context(), nil, svcCtx, progress)
		progress.Done()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no build result")
	})
}

func Test_ServiceManager_Package_OutputPath_File(t *testing.T) {
	tmpDir := t.TempDir()
	env := environment.NewWithValues("test", map[string]string{})

	// Create a fake package artifact file
	pkgFile := filepath.Join(tmpDir, "app.zip")
	require.NoError(t, os.WriteFile(pkgFile, []byte("zip-content"), 0600))

	target := &fakeServiceTargetStub{
		packageResult: &ServicePackageResult{
			Artifacts: ArtifactCollection{
				&Artifact{
					Kind:         ArtifactKindArchive,
					LocationKind: LocationKindLocal,
					Location:     pkgFile,
				},
			},
		},
	}
	framework := NewNoOpProject(env)
	locator := &fakeServiceLocator{framework: framework, target: target}
	resMgr := &fakeResourceManager{
		targetResource: environment.NewTargetResource("sub", "rg", "res", "type"),
	}

	sm := makeServiceManager(env, locator, resMgr)
	svcConfig := makeSvcConfigWithDispatcher("web", ServiceLanguageJavaScript, AppServiceTarget, t.TempDir())
	progress := newDrainedProgress()
	defer progress.Done()

	// File path output
	outDir := filepath.Join(tmpDir, "out")
	outFile := filepath.Join(outDir, "result.zip")
	result, err := sm.Package(t.Context(), svcConfig, nil, progress, &PackageOptions{OutputPath: outFile})
	require.NoError(t, err)
	require.NotNil(t, result)

	// The file should have been moved
	_, err = os.Stat(outFile)
	assert.NoError(t, err, "output file should exist")
}

func Test_ServiceManager_Package_OutputPath_Dir(t *testing.T) {
	tmpDir := t.TempDir()
	env := environment.NewWithValues("test", map[string]string{})

	// Create a fake package artifact file
	pkgFile := filepath.Join(tmpDir, "app.zip")
	require.NoError(t, os.WriteFile(pkgFile, []byte("zip-content"), 0600))

	target := &fakeServiceTargetStub{
		packageResult: &ServicePackageResult{
			Artifacts: ArtifactCollection{
				&Artifact{
					Kind:         ArtifactKindArchive,
					LocationKind: LocationKindLocal,
					Location:     pkgFile,
				},
			},
		},
	}
	framework := NewNoOpProject(env)
	locator := &fakeServiceLocator{framework: framework, target: target}
	resMgr := &fakeResourceManager{
		targetResource: environment.NewTargetResource("sub", "rg", "res", "type"),
	}

	sm := makeServiceManager(env, locator, resMgr)
	svcConfig := makeSvcConfigWithDispatcher("web", ServiceLanguageJavaScript, AppServiceTarget, t.TempDir())
	progress := newDrainedProgress()
	defer progress.Done()

	// Directory path output (no extension → treated as directory)
	outDir := filepath.Join(tmpDir, "outdir")
	result, err := sm.Package(t.Context(), svcConfig, nil, progress, &PackageOptions{OutputPath: outDir})
	require.NoError(t, err)
	require.NotNil(t, result)

	// The file should have been moved to outdir/app.zip
	_, err = os.Stat(filepath.Join(outDir, "app.zip"))
	assert.NoError(t, err, "output file should exist in directory")
}

func Test_ArtifactCollectionToString(t *testing.T) {
	ac := ArtifactCollection{
		{
			Kind:         ArtifactKindEndpoint,
			Location:     "https://app.azurewebsites.net",
			LocationKind: LocationKindRemote,
		},
		{
			Kind:         ArtifactKindContainer,
			Location:     "myacr.azurecr.io/app:latest",
			LocationKind: LocationKindRemote,
		},
		{
			Kind:         ArtifactKindArchive,
			Location:     "/tmp/deploy.zip",
			LocationKind: LocationKindLocal,
		},
	}

	result := ac.ToString("")
	assert.Contains(t, result, "https://app.azurewebsites.net")
	assert.Contains(t, result, "Remote Image")
	assert.Contains(t, result, "Package Output")
}

func Test_ArtifactCollectionToString_Empty(t *testing.T) {
	ac := ArtifactCollection{}
	result := ac.ToString("")
	assert.Contains(t, result, "No artifacts")
}

func Test_ArtifactCollectionToString_WithIndentation(t *testing.T) {
	ac := ArtifactCollection{
		{
			Kind:         ArtifactKindEndpoint,
			Location:     "https://example.com",
			LocationKind: LocationKindRemote,
		},
	}
	result := ac.ToString("  ")
	assert.Contains(t, result, "https://example.com")
}

func Test_ArtifactToString_Endpoint_Discriminator(t *testing.T) {
	a := Artifact{
		Kind:         ArtifactKindEndpoint,
		Location:     "https://example.com",
		LocationKind: LocationKindRemote,
		Metadata:     map[string]string{"label": "Primary"},
	}
	result := a.ToString("")
	assert.Contains(t, result, "https://example.com")
	assert.Contains(t, result, "Primary")
}

func Test_ArtifactToString_EndpointMultipleLabels(t *testing.T) {
	artifacts := ArtifactCollection{
		{
			Kind:         ArtifactKindEndpoint,
			Location:     "https://app1.com",
			LocationKind: LocationKindRemote,
			Metadata:     map[string]string{"label": "App 1"},
		},
		{
			Kind:         ArtifactKindEndpoint,
			Location:     "https://app2.com",
			LocationKind: LocationKindRemote,
			Metadata:     map[string]string{"label": "App 2"},
		},
	}
	result := artifacts.ToString("")
	assert.Contains(t, result, "https://app1.com")
	assert.Contains(t, result, "https://app2.com")
	// Both labels should appear
	assert.Contains(t, result, "App 1")
	assert.Contains(t, result, "App 2")
}

func Test_ArtifactAdd_AllKinds(t *testing.T) {
	kinds := []struct {
		kind ArtifactKind
		loc  string
	}{
		{ArtifactKindEndpoint, "https://example.com"},
		{ArtifactKindContainer, "myimage:latest"},
		{ArtifactKindArchive, "/tmp/app.zip"},
		{ArtifactKindDirectory, "/tmp/output"},
	}

	for _, k := range kinds {
		t.Run(fmt.Sprintf("Add_%s", k.kind), func(t *testing.T) {
			ctx := NewServiceContext()
			err := ctx.Package.Add(&Artifact{
				Kind:         k.kind,
				Location:     k.loc,
				LocationKind: LocationKindLocal,
			})
			require.NoError(t, err)
			assert.Len(t, ctx.Package, 1)
		})
	}
}

func Test_usingSwaConfig(t *testing.T) {
	tests := []struct {
		name      string
		artifacts ArtifactCollection
		expected  bool
	}{
		{
			name:      "empty",
			artifacts: ArtifactCollection{},
			expected:  false,
		},
		{
			name: "hasConfig",
			artifacts: ArtifactCollection{
				{Kind: ArtifactKindConfig, Location: "swa-cli.config.json"},
			},
			expected: true,
		},
		{
			name: "noConfig",
			artifacts: ArtifactCollection{
				{Kind: ArtifactKindDirectory, Location: "/build"},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := usingSwaConfig(tt.artifacts)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func Test_staticWebAppTarget_Package(t *testing.T) {
	t.Run("WithSwaConfig", func(t *testing.T) {
		target := NewStaticWebAppTarget(nil, nil, nil)
		svcCtx := NewServiceContext()
		require.NoError(t, svcCtx.Package.Add(&Artifact{
			Kind:         ArtifactKindConfig,
			Location:     "swa-cli.config.json",
			LocationKind: LocationKindLocal,
		}))

		result, err := target.Package(t.Context(), &ServiceConfig{}, svcCtx, nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, ArtifactKindConfig, result.Artifacts[0].Kind)
	})

	t.Run("WithBuildOutput", func(t *testing.T) {
		target := NewStaticWebAppTarget(nil, nil, nil)
		svcCtx := NewServiceContext()
		require.NoError(t, svcCtx.Package.Add(&Artifact{
			Kind:         ArtifactKindDirectory,
			Location:     "/build/output",
			LocationKind: LocationKindLocal,
		}))

		result, err := target.Package(t.Context(), &ServiceConfig{}, svcCtx, nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, ArtifactKindDirectory, result.Artifacts[0].Kind)
		assert.Equal(t, "/build/output", result.Artifacts[0].Location)
	})

	t.Run("WithOutputPath", func(t *testing.T) {
		target := NewStaticWebAppTarget(nil, nil, nil)
		svcCtx := NewServiceContext()
		require.NoError(t, svcCtx.Package.Add(&Artifact{
			Kind:         ArtifactKindDirectory,
			Location:     "/build/output",
			LocationKind: LocationKindLocal,
		}))

		result, err := target.Package(t.Context(), &ServiceConfig{OutputPath: "dist"}, svcCtx, nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "dist", result.Artifacts[0].Location)
	})
}
