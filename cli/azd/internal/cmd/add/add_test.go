// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package add

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/stretchr/testify/require"
)

func TestEnsureCompatibleProject(t *testing.T) {
	tests := []struct {
		name                   string
		setupFunc              func(t *testing.T) *project.ProjectConfig
		expectError            bool
		expectedErrorSubstring string
	}{
		{
			name: "no infra folder",
			setupFunc: func(t *testing.T) *project.ProjectConfig {
				// Create temp project directory
				tempDir := t.TempDir()

				return &project.ProjectConfig{
					Path: tempDir,
					Infra: provisioning.Options{
						Path:   "infra",
						Module: "main",
					},
					Resources: map[string]*project.ResourceConfig{},
				}
			},
			expectError: false,
		},
		{
			name: "infra folder with no module file",
			setupFunc: func(t *testing.T) *project.ProjectConfig {
				// Create temp project directory
				tempDir := t.TempDir()

				// Create infra directory but no main.bicep file
				infraDir := filepath.Join(tempDir, "infra")
				err := os.MkdirAll(infraDir, 0755)
				require.NoError(t, err)

				return &project.ProjectConfig{
					Path: tempDir,
					Infra: provisioning.Options{
						Path:   "infra",
						Module: "main",
					},
					Resources: map[string]*project.ResourceConfig{},
				}
			},
			expectError: false,
		},
		{
			name: "infra folder with main.bicep and resources",
			setupFunc: func(t *testing.T) *project.ProjectConfig {
				// Create temp project directory
				tempDir := t.TempDir()

				// Create infra directory and main.bicep file
				infraDir := filepath.Join(tempDir, "infra")
				err := os.MkdirAll(infraDir, 0755)
				require.NoError(t, err)

				mainBicepPath := filepath.Join(infraDir, "main.bicep")
				err = os.WriteFile(mainBicepPath, []byte("// bicep content"), osutil.PermissionFileOwnerOnly)
				require.NoError(t, err)

				return &project.ProjectConfig{
					Path: tempDir,
					Infra: provisioning.Options{
						Path:   "infra",
						Module: "main",
					},
					Resources: map[string]*project.ResourceConfig{
						"storage": {
							Name: "storage",
							Type: project.ResourceTypeStorage,
						},
					},
				}
			},
			expectError: false,
		},
		{
			name: "infra folder with main.bicepparam and resources",
			setupFunc: func(t *testing.T) *project.ProjectConfig {
				// Create temp project directory
				tempDir := t.TempDir()

				// Create infra directory and main.bicepparam file
				infraDir := filepath.Join(tempDir, "infra")
				err := os.MkdirAll(infraDir, 0755)
				require.NoError(t, err)

				mainParamPath := filepath.Join(infraDir, "main.bicepparam")
				err = os.WriteFile(mainParamPath, []byte("// bicepparam content"), osutil.PermissionFileOwnerOnly)
				require.NoError(t, err)

				return &project.ProjectConfig{
					Path: tempDir,
					Infra: provisioning.Options{
						Path:   "infra",
						Module: "main",
					},
					Resources: map[string]*project.ResourceConfig{
						"storage": {
							Name: "storage",
							Type: project.ResourceTypeStorage,
						},
					},
				}
			},
			expectError: false,
		},
		{
			name: "infra folder with module file but no resources",
			setupFunc: func(t *testing.T) *project.ProjectConfig {
				// Create temp project directory
				tempDir := t.TempDir()

				// Create infra directory and main.bicep file
				infraDir := filepath.Join(tempDir, "infra")
				err := os.MkdirAll(infraDir, 0755)
				require.NoError(t, err)

				mainBicepPath := filepath.Join(infraDir, "main.bicep")
				err = os.WriteFile(mainBicepPath, []byte("// bicep content"), osutil.PermissionFileOwnerOnly)
				require.NoError(t, err)

				return &project.ProjectConfig{
					Path: tempDir,
					Infra: provisioning.Options{
						Path:   "infra",
						Module: "main",
					},
					Resources: nil,
				}
			},
			expectError: true,
		},
		{
			name: "infra folder with custom module name but no resources",
			setupFunc: func(t *testing.T) *project.ProjectConfig {
				// Create temp project directory
				tempDir := t.TempDir()

				// Create infra directory and custom.bicep file
				infraDir := filepath.Join(tempDir, "infra")
				err := os.MkdirAll(infraDir, 0755)
				require.NoError(t, err)

				customBicepPath := filepath.Join(infraDir, "custom.bicep")
				err = os.WriteFile(customBicepPath, []byte("// bicep content"), osutil.PermissionFileOwnerOnly)
				require.NoError(t, err)

				return &project.ProjectConfig{
					Path: tempDir,
					Infra: provisioning.Options{
						Path:   "infra",
						Module: "custom",
					},
					Resources: map[string]*project.ResourceConfig{},
				}
			},
			expectError: true,
		},
		{
			name: "terraform module files",
			setupFunc: func(t *testing.T) *project.ProjectConfig {
				// Create temp project directory
				tempDir := t.TempDir()

				// Create infra directory and main.tf file
				infraDir := filepath.Join(tempDir, "infra")
				err := os.MkdirAll(infraDir, 0755)
				require.NoError(t, err)

				mainTfPath := filepath.Join(infraDir, "main.tf")
				err = os.WriteFile(mainTfPath, []byte("// terraform content"), osutil.PermissionFileOwnerOnly)
				require.NoError(t, err)

				return &project.ProjectConfig{
					Path: tempDir,
					Infra: provisioning.Options{
						Path:   "infra",
						Module: "main",
					},
					Resources: map[string]*project.ResourceConfig{},
				}
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			prjConfig := tt.setupFunc(t)

			// Create a mock ImportManager with minimal setup
			// For this test, we don't need the ImportManager to do anything special
			// as the ensureCompatibleProject function primarily checks infra compatibility
			importManager := project.NewImportManager(project.NewDotNetImporter(nil, nil, nil, nil, nil))

			err := ensureCompatibleProject(ctx, importManager, prjConfig)

			if tt.expectError {
				require.Error(t, err)
				require.Contains(t, err.Error(), "incompatible project:")
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestPathHasInfraModule(t *testing.T) {
	tests := []struct {
		name           string
		setupFunc      func(t *testing.T) (string, string)
		expectedResult bool
		expectError    bool
	}{
		{
			name: "existing bicep file",
			setupFunc: func(t *testing.T) (string, string) {
				tempDir := t.TempDir()
				mainBicepPath := filepath.Join(tempDir, "main.bicep")
				err := os.WriteFile(mainBicepPath, []byte("// bicep content"), osutil.PermissionFileOwnerOnly)
				require.NoError(t, err)
				return tempDir, "main"
			},
			expectedResult: true,
			expectError:    false,
		},
		{
			name: "existing terraform file",
			setupFunc: func(t *testing.T) (string, string) {
				tempDir := t.TempDir()
				mainTfPath := filepath.Join(tempDir, "main.tf")
				err := os.WriteFile(mainTfPath, []byte("// terraform content"), osutil.PermissionFileOwnerOnly)
				require.NoError(t, err)
				return tempDir, "main"
			},
			expectedResult: true,
			expectError:    false,
		},
		{
			name: "existing bicepparam file",
			setupFunc: func(t *testing.T) (string, string) {
				tempDir := t.TempDir()
				mainParamPath := filepath.Join(tempDir, "main.bicepparam")
				err := os.WriteFile(mainParamPath, []byte("// bicepparam content"), osutil.PermissionFileOwnerOnly)
				require.NoError(t, err)
				return tempDir, "main"
			},
			expectedResult: true,
			expectError:    false,
		},
		{
			name: "non-existing module file",
			setupFunc: func(t *testing.T) (string, string) {
				tempDir := t.TempDir()
				return tempDir, "main"
			},
			expectedResult: false,
			expectError:    false,
		},
		{
			name: "directory doesn't exist",
			setupFunc: func(t *testing.T) (string, string) {
				tempDir := t.TempDir()
				nonExistentDir := filepath.Join(tempDir, "non-existent")
				return nonExistentDir, "main"
			},
			expectedResult: false,
			expectError:    true,
		},
		{
			name: "directory with same name as module",
			setupFunc: func(t *testing.T) (string, string) {
				tempDir := t.TempDir()
				// Create a directory with the same name as the module
				moduleDir := filepath.Join(tempDir, "main")
				err := os.MkdirAll(moduleDir, 0755)
				require.NoError(t, err)
				return tempDir, "main"
			},
			expectedResult: false,
			expectError:    false,
		},
		{
			name: "custom module name",
			setupFunc: func(t *testing.T) (string, string) {
				tempDir := t.TempDir()
				customBicepPath := filepath.Join(tempDir, "custom.bicep")
				err := os.WriteFile(customBicepPath, []byte("// bicep content"), osutil.PermissionFileOwnerOnly)
				require.NoError(t, err)
				return tempDir, "custom"
			},
			expectedResult: true,
			expectError:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, module := tt.setupFunc(t)

			result, err := pathHasInfraModule(path, module)

			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expectedResult, result)
			}
		})
	}
}

func TestNonInteractiveMode(t *testing.T) {
	tests := []struct {
		name        string
		flags       *addFlags
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid ai.project type",
			flags: &addFlags{
				resourceType: "ai.project",
				name:         "test-ai",
				global:       &internal.GlobalCommandOptions{NoPrompt: true},
			},
			expectError: false,
		},
		{
			name: "missing type",
			flags: &addFlags{
				name:   "test-resource",
				global: &internal.GlobalCommandOptions{NoPrompt: true},
			},
			expectError: true,
			errorMsg:    "--type is required when using non-interactive mode",
		},
		{
			name: "missing name",
			flags: &addFlags{
				resourceType: "ai.project",
				global:       &internal.GlobalCommandOptions{NoPrompt: true},
			},
			expectError: true,
			errorMsg:    "--name is required when using non-interactive mode",
		},
		{
			name: "invalid resource type",
			flags: &addFlags{
				resourceType: "invalid.type",
				name:         "test-resource",
				global:       &internal.GlobalCommandOptions{NoPrompt: true},
			},
			expectError: true,
			errorMsg:    "invalid resource type: invalid.type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			prjConfig := &project.ProjectConfig{
				Resources: map[string]*project.ResourceConfig{},
			}

			// Create a minimal AddAction with the test flags
			action := &AddAction{
				flags: tt.flags,
			}

			resourceToAdd, serviceToAdd, err := action.handleNonInteractiveMode(ctx, prjConfig)

			if tt.expectError {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errorMsg)
				require.Nil(t, resourceToAdd)
				require.Nil(t, serviceToAdd)
			} else {
				require.NoError(t, err)
				require.NotNil(t, resourceToAdd)
				require.Equal(t, tt.flags.name, resourceToAdd.Name)
				require.Equal(t, project.ResourceType(tt.flags.resourceType), resourceToAdd.Type)
			}
		})
	}
}

func TestIsValidResourceType(t *testing.T) {
	action := &AddAction{}

	validTypes := []project.ResourceType{
		project.ResourceTypeAiProject,
		project.ResourceTypeStorage,
		project.ResourceTypeKeyVault,
	}

	for _, resourceType := range validTypes {
		t.Run(string(resourceType), func(t *testing.T) {
			result := action.isValidResourceType(resourceType)
			require.True(t, result)
		})
	}

	// Test invalid type
	t.Run("invalid_type", func(t *testing.T) {
		result := action.isValidResourceType("invalid.type")
		require.False(t, result)
	})
}
