// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
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
	manager, err := NewManager(userConfigManager, sourceManager, mockContext.HttpClient)
	require.NoError(t, err)

	// List installed extensions (expect 0)
	installed, err := manager.ListInstalled()
	require.NoError(t, err)
	require.NotNil(t, installed)
	require.Equal(t, 0, len(installed))

	// List extensions from the registry (expect at least 1)
	extensions, err := manager.ListFromRegistry(*mockContext.Context, nil)
	require.NoError(t, err)
	require.NotNil(t, extensions)
	require.Greater(t, len(extensions), 0)

	// Install the first extension
	extensionVersion, err := manager.Install(*mockContext.Context, extensions[0].Id, nil)
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
	manager, err := NewManager(userConfigManager, sourceManager, mockContext.HttpClient)
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
			filterOptions := &FilterOptions{
				Version: tc.Constraint,
			}
			extensionVersion, err := manager.Install(*mockContext.Context, "test.extension", filterOptions)
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

func createRegistryMocks(mockContext *mocks.MockContext) {
	// Create a mock source
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.URL.String() == extensionRegistryUrl
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, testRegistry)
	})

	// Return some mock file
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return strings.HasPrefix(request.URL.String(), "https://aka.ms/azd/extensions/registry/test.extension")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, []byte("test data"))
	})
}

var sampleArtifacts = map[string]ExtensionArtifact{
	"darwin": {
		//nolint:lll
		URL: "https://aka.ms/azd/extensions/registry/test.extension/azd-ext-test-darwin-amd64",
	},
	"windows": {
		//nolint:lll
		URL: "https://aka.ms/azd/extensions/registry/test.extension/azd-ext-test-windows-amd64.exe",
	},
	"linux": {
		//nolint:lll
		URL: "https://aka.ms/azd/extensions/registry/test.extension/azd-ext-test-linux-amd64",
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
	},
}
