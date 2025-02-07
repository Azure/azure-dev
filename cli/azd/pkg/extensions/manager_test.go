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

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, http.DefaultClient)

	manager := NewManager(userConfigManager, sourceManager, http.DefaultClient)
	err := manager.Initialize()
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
	extensionVersion, err := manager.Install(*mockContext.Context, extensions[0].Id, "")
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
