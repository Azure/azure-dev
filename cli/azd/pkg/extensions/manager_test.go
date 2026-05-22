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
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
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
	mockContext := mocks.NewMockContext(t.Context())

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
	mockContext := mocks.NewMockContext(t.Context())

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

func Test_MatchesVersionConstraint(t *testing.T) {
	testCases := []struct {
		Name      string
		Expr      string
		Candidate string
		Want      bool
	}{
		{Name: "empty matches any", Expr: "", Candidate: "0.1.0", Want: true},
		{Name: "latest matches any", Expr: "latest", Candidate: "0.1.0", Want: true},
		{Name: "Latest case-insensitive", Expr: "Latest", Candidate: "9.9.9-rc1", Want: true},
		{Name: "exact pin match", Expr: "0.1.31-preview", Candidate: "0.1.31-preview", Want: true},
		{Name: "exact pin miss", Expr: "0.1.31-preview", Candidate: "0.1.32-preview", Want: false},
		{Name: "ge matches base", Expr: ">=0.1.0", Candidate: "0.1.0", Want: true},
		// Pre-release versions are only matched by constraints that explicitly include a
		// pre-release tag on the lower bound (Masterminds/semver semantics).
		{Name: "ge does not match pre-release", Expr: ">=0.1.0", Candidate: "0.1.31-preview", Want: false},
		{
			Name:      "ge with preview lower bound matches pre-release",
			Expr:      ">=0.1.0-0",
			Candidate: "0.1.31-preview",
			Want:      true,
		},
		{Name: "ge matches higher major", Expr: ">=0.1.0", Candidate: "1.0.0", Want: true},
		{Name: "range matches in-range", Expr: ">=1.0.0,<2.0.0", Candidate: "1.5.0", Want: true},
		{Name: "range rejects below", Expr: ">=1.0.0,<2.0.0", Candidate: "0.9.0", Want: false},
		{Name: "range rejects upper bound", Expr: ">=1.0.0,<2.0.0", Candidate: "2.0.0", Want: false},
		{Name: "caret matches pre-release within range", Expr: "^0.1.0-0", Candidate: "0.1.31-preview", Want: true},
		{Name: "caret rejects next minor", Expr: "^0.1", Candidate: "0.2.0", Want: false},
		{Name: "tilde matches pre-release within range", Expr: "~0.1.0-preview", Candidate: "0.1.31-preview", Want: true},
		{Name: "tilde rejects next minor", Expr: "~0.1.0-preview", Candidate: "0.2.0", Want: false},
		{Name: "unparseable expr falls back to exact match", Expr: "dev", Candidate: "dev", Want: true},
		{
			Name:      "unparseable expr fallback case-insensitive",
			Expr:      "Nightly",
			Candidate: "nightly",
			Want:      true,
		},
		{Name: "unparseable expr no match", Expr: "dev", Candidate: "0.1.0", Want: false},
		{
			Name:      "non-semver candidate falls back to exact",
			Expr:      ">=0.1.0",
			Candidate: "nightly",
			Want:      false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			got := matchesVersionConstraint(tc.Expr, tc.Candidate)
			require.Equal(t, tc.Want, got)
		})
	}
}

func Test_CreateExtensionFilter_VersionConstraints(t *testing.T) {
	ext := &ExtensionMetadata{
		Id: "test.constraints",
		Versions: []ExtensionVersion{
			{Version: "0.1.0"},
			{Version: "0.1.31-preview"},
			{Version: "1.0.0"},
			{Version: "1.5.0"},
			{Version: "2.0.0"},
		},
	}

	testCases := []struct {
		Name    string
		Version string
		Match   bool
	}{
		{Name: "empty matches", Version: "", Match: true},
		{Name: "latest matches", Version: "latest", Match: true},
		{Name: "exact pin", Version: "0.1.31-preview", Match: true},
		{Name: "ge across versions", Version: ">=0.1.0", Match: true},
		{Name: "range matches 1.5.0", Version: ">=1.0.0,<2.0.0", Match: true},
		{Name: "caret 0.1 with preview lower bound", Version: "^0.1.0-0", Match: true},
		{Name: "tilde preview", Version: "~0.1.0-preview", Match: true},
		{Name: "unparseable matches none", Version: "dev", Match: false},
		{Name: "constraint matches nothing", Version: ">=99.0.0", Match: false},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			filter := createExtensionFilter(&FilterOptions{Version: tc.Version})
			require.Equal(t, tc.Match, filter(ext))
		})
	}
}

func Test_Install_PackDependency_SemverConstraint(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	packRegistry := Registry{
		Extensions: []*ExtensionMetadata{
			{
				Id:          "test.pack",
				Namespace:   "test",
				DisplayName: "Test Pack",
				Versions: []ExtensionVersion{
					{
						Version: "1.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "test.dependency", Version: ">=0.1.0-0"},
						},
					},
				},
			},
			{
				Id:          "test.dependency",
				Namespace:   "test",
				DisplayName: "Test Dependency",
				Versions: []ExtensionVersion{
					{Version: "0.1.0", Artifacts: sampleArtifacts},
					{Version: "0.1.15-preview", Artifacts: sampleArtifacts},
					{Version: "0.1.31-preview", Artifacts: sampleArtifacts},
				},
			},
		},
	}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.URL.String() == extensionRegistryUrl
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, packRegistry)
	})
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return strings.HasPrefix(request.URL.String(), "https://aka.ms/azd/extensions/registry/")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, []byte("test data"))
	})

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	packs, err := manager.FindExtensions(*mockContext.Context, &FilterOptions{Id: "test.pack"})
	require.NoError(t, err)
	require.Len(t, packs, 1)

	_, err = manager.Install(*mockContext.Context, packs[0], "")
	require.NoError(t, err)

	installed, err := manager.GetInstalled(FilterOptions{Id: "test.dependency"})
	require.NoError(t, err)
	require.NotNil(t, installed)
	require.Equal(t, "0.1.31-preview", installed.Version)
}

func Test_DownloadArtifact_Remote(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

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
	mockContext := mocks.NewMockContext(t.Context())

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
	mockContext := mocks.NewMockContext(t.Context())

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
	mockContext := mocks.NewMockContext(t.Context())

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
	mockContext := mocks.NewMockContext(t.Context())

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
	mockContext := mocks.NewMockContext(t.Context())

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
	mockContext := mocks.NewMockContext(t.Context())
	createRegistryMocks(mockContext)

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	t.Run("filter by service-target-provider capability", func(t *testing.T) {
		extensions, err := manager.FindExtensions(t.Context(), &FilterOptions{
			Capability: ServiceTargetProviderCapability,
		})
		require.NoError(t, err)
		require.Len(t, extensions, 3, "Should find 3 extensions with service-target-provider capability")

		assertExtensionIds(t, extensions,
			[]string{"azure.containerapp", "kubernetes.deploy", "foundry.multi.target"},
			[]string{"test.mcp.extension"})
	})

	t.Run("filter by MCP capability", func(t *testing.T) {
		extensions, err := manager.FindExtensions(t.Context(), &FilterOptions{
			Capability: McpServerCapability,
		})
		require.NoError(t, err)
		require.Len(t, extensions, 2, "Should find 2 extensions with MCP capability")

		assertExtensionIds(t, extensions,
			[]string{"test.mcp.extension", "kubernetes.deploy"},
			[]string{"azure.containerapp", "foundry.multi.target"})
	})

	t.Run("find extension with containerapp provider", func(t *testing.T) {
		extensions, err := manager.FindExtensions(t.Context(), &FilterOptions{
			Provider: "containerapp",
		})
		require.NoError(t, err)
		require.Len(t, extensions, 2, "Should find exactly 2 extensions with containerapp provider")

		assertExtensionIds(t, extensions,
			[]string{"azure.containerapp", "foundry.multi.target"},
			[]string{"test.mcp.extension", "kubernetes.deploy"})
	})

	t.Run("find extension with kubernetes provider", func(t *testing.T) {
		extensions, err := manager.FindExtensions(t.Context(), &FilterOptions{
			Provider: "kubernetes",
		})
		require.NoError(t, err)
		require.Len(t, extensions, 1, "Should find exactly 1 extension with kubernetes provider")

		assertExtensionIds(t, extensions,
			[]string{"kubernetes.deploy"},
			[]string{"azure.containerapp", "test.mcp.extension", "foundry.multi.target"})
	})

	t.Run("find extension with azure.ai.agents provider", func(t *testing.T) {
		extensions, err := manager.FindExtensions(t.Context(), &FilterOptions{
			Provider: "azure.ai.agents",
		})
		require.NoError(t, err)
		require.Len(t, extensions, 1, "Should find exactly 1 extension with azure.ai.agents provider")

		assertExtensionIds(t, extensions,
			[]string{"foundry.multi.target"},
			[]string{"azure.containerapp", "test.mcp.extension", "kubernetes.deploy"})
	})

	t.Run("find service target extension for containerapp", func(t *testing.T) {
		extensions, err := manager.FindExtensions(t.Context(), &FilterOptions{
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
		extensions, err := manager.FindExtensions(t.Context(), &FilterOptions{
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
		extensions, err := manager.FindExtensions(t.Context(), &FilterOptions{
			Provider: "KUBERNETES",
		})
		require.NoError(t, err)
		require.Len(t, extensions, 1, "Should find extension with case-insensitive provider matching")

		assertExtensionIds(t, extensions,
			[]string{"kubernetes.deploy"},
			[]string{"azure.containerapp", "test.mcp.extension", "foundry.multi.target"})
	})

	t.Run("filter with no matches", func(t *testing.T) {
		extensions, err := manager.FindExtensions(t.Context(), &FilterOptions{
			Provider: "nonexistent-provider",
		})
		require.NoError(t, err)
		require.Len(t, extensions, 0, "Should find no extensions with nonexistent provider")
	})

	t.Run("combine capability and tag filters", func(t *testing.T) {
		extensions, err := manager.FindExtensions(t.Context(), &FilterOptions{
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
		extensions, err := manager.FindExtensions(t.Context(), &FilterOptions{
			Capability: McpServerCapability,
			Provider:   "containerapp",
		})
		require.NoError(t, err)
		require.Len(t, extensions, 0, "Should find no extensions with MCP capability AND containerapp provider")
	})
}

func Test_FetchAndCacheMetadata(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
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
		timeoutMockContext := mocks.NewMockContext(t.Context())

		// Mock CommandRunner to simulate timeout for ANY metadata command
		timeoutMockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			// Match any command that has "metadata" as an argument
			return slices.Contains(args.Args, "metadata")
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

func Test_GetInstalled_WithSourceFilter(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", tempDir)

	mockContext := mocks.NewMockContext(t.Context())
	fileConfigManager := config.NewFileConfigManager(config.NewManager())
	userConfigManager := config.NewUserConfigManager(fileConfigManager)

	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})

	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	// Install same extension ID from two different sources
	extensions := map[string]*Extension{
		"test.ext": {
			Id:          "test.ext",
			Namespace:   "test",
			DisplayName: "Test Extension",
			Version:     "1.0.0",
			Source:      "azd",
			Path:        "extensions/test.ext/test-ext",
		},
	}

	err = manager.userConfig.Set(installedConfigKey, extensions)
	require.NoError(t, err)

	t.Run("filter by ID only", func(t *testing.T) {
		ext, err := manager.GetInstalled(FilterOptions{Id: "test.ext"})
		require.NoError(t, err)
		require.NotNil(t, ext)
		require.Equal(t, "test.ext", ext.Id)
	})

	t.Run("filter by ID and Source", func(t *testing.T) {
		ext, err := manager.GetInstalled(FilterOptions{Id: "test.ext", Source: "azd"})
		require.NoError(t, err)
		require.NotNil(t, ext)
		require.Equal(t, "test.ext", ext.Id)
		require.Equal(t, "azd", ext.Source)
	})

	t.Run("filter by ID and wrong Source returns not found", func(t *testing.T) {
		_, err := manager.GetInstalled(FilterOptions{Id: "test.ext", Source: "local"})
		require.ErrorIs(t, err, ErrInstalledExtensionNotFound)
	})

	t.Run("filter by non-existent ID returns not found", func(t *testing.T) {
		_, err := manager.GetInstalled(FilterOptions{Id: "non.existent"})
		require.ErrorIs(t, err, ErrInstalledExtensionNotFound)
	})
}

func Test_CreateSourcesFromConfig_PartialSchemaFailure(t *testing.T) {
	tempDir := t.TempDir()

	// Create a compatible registry file (schema 1.0)
	compatibleRegistry := Registry{
		SchemaVersion: "1.0",
		Extensions: []*ExtensionMetadata{
			{
				Id:          "compat.extension",
				Namespace:   "compat",
				DisplayName: "Compatible Extension",
				Description: "An extension from a compatible source",
				Versions: []ExtensionVersion{{
					Version:   "1.0.0",
					Artifacts: sampleArtifacts,
				}},
			},
		},
	}
	compatData, err := json.Marshal(compatibleRegistry)
	require.NoError(t, err)
	compatFile := filepath.Join(tempDir, "compat-registry.json")
	require.NoError(t, os.WriteFile(compatFile, compatData, 0600))

	// Create an incompatible registry file (schema 2.0)
	incompatibleRegistry := map[string]any{
		"schemaVersion": "2.0",
		"extensions":    []any{},
	}
	incompatData, err := json.Marshal(incompatibleRegistry)
	require.NoError(t, err)
	incompatFile := filepath.Join(tempDir, "incompat-registry.json")
	require.NoError(t, os.WriteFile(
		incompatFile, incompatData, 0600,
	))

	mockContext := mocks.NewMockContext(context.Background())

	// Pre-populate config with only our file sources so the
	// default "azd" URL source is never auto-created.
	cfg, _ := mockContext.ConfigManager.Load("")
	require.NoError(t, cfg.Set("extension.sources.compatible",
		&SourceConfig{
			Name:     "compatible",
			Type:     SourceKindFile,
			Location: compatFile,
		},
	))
	require.NoError(t, cfg.Set("extension.sources.incompatible",
		&SourceConfig{
			Name:     "incompatible",
			Type:     SourceKindFile,
			Location: incompatFile,
		},
	))

	userConfigManager := config.NewUserConfigManager(
		mockContext.ConfigManager,
	)
	sourceManager := NewSourceManager(
		mockContext.Container,
		userConfigManager,
		mockContext.HttpClient,
	)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(
		userConfigManager, sourceManager,
		lazyRunner, mockContext.HttpClient,
	)
	require.NoError(t, err)

	// FindExtensions should succeed — the compatible source works
	extensions, err := manager.FindExtensions(
		*mockContext.Context, nil,
	)
	require.NoError(t, err)
	require.Len(t, extensions, 1)
	require.Equal(t, "compat.extension", extensions[0].Id)
}

func Test_CreateSourcesFromConfig_AllSchemasIncompatible(t *testing.T) {
	tempDir := t.TempDir()

	// Create two incompatible registry files
	for _, name := range []string{
		"incompat1.json", "incompat2.json",
	} {
		registry := map[string]any{
			"schemaVersion": "2.0",
			"extensions":    []any{},
		}
		data, err := json.Marshal(registry)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(
			filepath.Join(tempDir, name), data, 0600,
		))
	}

	mockContext := mocks.NewMockContext(context.Background())

	// Pre-populate config with only incompatible file sources.
	cfg, _ := mockContext.ConfigManager.Load("")
	require.NoError(t, cfg.Set("extension.sources.src1",
		&SourceConfig{
			Name:     "src1",
			Type:     SourceKindFile,
			Location: filepath.Join(tempDir, "incompat1.json"),
		},
	))
	require.NoError(t, cfg.Set("extension.sources.src2",
		&SourceConfig{
			Name:     "src2",
			Type:     SourceKindFile,
			Location: filepath.Join(tempDir, "incompat2.json"),
		},
	))

	userConfigManager := config.NewUserConfigManager(
		mockContext.ConfigManager,
	)
	sourceManager := NewSourceManager(
		mockContext.Container,
		userConfigManager,
		mockContext.HttpClient,
	)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(
		userConfigManager, sourceManager,
		lazyRunner, mockContext.HttpClient,
	)
	require.NoError(t, err)

	// FindExtensions should fail — all sources are incompatible
	_, err = manager.FindExtensions(*mockContext.Context, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not supported")
}

func Test_EntryPoint_PathTraversal_Blocked(t *testing.T) {
	// Test that path traversal in entryPoint is detected via the shared IsPathContained utility
	testCases := []struct {
		name        string
		entryPoint  string
		shouldFail  bool
		windowsOnly bool
	}{
		{"normal entry point", "myext.exe", false, false},
		{"subdirectory entry point", "bin/myext", false, false},
		{"path traversal attempt", "../../malicious", true, false},
		// While backslashes are traditionally path separators only on Windows, IsPathContained normalizes
		// them on all platforms and will treat `..\..` as a traversal sequence even on non-Windows.
		{"path traversal with backslash", `..\..\malicious`, true, false},
		{"double dot deep traversal", "../../../../../../../tmp/evil", true, false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.windowsOnly && runtime.GOOS != "windows" {
				t.Skip("backslash path traversal only applies on Windows")
			}
			baseDir := t.TempDir()
			targetPath := filepath.Join(baseDir, tc.entryPoint)
			isContained := osutil.IsPathContained(baseDir, targetPath)

			if tc.shouldFail {
				require.False(t, isContained, "path traversal should have been detected for %q", tc.entryPoint)
			} else {
				require.True(t, isContained, "legitimate entry point %q should be allowed", tc.entryPoint)
			}
		})
	}
}

func Test_CopyFromLocalPath_PathTraversal_Blocked(t *testing.T) {
	// Verify the containment check logic for relative artifact paths via shared IsPathContained
	testCases := []struct {
		name       string
		path       string
		shouldFail bool
	}{
		{"normal relative path", "extensions/myext.zip", false},
		{"path traversal attempt", "../../etc/passwd", true},
		{"path traversal with nested dirs", "../../../tmp/malicious", true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			baseDir := t.TempDir()
			resolvedPath := filepath.Join(baseDir, tc.path)
			isContained := osutil.IsPathContained(baseDir, resolvedPath)

			if tc.shouldFail {
				require.False(t, isContained, "path traversal should have been detected for %q", tc.path)
			} else {
				require.True(t, isContained, "legitimate path %q should be allowed", tc.path)
			}
		})
	}
}

// Test_Upgrade_DependencyUpgrade verifies the dependency-upgrade decision
// matrix: parent upgrades select the highest published version satisfying
// each declared constraint (upgrade-to-best-match, same as npm/yarn), skip
// dependencies already at that best version, and respect the
// UpgradeDependencies=false opt-out.
func Test_Upgrade_DependencyUpgrade_TightenedConstraint(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	registry := Registry{
		Extensions: []*ExtensionMetadata{
			{
				Id:          "test.pack",
				Namespace:   "test",
				DisplayName: "Test Pack",
				Versions: []ExtensionVersion{
					{
						Version: "1.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "test.child", Version: "~1.0.0"},
						},
					},
					{
						Version: "2.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "test.child", Version: ">=2.0.0"},
						},
					},
				},
			},
			{
				Id:          "test.child",
				Namespace:   "test",
				DisplayName: "Test Child",
				Versions: []ExtensionVersion{
					{Version: "1.0.0", Artifacts: sampleArtifacts},
					{Version: "2.0.0", Artifacts: sampleArtifacts},
				},
			},
		},
	}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.URL.String() == extensionRegistryUrl
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, registry)
	})
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return strings.HasPrefix(request.URL.String(), "https://aka.ms/azd/extensions/registry/")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, []byte("test data"))
	})

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	// Install pack v1, which pulls in child v1 (constrained to ~1.0.0).
	packs, err := manager.FindExtensions(*mockContext.Context, &FilterOptions{Id: "test.pack"})
	require.NoError(t, err)
	require.Len(t, packs, 1)
	_, err = manager.Install(*mockContext.Context, packs[0], "1.0.0")
	require.NoError(t, err)

	installed, err := manager.GetInstalled(FilterOptions{Id: "test.child"})
	require.NoError(t, err)
	require.Equal(t, "1.0.0", installed.Version)

	// Upgrade pack to v2 — child constraint is now >=2.0.0 so child
	// must cascade-upgrade from 1.0.0 to 2.0.0.
	packsV2, err := manager.FindExtensions(*mockContext.Context, &FilterOptions{Id: "test.pack"})
	require.NoError(t, err)
	require.Len(t, packsV2, 1)

	_, depUpgrades, err := manager.Upgrade(
		*mockContext.Context,
		packsV2[0],
		DefaultUpgradeOptions("2.0.0"),
	)
	require.NoError(t, err)
	require.Len(t, depUpgrades, 1)
	require.Equal(t, "test.child", depUpgrades[0].ExtensionId)
	require.Equal(t, UpgradeStatusUpgraded, depUpgrades[0].Status)
	require.Equal(t, "1.0.0", depUpgrades[0].FromVersion)
	require.Equal(t, "2.0.0", depUpgrades[0].ToVersion)

	child, err := manager.GetInstalled(FilterOptions{Id: "test.child"})
	require.NoError(t, err)
	require.Equal(t, "2.0.0", child.Version)
}

func Test_Upgrade_DependencyUpgrade_ConstraintAlreadySatisfied(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	registry := Registry{
		Extensions: []*ExtensionMetadata{
			{
				Id: "test.pack",
				Versions: []ExtensionVersion{
					{
						Version: "2.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "test.child", Version: ">=1.0.0"},
						},
					},
				},
			},
			{
				Id: "test.child",
				Versions: []ExtensionVersion{
					{Version: "1.5.0", Artifacts: sampleArtifacts},
					{Version: "2.0.0", Artifacts: sampleArtifacts},
				},
			},
		},
	}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.URL.String() == extensionRegistryUrl
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, registry)
	})
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return strings.HasPrefix(request.URL.String(), "https://aka.ms/azd/extensions/registry/")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, []byte("test data"))
	})

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	packs, err := manager.FindExtensions(*mockContext.Context, &FilterOptions{Id: "test.pack"})
	require.NoError(t, err)
	_, err = manager.Install(*mockContext.Context, packs[0], "")
	require.NoError(t, err)

	// Child should be at 2.0.0 (latest >=1.0.0).
	child, err := manager.GetInstalled(FilterOptions{Id: "test.child"})
	require.NoError(t, err)
	require.Equal(t, "2.0.0", child.Version)

	// Upgrading pack again with the same constraint should not cascade.
	_, depUpgrades, err := manager.Upgrade(
		*mockContext.Context,
		packs[0],
		DefaultUpgradeOptions(""),
	)
	require.NoError(t, err)
	require.Empty(t, depUpgrades)
}

func Test_Upgrade_DependencyUpgrade_EmptyConstraint(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	registry := Registry{
		Extensions: []*ExtensionMetadata{
			{
				Id: "test.pack",
				Versions: []ExtensionVersion{
					{
						Version: "2.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "test.child"}, // empty constraint
						},
					},
				},
			},
			{
				Id: "test.child",
				Versions: []ExtensionVersion{
					{Version: "1.0.0", Artifacts: sampleArtifacts},
					{Version: "9.9.9", Artifacts: sampleArtifacts},
				},
			},
		},
	}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.URL.String() == extensionRegistryUrl
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, registry)
	})
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return strings.HasPrefix(request.URL.String(), "https://aka.ms/azd/extensions/registry/")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, []byte("test data"))
	})

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	packs, err := manager.FindExtensions(*mockContext.Context, &FilterOptions{Id: "test.pack"})
	require.NoError(t, err)
	_, err = manager.Install(*mockContext.Context, packs[0], "")
	require.NoError(t, err)

	// Force the child to an older version to confirm cascade does NOT
	// run when the parent's dependency constraint is empty.
	_ = manager.Uninstall("test.child")
	childMeta, err := manager.FindExtensions(*mockContext.Context, &FilterOptions{Id: "test.child"})
	require.NoError(t, err)
	_, err = manager.Install(*mockContext.Context, childMeta[0], "1.0.0")
	require.NoError(t, err)

	_, depUpgrades, err := manager.Upgrade(
		*mockContext.Context,
		packs[0],
		DefaultUpgradeOptions(""),
	)
	require.NoError(t, err)
	require.Empty(t, depUpgrades)

	child, err := manager.GetInstalled(FilterOptions{Id: "test.child"})
	require.NoError(t, err)
	require.Equal(t, "1.0.0", child.Version)
}

func Test_Upgrade_DependencyUpgrade_DisabledByOpts(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	registry := Registry{
		Extensions: []*ExtensionMetadata{
			{
				Id: "test.pack",
				Versions: []ExtensionVersion{
					{
						Version: "1.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "test.child", Version: "~1.0.0"},
						},
					},
					{
						Version: "2.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "test.child", Version: ">=2.0.0"},
						},
					},
				},
			},
			{
				Id: "test.child",
				Versions: []ExtensionVersion{
					{Version: "1.0.0", Artifacts: sampleArtifacts},
					{Version: "2.0.0", Artifacts: sampleArtifacts},
				},
			},
		},
	}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.URL.String() == extensionRegistryUrl
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, registry)
	})
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return strings.HasPrefix(request.URL.String(), "https://aka.ms/azd/extensions/registry/")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, []byte("test data"))
	})

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	packs, err := manager.FindExtensions(*mockContext.Context, &FilterOptions{Id: "test.pack"})
	require.NoError(t, err)
	_, err = manager.Install(*mockContext.Context, packs[0], "1.0.0")
	require.NoError(t, err)

	_, depUpgrades, err := manager.Upgrade(
		*mockContext.Context,
		packs[0],
		UpgradeOptions{VersionPreference: "2.0.0", UpgradeDependencies: false},
	)
	require.NoError(t, err)
	// With cascade disabled, a child whose installed version no longer
	// satisfies the new parent's constraint must surface as a Skipped
	// entry so the user is not left with a silent constraint violation.
	require.Len(t, depUpgrades, 1)
	require.Equal(t, "test.child", depUpgrades[0].ExtensionId)
	require.Equal(t, UpgradeStatusSkipped, depUpgrades[0].Status)
	require.Equal(t, "1.0.0", depUpgrades[0].FromVersion)
	require.Contains(t, depUpgrades[0].SkipReason, "automatic dependency upgrade disabled")

	child, err := manager.GetInstalled(FilterOptions{Id: "test.child"})
	require.NoError(t, err)
	require.Equal(t, "1.0.0", child.Version, "child must remain at 1.0.0 when cascade disabled")
}

func Test_Upgrade_DependencyUpgrade_CycleGuard(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	// Registry shape avoids a cycle during initial Install (pack.b v1 has no
	// dependency on pack.a) but introduces a cycle in the upgraded versions
	// (pack.b v2 depends on pack.a). The cascade visited-set must prevent the
	// upgraded pack.b from re-cascading back into pack.a.
	registry := Registry{
		Extensions: []*ExtensionMetadata{
			{
				Id: "pack.a",
				Versions: []ExtensionVersion{
					{
						Version: "1.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "pack.b", Version: "~1.0.0"},
						},
					},
					{
						Version: "2.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "pack.b", Version: ">=2.0.0"},
						},
					},
				},
			},
			{
				Id: "pack.b",
				Versions: []ExtensionVersion{
					{
						Version:   "1.0.0",
						Artifacts: sampleArtifacts,
					},
					{
						Version:   "2.0.0",
						Artifacts: sampleArtifacts,
						Dependencies: []ExtensionDependency{
							{Id: "pack.a", Version: ">=1.0.0"},
						},
					},
				},
			},
		},
	}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.URL.String() == extensionRegistryUrl
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, registry)
	})
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return strings.HasPrefix(request.URL.String(), "https://aka.ms/azd/extensions/registry/")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, []byte("test data"))
	})

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	// Bootstrap: install pack.a v1 → pulls in pack.b v1 (no transitive deps).
	packs, err := manager.FindExtensions(*mockContext.Context, &FilterOptions{Id: "pack.a"})
	require.NoError(t, err)
	_, err = manager.Install(*mockContext.Context, packs[0], "1.0.0")
	require.NoError(t, err)

	// Upgrade pack.a — pack.b cascade-upgrades to v2 (which now declares
	// pack.a as a dep). The cascade visited-set must skip pack.a inside
	// pack.b's nested cascade rather than recurse back into it.
	_, depUpgrades, err := manager.Upgrade(
		*mockContext.Context,
		packs[0],
		DefaultUpgradeOptions("2.0.0"),
	)
	require.NoError(t, err)
	require.Len(t, depUpgrades, 1)
	require.Equal(t, "pack.b", depUpgrades[0].ExtensionId)
	require.Equal(t, UpgradeStatusUpgraded, depUpgrades[0].Status)
	require.Equal(t, "2.0.0", depUpgrades[0].ToVersion)
	// pack.a is in the visited set, so pack.b's nested cascade must not
	// re-upgrade it.
	require.Empty(t, depUpgrades[0].DependencyUpgrades, "nested cascade must respect visited set")
}

func Test_Upgrade_DependencyUpgrade_NestedPacks(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	registry := Registry{
		Extensions: []*ExtensionMetadata{
			{
				Id: "pack.a",
				Versions: []ExtensionVersion{
					{
						Version: "1.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "pack.b", Version: "~1.0.0"},
						},
					},
					{
						Version: "2.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "pack.b", Version: ">=2.0.0"},
						},
					},
				},
			},
			{
				Id: "pack.b",
				Versions: []ExtensionVersion{
					{
						Version: "1.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "leaf.c", Version: "~1.0.0"},
						},
					},
					{
						Version: "2.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "leaf.c", Version: ">=2.0.0"},
						},
					},
				},
			},
			{
				Id: "leaf.c",
				Versions: []ExtensionVersion{
					{Version: "1.0.0", Artifacts: sampleArtifacts},
					{Version: "2.0.0", Artifacts: sampleArtifacts},
				},
			},
		},
	}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.URL.String() == extensionRegistryUrl
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, registry)
	})
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return strings.HasPrefix(request.URL.String(), "https://aka.ms/azd/extensions/registry/")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, []byte("test data"))
	})

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	// Bootstrap: install A v1 → B v1 → C v1 (constraints pin each step to ~1.0.0).
	packs, err := manager.FindExtensions(*mockContext.Context, &FilterOptions{Id: "pack.a"})
	require.NoError(t, err)
	_, err = manager.Install(*mockContext.Context, packs[0], "1.0.0")
	require.NoError(t, err)

	bInstalled, err := manager.GetInstalled(FilterOptions{Id: "pack.b"})
	require.NoError(t, err)
	require.Equal(t, "1.0.0", bInstalled.Version)
	cInstalled, err := manager.GetInstalled(FilterOptions{Id: "leaf.c"})
	require.NoError(t, err)
	require.Equal(t, "1.0.0", cInstalled.Version)

	// Upgrade A to v2 — should cascade to B v2 → C v2.
	_, depUpgrades, err := manager.Upgrade(
		*mockContext.Context,
		packs[0],
		DefaultUpgradeOptions("2.0.0"),
	)
	require.NoError(t, err)
	require.Len(t, depUpgrades, 1)
	require.Equal(t, "pack.b", depUpgrades[0].ExtensionId)
	require.Equal(t, UpgradeStatusUpgraded, depUpgrades[0].Status)
	require.Equal(t, "2.0.0", depUpgrades[0].ToVersion)
	require.Len(t, depUpgrades[0].DependencyUpgrades, 1)
	require.Equal(t, "leaf.c", depUpgrades[0].DependencyUpgrades[0].ExtensionId)
	require.Equal(t, "2.0.0", depUpgrades[0].DependencyUpgrades[0].ToVersion)

	bFinal, err := manager.GetInstalled(FilterOptions{Id: "pack.b"})
	require.NoError(t, err)
	require.Equal(t, "2.0.0", bFinal.Version)
	cFinal, err := manager.GetInstalled(FilterOptions{Id: "leaf.c"})
	require.NoError(t, err)
	require.Equal(t, "2.0.0", cFinal.Version)
}

func Test_Install_DependencyCycle_Bounded(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	// Registry defines a true install-time cycle: A → B → A. Without an
	// in-flight visited set, Install would recurse without bound because the
	// parent's installed-config record is only written after dependencies
	// finish installing, so ErrExtensionInstalled cannot short-circuit
	// the cycle during the initial install.
	registry := Registry{
		Extensions: []*ExtensionMetadata{
			{
				Id: "pack.a",
				Versions: []ExtensionVersion{
					{
						Version: "1.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "pack.b", Version: "1.0.0"},
						},
					},
				},
			},
			{
				Id: "pack.b",
				Versions: []ExtensionVersion{
					{
						Version: "1.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "pack.a", Version: "1.0.0"},
						},
					},
				},
			},
		},
	}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.URL.String() == extensionRegistryUrl
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, registry)
	})
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return strings.HasPrefix(request.URL.String(), "https://aka.ms/azd/extensions/registry/")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, []byte("test data"))
	})

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	packs, err := manager.FindExtensions(*mockContext.Context, &FilterOptions{Id: "pack.a"})
	require.NoError(t, err)
	require.Len(t, packs, 1)

	// Install must terminate (not recurse forever) and surface a cycle error.
	_, err = manager.Install(*mockContext.Context, packs[0], "1.0.0")
	require.Error(t, err)
	require.Contains(t, err.Error(), "dependency cycle detected")
}

func Test_Upgrade_DependencyUpgrade_ConstraintConflict(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	// Pack A v2 depends on { B: ">=2.0.0", C: ">=2.0.0" }.
	// B v2 depends on { C: "~1.0.0" } — mutually unsatisfiable with A's
	// constraint on C. Iteration upgrades B first, B's cascade pins C to
	// 1.x to satisfy its own constraint, then A's cascade reaches C: the
	// installed C now violates A's >=2.0.0 and C is already in the visited
	// set. The cascade must surface this as a Failed entry rather than
	// silently leaving C at a version A does not allow.
	registry := Registry{
		Extensions: []*ExtensionMetadata{
			{
				Id: "pack.a",
				Versions: []ExtensionVersion{
					{
						Version: "1.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "pack.b", Version: "~1.0.0"},
							{Id: "leaf.c", Version: "~0.9.0"},
						},
					},
					{
						Version: "2.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "pack.b", Version: ">=2.0.0"},
							{Id: "leaf.c", Version: ">=2.0.0"},
						},
					},
				},
			},
			{
				Id: "pack.b",
				Versions: []ExtensionVersion{
					{
						Version: "1.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "leaf.c", Version: "~0.9.0"},
						},
					},
					{
						Version: "2.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "leaf.c", Version: "~1.0.0"},
						},
					},
				},
			},
			{
				Id: "leaf.c",
				Versions: []ExtensionVersion{
					{Version: "0.9.0", Artifacts: sampleArtifacts},
					{Version: "1.0.0", Artifacts: sampleArtifacts},
					{Version: "2.0.0", Artifacts: sampleArtifacts},
				},
			},
		},
	}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.URL.String() == extensionRegistryUrl
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, registry)
	})
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return strings.HasPrefix(request.URL.String(), "https://aka.ms/azd/extensions/registry/")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, []byte("test data"))
	})

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	// Bootstrap: install A v1 → B v1 → C v0.9. After this, all three are
	// installed and consistent with A v1's constraints.
	packs, err := manager.FindExtensions(*mockContext.Context, &FilterOptions{Id: "pack.a"})
	require.NoError(t, err)
	_, err = manager.Install(*mockContext.Context, packs[0], "1.0.0")
	require.NoError(t, err)

	cInstalled, err := manager.GetInstalled(FilterOptions{Id: "leaf.c"})
	require.NoError(t, err)
	require.Equal(t, "0.9.0", cInstalled.Version)

	// Upgrade A to v2 — iteration order processes B first (sibling pin to
	// C ~1.0.0), then attempts to upgrade C to satisfy A's >=2.0.0.
	_, depUpgrades, err := manager.Upgrade(
		*mockContext.Context,
		packs[0],
		DefaultUpgradeOptions("2.0.0"),
	)
	require.NoError(t, err)

	// Two top-level dependency-upgrade entries: B upgraded, C reported as a conflict.
	require.Len(t, depUpgrades, 2)

	var bEntry, cEntry *UpgradeResult
	for i := range depUpgrades {
		entry := &depUpgrades[i]
		switch entry.ExtensionId {
		case "pack.b":
			bEntry = entry
		case "leaf.c":
			cEntry = entry
		}
	}
	require.NotNil(t, bEntry, "pack.b dependency-upgrade entry expected")
	require.NotNil(t, cEntry, "leaf.c dependency-upgrade entry expected")

	require.Equal(t, UpgradeStatusUpgraded, bEntry.Status)
	require.Equal(t, "2.0.0", bEntry.ToVersion)

	require.Equal(t, UpgradeStatusFailed, cEntry.Status)
	require.Error(t, cEntry.Error)
	require.Contains(t, cEntry.Error.Error(), "constraint conflict")
	require.Contains(t, cEntry.Error.Error(), "leaf.c")
}
