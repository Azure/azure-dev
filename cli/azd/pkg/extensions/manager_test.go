package extensions

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
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

func Test_SignAndVerifySignature_Success(t *testing.T) {
	// Generate a new RSA key pair
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	publicKey := &privateKey.PublicKey

	// Data to be signed
	data := []byte("test data")

	// Sign the data
	signature, err := sign(data, privateKey)
	require.NoError(t, err)

	// Verify the signature
	err = verifySignature(data, signature, publicKey)
	require.NoError(t, err)
}

func Test_VerifySignature_Failure(t *testing.T) {
	// Generate a new RSA key pair
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	publicKey := &privateKey.PublicKey

	// Data to be signed
	data := []byte("test data")

	// Sign the data
	signature, err := sign(data, privateKey)
	require.NoError(t, err)

	// Modify the data to make the signature invalid
	modifiedData := []byte("modified data")

	// Verify the signature with modified data
	err = verifySignature(modifiedData, signature, publicKey)
	require.Error(t, err)
	require.Contains(t, err.Error(), "signature verification failed")
}

func Test_Sign_Failure(t *testing.T) {
	// Generate a new RSA key pair with a small key size to force an error
	// nolint:gosec
	privateKey, err := rsa.GenerateKey(rand.Reader, 512)
	require.NoError(t, err)

	// Data to be signed
	data := []byte("test data")

	// Sign the data
	_, err = sign(data, privateKey)
	require.Error(t, err)
	require.Contains(t, err.Error(), "key size is too small")
}

func Test_VerifySignature_InvalidSignature(t *testing.T) {
	// Generate a new RSA key pair
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	publicKey := &privateKey.PublicKey

	// Data to be signed
	data := []byte("test data")

	// Invalid signature
	invalidSignature := "invalid_signature"

	// Verify the invalid signature
	err = verifySignature(data, invalidSignature, publicKey)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to decode signature")
}

func Test_LoadPublicKeys(t *testing.T) {
	// Load the public keys
	keys, err := loadPublicKeys()
	require.NoError(t, err)
	require.NotEmpty(t, keys)
	require.Greater(t, len(keys), 0)

	// Validate that all public keys are at least 2048 bits in length
	for keyID, publicKey := range keys {
		require.GreaterOrEqual(t, publicKey.N.BitLen(), 2048, "public key %s is less than 2048 bits", keyID)
	}
}

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
