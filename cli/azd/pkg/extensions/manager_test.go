// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_ValidateChecksum_Success_SHA256(t *testing.T) {
	// Create a temporary file with known content
	content := []byte("test data")
	tempFile, err := os.CreateTemp(t.TempDir(), "testfile")
	require.NoError(t, err)
	defer os.Remove(tempFile.Name())

	_, err = tempFile.Write(content)
	require.NoError(t, err)
	tempFile.Close()

	// Compute the expected checksum
	hash := sha256.Sum256(content)
	expectedChecksum := hex.EncodeToString(hash[:])

	// Create the checksum struct
	checksum := ExtensionChecksum{
		Algorithm: "sha256",
		Value:     expectedChecksum,
	}

	// Validate the checksum
	err = validateChecksum(tempFile.Name(), checksum)
	require.NoError(t, err)
}

func Test_ValidateChecksum_Success_SHA512(t *testing.T) {
	// Create a temporary file with known content
	content := []byte("test data")
	tempFile, err := os.CreateTemp(t.TempDir(), "testfile")
	require.NoError(t, err)
	defer os.Remove(tempFile.Name())

	_, err = tempFile.Write(content)
	require.NoError(t, err)
	tempFile.Close()

	// Compute the expected checksum
	hash := sha512.Sum512(content)
	expectedChecksum := hex.EncodeToString(hash[:])

	// Create the checksum struct
	checksum := ExtensionChecksum{
		Algorithm: "sha512",
		Value:     expectedChecksum,
	}

	// Validate the checksum
	err = validateChecksum(tempFile.Name(), checksum)
	require.NoError(t, err)
}

func Test_ValidateChecksum_Failure_InvalidAlgorithm(t *testing.T) {
	// Create a temporary file with known content
	content := []byte("test data")
	tempFile, err := os.CreateTemp(t.TempDir(), "testfile")
	require.NoError(t, err)
	defer os.Remove(tempFile.Name())

	_, err = tempFile.Write(content)
	require.NoError(t, err)
	tempFile.Close()

	// Create the checksum struct with an invalid algorithm
	checksum := ExtensionChecksum{
		Algorithm: "invalid",
		Value:     "dummychecksum",
	}

	// Validate the checksum
	err = validateChecksum(tempFile.Name(), checksum)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported checksum algorithm")
}

func Test_ValidateChecksum_Failure_ChecksumMismatch(t *testing.T) {
	// Create a temporary file with known content
	content := []byte("test data")
	tempFile, err := os.CreateTemp(t.TempDir(), "testfile")
	require.NoError(t, err)
	defer os.Remove(tempFile.Name())

	_, err = tempFile.Write(content)
	require.NoError(t, err)
	tempFile.Close()

	// Create the checksum struct with an incorrect checksum value
	checksum := ExtensionChecksum{
		Algorithm: "sha256",
		Value:     "incorrectchecksum",
	}

	// Validate the checksum
	err = validateChecksum(tempFile.Name(), checksum)
	require.Error(t, err)
	require.Contains(t, err.Error(), "checksum mismatch")
}

func Test_ValidateChecksum_Failure_InvalidChecksumData(t *testing.T) {
	// Create a temporary file with known content
	content := []byte("test data")
	tempFile, err := os.CreateTemp(t.TempDir(), "testfile")
	require.NoError(t, err)
	defer os.Remove(tempFile.Name())

	_, err = tempFile.Write(content)
	require.NoError(t, err)
	tempFile.Close()

	// Create the checksum struct with missing algorithm and value
	checksum := ExtensionChecksum{
		Algorithm: "",
		Value:     "",
	}

	// Validate the checksum
	err = validateChecksum(tempFile.Name(), checksum)

	// Empty checksum skips verification
	require.NoError(t, err)
}

func Test_List_Install_Uninstall_Flow(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	createRegistryMocks(mockContext)

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	// List installed extensions (expect 0)
	installed, err := manager.ListInstalled()
	require.NoError(t, err)
	require.NotNil(t, installed)
	require.Equal(t, 0, len(installed))

	// List extensions from the registry (expect at least 1)
	extensions, err := manager.FindExtensions(*mockContext.Context, nil)
	require.NoError(t, err)
	require.NotNil(t, extensions)
	require.Greater(t, len(extensions), 0)

	// Install the first extension
	extensionVersion, err := manager.Install(*mockContext.Context, extensions[0], "")
	require.NoError(t, err)
	require.NotNil(t, extensionVersion)

	// List installed extensions (expect 1)
	installed, err = manager.ListInstalled()
	require.NoError(t, err)
	require.NotNil(t, installed)
	require.Greater(t, len(installed), 0)

	// Uninstall the first extension
	err = manager.Uninstall(extensions[0].Id)
	require.NoError(t, err)

	// List installed extensions (expect 0)
	installed, err = manager.ListInstalled()
	require.NoError(t, err)
	require.NotNil(t, installed)
	require.Equal(t, 0, len(installed))
}

func Test_Install_With_SemverConstraints(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	createRegistryMocks(mockContext)

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	// Generate a list of tests cases to validate the semver constraints of the Install function.
	testCases := []struct {
		Constraint string
		Expected   string
	}{
		{
			Constraint: "latest",
			Expected:   "3.1.0",
		},
		{
			Constraint: "*",
			Expected:   "3.1.0",
		},
		{
			Constraint: "2.x",
			Expected:   "2.1.1",
		},
		{
			Constraint: "1.0.0",
			Expected:   "1.0.0",
		},
		{
			Constraint: "=2.1.1",
			Expected:   "2.1.1",
		},
		{
			Constraint: ">=1.0.0",
			Expected:   "3.1.0",
		},
		{
			Constraint: ">=1.0.0 <2.0.0",
			Expected:   "1.3.0",
		},
		{
			Constraint: ">=1.0.0 <1.2.0",
			Expected:   "1.1.0",
		},
		{
			Constraint: ">=1.0.0 <1.1.0",
			Expected:   "1.0.0",
		},
		{
			Constraint: ">=1.0.0 <2.0.0",
			Expected:   "1.3.0",
		},
		{
			Constraint: ">=1.0.0 <1.0.0 || >=2.0.0 <3.0.0",
			Expected:   "2.1.1",
		},
		{
			Constraint: "~2.1.0",
			Expected:   "2.1.1",
		},
		{
			Constraint: "^1.0.0",
			Expected:   "1.3.0",
		},
		{
			Constraint: "^1.1.0",
			Expected:   "1.3.0",
		},
		{
			Constraint: "invalid",
			Expected:   "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Constraint, func(t *testing.T) {
			// Find the extension first
			extensions, err := manager.FindExtensions(*mockContext.Context, &FilterOptions{Id: "test.extension"})
			require.NoError(t, err)
			require.Len(t, extensions, 1)

			extensionVersion, err := manager.Install(*mockContext.Context, extensions[0], tc.Constraint)
			if tc.Expected == "" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.NotNil(t, extensionVersion)
				require.Equal(t, tc.Expected, extensionVersion.Version)

				err = manager.Uninstall("test.extension")
				require.NoError(t, err)
			}
		})
	}
}

func Test_DownloadArtifact_Remote(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	// Mock the HTTP client to simulate a remote download
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.URL.String() == "https://example.com/artifact.zip"
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, []byte("artifact content"))
	})

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	tempFilePath, err := manager.downloadArtifact(*mockContext.Context, "https://example.com/artifact.zip")
	require.NoError(t, err)
	require.FileExists(t, tempFilePath)

	// Clean up the temp file
	defer os.Remove(tempFilePath)
}

func Test_DownloadArtifact_Local(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	// Create a temporary file to simulate a local artifact
	content := []byte("local artifact content")
	tempFile, err := os.CreateTemp(t.TempDir(), "artifact")
	require.NoError(t, err)
	defer os.Remove(tempFile.Name())

	_, err = tempFile.Write(content)
	require.NoError(t, err)
	tempFile.Close()

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	tempFilePath, err := manager.downloadArtifact(*mockContext.Context, tempFile.Name())
	require.NoError(t, err)
	require.FileExists(t, tempFilePath)

	// Clean up the temp file
	defer os.Remove(tempFilePath)
}

func Test_DownloadArtifact_Local_Error(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	// Provide an invalid local file path
	invalidFilePath := "non-existent-file.txt"

	tempFilePath, err := manager.downloadArtifact(*mockContext.Context, invalidFilePath)
	require.Error(t, err)
	require.Contains(t, err.Error(), "file does not exist at path")
	require.Empty(t, tempFilePath)
}

func Test_DownloadArtifact_Remote_Error(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	// Mock the HTTP client to simulate a failed remote download
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.URL.String() == "https://example.com/invalid-artifact.zip"
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateEmptyHttpResponse(request, http.StatusNotFound)
	})

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	tempFilePath, err := manager.downloadArtifact(*mockContext.Context, "https://example.com/invalid-artifact.zip")
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to download file")
	require.Empty(t, tempFilePath)
}

func Test_ValidateChecksum_Error_InvalidFile(t *testing.T) {
	// Create a non-existent file path
	nonExistentFilePath := "non-existent-file.txt"

	// Create the checksum struct
	checksum := ExtensionChecksum{
		Algorithm: "sha256",
		Value:     "dummychecksum",
	}

	// Validate the checksum
	err := validateChecksum(nonExistentFilePath, checksum)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to open file for checksum validation")
}

func Test_ValidateChecksum_Error_UnsupportedAlgorithm(t *testing.T) {
	// Create a temporary file with known content
	content := []byte("test data")
	tempFile, err := os.CreateTemp(t.TempDir(), "testfile")
	require.NoError(t, err)
	defer os.Remove(tempFile.Name())

	_, err = tempFile.Write(content)
	require.NoError(t, err)
	tempFile.Close()

	// Create the checksum struct with an unsupported algorithm
	checksum := ExtensionChecksum{
		Algorithm: "unsupported",
		Value:     "dummychecksum",
	}

	// Validate the checksum
	err = validateChecksum(tempFile.Name(), checksum)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported checksum algorithm")
}

func createRegistryMocks(mockContext *mocks.MockContext) {
	// Create a mock source
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.URL.String() == extensionRegistryUrl
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, testRegistry)
	})

	// Return mock file for any extension artifact download
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return strings.HasPrefix(request.URL.String(), "https://aka.ms/azd/extensions/registry/")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, []byte("test data"))
	})
}

var sampleArtifacts = map[string]ExtensionArtifact{
	"darwin": {
		//nolint:lll
		URL: "https://aka.ms/azd/extensions/registry/test.extension/azd-ext-test-darwin-amd64",
		AdditionalMetadata: map[string]any{
			"entryPoint": "azd-ext-test-darwin-amd64",
		},
	},
	"windows": {
		//nolint:lll
		URL: "https://aka.ms/azd/extensions/registry/test.extension/azd-ext-test-windows-amd64.exe",
		AdditionalMetadata: map[string]any{
			"entryPoint": "azd-ext-test-windows-amd64.exe",
		},
	},
	"linux": {
		//nolint:lll
		URL: "https://aka.ms/azd/extensions/registry/test.extension/azd-ext-test-linux-amd64",
		AdditionalMetadata: map[string]any{
			"entryPoint": "azd-ext-test-linux-amd64",
		},
	},
}

var testRegistry = Registry{
	Extensions: []*ExtensionMetadata{
		{
			Id:          "test.extension",
			Namespace:   "test",
			DisplayName: "Test Extension",
			Description: "Test extension description",
			Tags:        []string{"test"},
			Versions: []ExtensionVersion{
				{
					Version:   "1.0.0",
					Artifacts: sampleArtifacts,
				},
				{
					Version:   "1.1.0",
					Artifacts: sampleArtifacts,
				},
				{
					Version:   "1.2.0",
					Artifacts: sampleArtifacts,
				},
				{
					Version:   "1.3.0",
					Artifacts: sampleArtifacts,
				},
				{
					Version:   "2.0.0",
					Artifacts: sampleArtifacts,
				},
				{
					Version:   "2.1.0",
					Artifacts: sampleArtifacts,
				},
				{
					Version:   "2.1.1",
					Artifacts: sampleArtifacts,
				},
				{
					Version:   "3.0.0",
					Artifacts: sampleArtifacts,
				},
				{
					Version:   "3.1.0",
					Artifacts: sampleArtifacts,
				},
			},
		},
		{
			Id:          "test.mcp.extension",
			Namespace:   "test.mcp",
			DisplayName: "Test MCP Extension",
			Description: "Test extension with MCP configuration",
			Tags:        []string{"test", "mcp"},
			Versions: []ExtensionVersion{
				{
					Version:      "1.0.0",
					Artifacts:    sampleArtifacts,
					Capabilities: []CapabilityType{McpServerCapability},
					McpConfig: &McpConfig{
						Server: McpServerConfig{
							Args: []string{"custom", "mcp", "start"},
							Env:  []string{"CUSTOM_VAR=test", "DEBUG=${HOME}/debug"},
						},
					},
				},
			},
		},
		{
			Id:          "azure.containerapp",
			Namespace:   "azure",
			DisplayName: "Azure Container Apps Extension",
			Description: "Extension for deploying to Azure Container Apps",
			Tags:        []string{"azure", "containerapp", "service"},
			Versions: []ExtensionVersion{
				{
					Version:      "1.0.0",
					Artifacts:    sampleArtifacts,
					Capabilities: []CapabilityType{ServiceTargetProviderCapability},
					Providers: []Provider{
						{
							Name:        "containerapp",
							Type:        ServiceTargetProviderType,
							Description: "Deploys to Azure Container Apps",
						},
					},
				},
			},
		},
		{
			Id:          "kubernetes.deploy",
			Namespace:   "kubernetes",
			DisplayName: "Kubernetes Deployment Extension",
			Description: "Extension with Kubernetes deployment and MCP capabilities",
			Tags:        []string{"kubernetes", "multi", "service", "mcp"},
			Versions: []ExtensionVersion{
				{
					Version:      "1.0.0",
					Artifacts:    sampleArtifacts,
					Capabilities: []CapabilityType{ServiceTargetProviderCapability, McpServerCapability},
					Providers: []Provider{
						{
							Name:        "kubernetes",
							Type:        ServiceTargetProviderType,
							Description: "Deploys to Kubernetes",
						},
					},
				},
			},
		},
		{
			Id:          "foundry.multi.target",
			Namespace:   "foundry",
			DisplayName: "Multi-Target Foundry Extension",
			Description: "Extension supporting multiple deployment targets",
			Tags:        []string{"foundry", "multi", "providers"},
			Versions: []ExtensionVersion{
				{
					Version:      "1.0.0",
					Artifacts:    sampleArtifacts,
					Capabilities: []CapabilityType{ServiceTargetProviderCapability},
					Providers: []Provider{
						{
							Name:        "azure.ai.agents",
							Type:        ServiceTargetProviderType,
							Description: "Deploys to Microsoft Foundry hosted agents",
						},
						{
							Name:        "containerapp",
							Type:        ServiceTargetProviderType,
							Description: "Deploys to Azure Container Apps",
						},
					},
				},
			},
		},
	},
}

func Test_FindArtifactForCurrentOS_ErrorMessage_Format(t *testing.T) {
	// Create a version with artifacts that don't match current OS/architecture
	// For this test, we'll use artificial platform names that definitely won't match
	version := &ExtensionVersion{
		Artifacts: map[string]ExtensionArtifact{
			"fakeos": {
				URL: "https://example.com/fakeos-binary",
			},
			"anotherfakeos/fakearch": {
				URL: "https://example.com/anotherfakeos-binary",
			},
		},
	}

	artifact, err := findArtifactForCurrentOS(version)

	require.Error(t, err)
	require.Nil(t, artifact)
	require.Contains(t, err.Error(), "no artifact available for platform:")
}

func Test_FindExtensions_MultipleMatches_ErrorHandling(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)

	// Mock two different registries that both contain the same extension ID
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.URL.String() == extensionRegistryUrl
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		// Return a registry with an extension
		registry := Registry{
			Extensions: []*ExtensionMetadata{
				{
					Id:          "duplicate.extension",
					Namespace:   "duplicate",
					DisplayName: "Duplicate Extension from Registry",
					Description: "Extension from first source",
					Source:      "default", // This will be overwritten by the source
					Tags:        []string{"test"},
					Versions: []ExtensionVersion{
						{
							Version:   "1.0.0",
							Artifacts: sampleArtifacts,
						},
					},
				},
			},
		}
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, registry)
	})

	// Create a manager and mock two sources
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	// Create mock sources that will return the same extension
	mockSource1 := &mockSource{
		name: "source1",
		extensions: []*ExtensionMetadata{
			{
				Id:          "duplicate.extension",
				Namespace:   "duplicate",
				DisplayName: "Extension from Source 1",
				Source:      "source1",
			},
		},
	}

	mockSource2 := &mockSource{
		name: "source2",
		extensions: []*ExtensionMetadata{
			{
				Id:          "duplicate.extension",
				Namespace:   "duplicate",
				DisplayName: "Extension from Source 2",
				Source:      "source2",
			},
		},
	}

	// Override the sources with our mocks
	manager.sources = []Source{mockSource1, mockSource2}

	// Try to find the extension - should return multiple matches
	extensions, err := manager.FindExtensions(*mockContext.Context, &FilterOptions{Id: "duplicate.extension"})

	// Verify we got multiple matches (this is expected behavior for FindExtensions)
	require.NoError(t, err)
	require.Len(t, extensions, 2)

	// Verify both sources are represented
	sourceNames := make(map[string]bool)
	for _, ext := range extensions {
		sourceNames[ext.Source] = true
	}
	require.True(t, sourceNames["source1"])
	require.True(t, sourceNames["source2"])
}

// mockSource is a test implementation of the Source interface
type mockSource struct {
	name       string
	extensions []*ExtensionMetadata
}

func (m *mockSource) Name() string {
	return m.name
}

func (m *mockSource) ListExtensions(ctx context.Context) ([]*ExtensionMetadata, error) {
	return m.extensions, nil
}

func (m *mockSource) GetExtension(ctx context.Context, extensionId string) (*ExtensionMetadata, error) {
	for _, ext := range m.extensions {
		if ext.Id == extensionId {
			return ext, nil
		}
	}
	return nil, nil
}

func Test_Install_WithMcpConfig(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	// Use the existing registry mock setup
	createRegistryMocks(mockContext)

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	// Install extension with MCP configuration
	extensions, err := manager.FindExtensions(*mockContext.Context, &FilterOptions{Id: "test.mcp.extension"})
	require.NoError(t, err)
	require.Len(t, extensions, 1)

	extensionVersion, err := manager.Install(*mockContext.Context, extensions[0], "")
	require.NoError(t, err)
	require.NotNil(t, extensionVersion)

	// Verify the extension was installed
	installed, err := manager.ListInstalled()
	require.NoError(t, err)
	require.Equal(t, 1, len(installed))

	// Get the installed extension
	installedExtension, exists := installed["test.mcp.extension"]
	require.True(t, exists)
	require.NotNil(t, installedExtension)

	// Verify McpConfig was preserved during installation
	require.NotNil(t, installedExtension.McpConfig, "McpConfig should be preserved during installation")
	require.NotNil(t, installedExtension.McpConfig.Server, "McpServerConfig should be preserved")
	require.Equal(t, []string{"custom", "mcp", "start"}, installedExtension.McpConfig.Server.Args)
	require.Equal(t, []string{"CUSTOM_VAR=test", "DEBUG=${HOME}/debug"}, installedExtension.McpConfig.Server.Env)

	// Verify the extension has MCP server capability
	require.True(t, installedExtension.HasCapability(McpServerCapability))
}

// Helper function to convert extension slice to ID set
func extensionIdsToSet(extensions []*ExtensionMetadata) map[string]bool {
	ids := make(map[string]bool)
	for _, ext := range extensions {
		ids[ext.Id] = true
	}
	return ids
}

// Helper function to assert extension IDs match expectations
func assertExtensionIds(t *testing.T, extensions []*ExtensionMetadata, expectedIds []string, unexpectedIds []string) {
	t.Helper()
	ids := extensionIdsToSet(extensions)

	for _, expectedId := range expectedIds {
		require.True(t, ids[expectedId], "Expected to find extension: %s", expectedId)
	}

	for _, unexpectedId := range unexpectedIds {
		require.False(t, ids[unexpectedId], "Expected NOT to find extension: %s", unexpectedId)
	}
}

// Test_FilterExtensions_ByCapabilityAndProvider tests the capability and provider filtering functionality
func Test_FilterExtensions_ByCapabilityAndProvider(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	createRegistryMocks(mockContext)

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	t.Run("filter by service-target-provider capability", func(t *testing.T) {
		extensions, err := manager.FindExtensions(context.Background(), &FilterOptions{
			Capability: ServiceTargetProviderCapability,
		})
		require.NoError(t, err)
		require.Len(t, extensions, 3, "Should find 3 extensions with service-target-provider capability")

		assertExtensionIds(t, extensions,
			[]string{"azure.containerapp", "kubernetes.deploy", "foundry.multi.target"},
			[]string{"test.mcp.extension"})
	})

	t.Run("filter by MCP capability", func(t *testing.T) {
		extensions, err := manager.FindExtensions(context.Background(), &FilterOptions{
			Capability: McpServerCapability,
		})
		require.NoError(t, err)
		require.Len(t, extensions, 2, "Should find 2 extensions with MCP capability")

		assertExtensionIds(t, extensions,
			[]string{"test.mcp.extension", "kubernetes.deploy"},
			[]string{"azure.containerapp", "foundry.multi.target"})
	})

	t.Run("find extension with containerapp provider", func(t *testing.T) {
		extensions, err := manager.FindExtensions(context.Background(), &FilterOptions{
			Provider: "containerapp",
		})
		require.NoError(t, err)
		require.Len(t, extensions, 2, "Should find exactly 2 extensions with containerapp provider")

		assertExtensionIds(t, extensions,
			[]string{"azure.containerapp", "foundry.multi.target"},
			[]string{"test.mcp.extension", "kubernetes.deploy"})
	})

	t.Run("find extension with kubernetes provider", func(t *testing.T) {
		extensions, err := manager.FindExtensions(context.Background(), &FilterOptions{
			Provider: "kubernetes",
		})
		require.NoError(t, err)
		require.Len(t, extensions, 1, "Should find exactly 1 extension with kubernetes provider")

		assertExtensionIds(t, extensions,
			[]string{"kubernetes.deploy"},
			[]string{"azure.containerapp", "test.mcp.extension", "foundry.multi.target"})
	})

	t.Run("find extension with azure.ai.agents provider", func(t *testing.T) {
		extensions, err := manager.FindExtensions(context.Background(), &FilterOptions{
			Provider: "azure.ai.agents",
		})
		require.NoError(t, err)
		require.Len(t, extensions, 1, "Should find exactly 1 extension with azure.ai.agents provider")

		assertExtensionIds(t, extensions,
			[]string{"foundry.multi.target"},
			[]string{"azure.containerapp", "test.mcp.extension", "kubernetes.deploy"})
	})

	t.Run("find service target extension for containerapp", func(t *testing.T) {
		extensions, err := manager.FindExtensions(context.Background(), &FilterOptions{
			Capability: ServiceTargetProviderCapability,
			Provider:   "containerapp",
		})
		require.NoError(t, err)
		require.Len(t, extensions, 2, "Should find exactly 2 extensions")

		assertExtensionIds(t, extensions,
			[]string{"azure.containerapp", "foundry.multi.target"},
			[]string{"test.mcp.extension", "kubernetes.deploy"})
	})

	t.Run("find service target extension for kubernetes", func(t *testing.T) {
		extensions, err := manager.FindExtensions(context.Background(), &FilterOptions{
			Capability: ServiceTargetProviderCapability,
			Provider:   "kubernetes",
		})
		require.NoError(t, err)
		require.Len(t, extensions, 1, "Should find exactly 1 extension")

		assertExtensionIds(t, extensions,
			[]string{"kubernetes.deploy"},
			[]string{"azure.containerapp", "test.mcp.extension", "foundry.multi.target"})
	})

	t.Run("case-insensitive provider matching", func(t *testing.T) {
		extensions, err := manager.FindExtensions(context.Background(), &FilterOptions{
			Provider: "KUBERNETES",
		})
		require.NoError(t, err)
		require.Len(t, extensions, 1, "Should find extension with case-insensitive provider matching")

		assertExtensionIds(t, extensions,
			[]string{"kubernetes.deploy"},
			[]string{"azure.containerapp", "test.mcp.extension", "foundry.multi.target"})
	})

	t.Run("filter with no matches", func(t *testing.T) {
		extensions, err := manager.FindExtensions(context.Background(), &FilterOptions{
			Provider: "nonexistent-provider",
		})
		require.NoError(t, err)
		require.Len(t, extensions, 0, "Should find no extensions with nonexistent provider")
	})

	t.Run("combine capability and tag filters", func(t *testing.T) {
		extensions, err := manager.FindExtensions(context.Background(), &FilterOptions{
			Capability: ServiceTargetProviderCapability,
			Tags:       []string{"multi"},
		})
		require.NoError(t, err)
		require.Len(t, extensions, 2, "Should find extensions with both capability and tag filters")

		assertExtensionIds(t, extensions,
			[]string{"kubernetes.deploy", "foundry.multi.target"},
			[]string{"azure.containerapp", "test.mcp.extension"})
	})

	t.Run("invalid capability and provider combination", func(t *testing.T) {
		extensions, err := manager.FindExtensions(context.Background(), &FilterOptions{
			Capability: McpServerCapability,
			Provider:   "containerapp",
		})
		require.NoError(t, err)
		require.Len(t, extensions, 0, "Should find no extensions with MCP capability AND containerapp provider")
	})
}

func Test_FetchAndCacheMetadata(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	createRegistryMocks(mockContext)

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)

	// Create a temporary directory for test extensions
	tempDir := t.TempDir()
	extDir := filepath.Join(tempDir, "extensions", "test.metadata.extension")
	err := os.MkdirAll(extDir, os.ModePerm)
	require.NoError(t, err)

	// Create a dummy extension binary
	extBinary := filepath.Join(extDir, "test-ext.exe")
	err = os.WriteFile(extBinary, []byte("fake binary"), 0600)
	require.NoError(t, err)

	// Get relative path from temp dir
	relPath, err := filepath.Rel(tempDir, extBinary)
	require.NoError(t, err)

	// Override user config directory for this test
	t.Setenv("AZD_CONFIG_DIR", tempDir)

	// Create a mock extension that supports metadata capability
	extension := &Extension{
		Id:           "test.metadata.extension",
		Namespace:    "test",
		DisplayName:  "Test Metadata Extension",
		Version:      "1.0.0",
		Path:         relPath,
		Capabilities: []CapabilityType{MetadataCapability},
	}

	// Mock the runner to return metadata JSON
	mockMetadata := ExtensionCommandMetadata{
		SchemaVersion: "1.0",
		ID:            "test.metadata.extension",
		Commands: []Command{
			{
				Name:  []string{"test"},
				Short: "Test command",
				Usage: "azd test",
			},
		},
	}

	metadataJSON, err := json.Marshal(mockMetadata)
	require.NoError(t, err)

	// Mock CommandRunner to return metadata JSON
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "metadata")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.RunResult{
			Stdout:   string(metadataJSON),
			ExitCode: 0,
		}, nil
	})

	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	t.Run("fetch metadata for extension with metadata capability", func(t *testing.T) {
		// Install extension first (simulate by adding to config)
		extensions, err := manager.ListInstalled()
		require.NoError(t, err)
		extensions[extension.Id] = extension
		err = manager.userConfig.Set(installedConfigKey, extensions)
		require.NoError(t, err)

		// Fetch and cache metadata
		err = manager.fetchAndCacheMetadata(*mockContext.Context, extension)
		require.NoError(t, err)

		// Verify metadata exists
		exists := manager.MetadataExists(extension.Id)
		require.True(t, exists, "Metadata should exist after fetch")

		// Load and verify metadata
		loadedMetadata, err := manager.LoadMetadata(extension.Id)
		require.NoError(t, err)
		require.NotNil(t, loadedMetadata)
		require.Equal(t, mockMetadata.ID, loadedMetadata.ID)
		require.Len(t, loadedMetadata.Commands, 1)
		require.Equal(t, "test", loadedMetadata.Commands[0].Name[0])
	})

	t.Run("caller should check capability before calling fetchAndCacheMetadata", func(t *testing.T) {
		extensionNoMetadata := &Extension{
			Id:           "test.no.metadata",
			Namespace:    "test",
			DisplayName:  "Test No Metadata Extension",
			Version:      "1.0.0",
			Path:         "extensions/test.no.metadata/test-ext.exe",
			Capabilities: []CapabilityType{}, // No metadata capability
		}

		// Caller should check capability before calling
		hasCapability := extensionNoMetadata.HasCapability(MetadataCapability)
		require.False(t, hasCapability, "Extension should not have metadata capability")

		// Metadata should not exist
		exists := manager.MetadataExists(extensionNoMetadata.Id)
		require.False(t, exists, "Metadata should not exist for extension without capability")
	})

	t.Run("delete metadata", func(t *testing.T) {
		// Ensure metadata exists first
		exists := manager.MetadataExists(extension.Id)
		require.True(t, exists)

		// Delete metadata
		err := manager.DeleteMetadata(extension.Id)
		require.NoError(t, err)

		// Verify metadata no longer exists
		exists = manager.MetadataExists(extension.Id)
		require.False(t, exists, "Metadata should not exist after deletion")
	})

	t.Run("load non-existent metadata returns error", func(t *testing.T) {
		_, err := manager.LoadMetadata("non.existent.extension")
		require.Error(t, err)
		require.Contains(t, err.Error(), "metadata not found")
	})

	t.Run("metadata command timeout does not fail operation", func(t *testing.T) {
		timeoutExtension := &Extension{
			Id:           "test.timeout.extension",
			Namespace:    "test",
			DisplayName:  "Test Timeout Extension",
			Version:      "1.0.0",
			Path:         relPath,
			Capabilities: []CapabilityType{MetadataCapability},
		}

		// Create extension directory
		timeoutExtDir := filepath.Join(tempDir, "extensions", timeoutExtension.Id)
		err := os.MkdirAll(timeoutExtDir, os.ModePerm)
		require.NoError(t, err)

		// Create a new mock context with isolated command runner
		timeoutMockContext := mocks.NewMockContext(context.Background())

		// Mock CommandRunner to simulate timeout for ANY metadata command
		timeoutMockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			// Match any command that has "metadata" as an argument
			for _, arg := range args.Args {
				if arg == "metadata" {
					return true
				}
			}
			return false
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.RunResult{}, context.DeadlineExceeded
		})

		// Create a new runner with the timeout mock context
		timeoutLazyRunner := lazy.NewLazy(func() (*Runner, error) {
			return NewRunner(timeoutMockContext.CommandRunner), nil
		})

		timeoutManager, err := NewManager(userConfigManager, sourceManager, timeoutLazyRunner, mockContext.HttpClient)
		require.NoError(t, err)

		// Fetch and cache metadata - should return error for timeout
		err = timeoutManager.fetchAndCacheMetadata(*mockContext.Context, timeoutExtension)
		require.Error(t, err, "Should return an error for timeout")
		require.Contains(t, err.Error(), "timed out", "Error should mention timeout")

		// Metadata should not exist since command timed out
		exists := timeoutManager.MetadataExists(timeoutExtension.Id)
		require.False(t, exists, "Metadata should not exist when command times out")

		// This demonstrates that installation would still succeed despite timeout
		// The actual Install() function logs this as a warning but doesn't fail
	})
}
