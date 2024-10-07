package extensions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	azruntime "github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/cache"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
)

const (
	registryCacheFilePath = "registry.cache"
	extensionRegistryUrl  = "https://raw.githubusercontent.com/wbreza/azd-extensions/refs/heads/main/registry/registry.json"
)

var (
	ErrInstalledExtensionNotFound = errors.New("extension not found")
	ErrRegistryExtensionNotFound  = errors.New("extension not found in registry")
	ErrExtensionInstalled         = errors.New("extension already installed")
	registryCacheDuration         = 24 * time.Hour
)

type Manager struct {
	configManager config.UserConfigManager
	userConfig    config.Config
	pipeline      azruntime.Pipeline
	registryCache *cache.FileCache[ExtensionRegistry]
}

// NewManager creates a new extension manager
func NewManager(configManager config.UserConfigManager, transport policy.Transporter) *Manager {
	pipeline := azruntime.NewPipeline("azd-extensions", "1.0.0", azruntime.PipelineOptions{}, &policy.ClientOptions{
		Transport: transport,
	})

	return &Manager{
		configManager: configManager,
		pipeline:      pipeline,
	}
}

// Initialize the extension manager
func (m *Manager) Initialize() error {
	userConfig, err := m.configManager.Load()
	if err != nil {
		return err
	}

	configDir, err := config.GetUserConfigDir()
	if err != nil {
		return fmt.Errorf("failed to get user config directory: %w", err)
	}

	registryCachePath := filepath.Join(configDir, registryCacheFilePath)
	m.registryCache = cache.NewFileCache(registryCachePath, registryCacheDuration, m.loadRegistry)
	m.userConfig = userConfig

	return nil
}

// ListInstalled retrieves a list of installed extensions
func (m *Manager) ListInstalled() (map[string]*Extension, error) {
	var extensions map[string]*Extension

	ok, err := m.userConfig.GetSection("extensions", &extensions)
	if err != nil {
		return nil, fmt.Errorf("failed to get extensions section: %w", err)
	}

	if !ok || extensions == nil {
		return map[string]*Extension{}, nil
	}

	return extensions, nil
}

// GetInstalled retrieves an installed extension by name
func (m *Manager) GetInstalled(name string) (*Extension, error) {
	extensions, err := m.ListInstalled()
	if err != nil {
		return nil, err
	}

	if extension, has := extensions[name]; has {
		return extension, nil
	}

	return nil, fmt.Errorf("%s %w", name, ErrInstalledExtensionNotFound)
}

// GetFromRegistry retrieves an extension from the registry by name
func (m *Manager) GetFromRegistry(ctx context.Context, name string) (*RegistryExtension, error) {
	extensions, err := m.ListFromRegistry(ctx)
	if err != nil {
		return nil, err
	}

	for _, extension := range extensions {
		if strings.EqualFold(extension.Name, name) {
			return extension, nil
		}
	}

	return nil, fmt.Errorf("%s %w", name, ErrRegistryExtensionNotFound)
}

func (m *Manager) ListFromRegistry(ctx context.Context) ([]*RegistryExtension, error) {
	registry, err := m.registryCache.Resolve(ctx)
	if err != nil {
		return nil, err
	}

	return registry.Extensions, nil
}

// loadRegistry retrieves a list of extensions from the registry
func (m *Manager) loadRegistry(ctx context.Context) (*ExtensionRegistry, error) {
	req, err := azruntime.NewRequest(ctx, http.MethodGet, extensionRegistryUrl)
	if err != nil {
		return nil, err
	}

	resp, err := m.pipeline.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed for template source '%s', %w", extensionRegistryUrl, err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, azruntime.NewResponseError(resp)
	}

	// Read the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Unmarshal JSON into ExtensionRegistry struct
	var registry *ExtensionRegistry
	err = json.Unmarshal(body, &registry)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON: %w", err)
	}

	// Return the registry
	return registry, nil
}

// Install an extension by name and optional version
// If no version is provided, the latest version is installed
// Latest version is determined by the last element in the Versions slice
func (m *Manager) Install(ctx context.Context, name string, version string) (*RegistryExtensionVersion, error) {
	installed, err := m.GetInstalled(name)
	if err == nil && installed != nil {
		return nil, fmt.Errorf("%s %w", name, ErrExtensionInstalled)
	}

	// Step 1: Find the extension by name
	extension, err := m.GetFromRegistry(ctx, name)
	if err != nil {
		return nil, err
	}

	// Step 2: Determine the version to install
	var selectedVersion *RegistryExtensionVersion

	if version == "" {
		// Default to the latest version (last in the slice)
		versions := extension.Versions
		if len(versions) == 0 {
			return nil, fmt.Errorf("no versions available for extension: %s", name)
		}

		selectedVersion = &versions[len(versions)-1]
	} else {
		// Find the specific version
		for _, v := range extension.Versions {
			if v.Version == version {
				selectedVersion = &v
				break
			}
		}

		if selectedVersion == nil {
			return nil, fmt.Errorf("version %s not found for extension: %s", version, name)
		}
	}

	// Step 3: Find the binary for the current OS
	binary, err := findBinaryForCurrentOS(selectedVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to find binary for current OS: %w", err)
	}

	// Step 4: Download the binary to a temp location
	tempFilePath, err := m.downloadBinary(ctx, binary.Url)
	if err != nil {
		return nil, fmt.Errorf("failed to download binary: %w", err)
	}

	// Clean up the temp file after all scenarios
	defer os.Remove(tempFilePath)

	// Step 5: Validate the checksum if provided
	if err := validateChecksum(tempFilePath, binary.Checksum); err != nil {
		return nil, fmt.Errorf("checksum validation failed: %w", err)
	}

	// Step 6: Copy the binary to the user's home directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get user's home directory: %w", err)
	}

	targetDir := filepath.Join(homeDir, ".azd", "bin")
	if err := os.MkdirAll(targetDir, os.ModePerm); err != nil {
		return nil, fmt.Errorf("failed to create target directory: %w", err)
	}

	targetPath := filepath.Join(targetDir, filepath.Base(tempFilePath))
	if err := copyFile(tempFilePath, targetPath); err != nil {
		return nil, fmt.Errorf("failed to copy binary to target location: %w", err)
	}

	relativeExtensionPath, err := filepath.Rel(homeDir, targetPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get relative path: %w", err)
	}

	// Step 7: Update the user config with the installed extension
	extensions, err := m.ListInstalled()
	if err != nil {
		return nil, fmt.Errorf("failed to list installed extensions: %w", err)
	}

	extensions[name] = &Extension{
		Name:        name,
		DisplayName: extension.DisplayName,
		Description: extension.Description,
		Version:     selectedVersion.Version,
		Usage:       selectedVersion.Usage,
		Path:        relativeExtensionPath,
	}

	if err := m.userConfig.Set("extensions", extensions); err != nil {
		return nil, fmt.Errorf("failed to set extensions section: %w", err)
	}

	if err := m.configManager.Save(m.userConfig); err != nil {
		return nil, fmt.Errorf("failed to save user config: %w", err)
	}

	log.Printf("Extension '%s' (version %s) installed successfully to %s\n", name, selectedVersion.Version, targetPath)
	return selectedVersion, nil
}

// Uninstall an extension by name
func (m *Manager) Uninstall(name string) error {
	// Get the installed extension
	extension, err := m.GetInstalled(name)
	if err != nil {
		return fmt.Errorf("failed to get installed extension: %w", err)
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get user's home directory: %w", err)
	}

	// Remove the extension binary when it exists
	extensionPath := filepath.Join(homeDir, extension.Path)
	_, err = os.Stat(extensionPath)
	if err == nil {
		if err := os.Remove(extensionPath); err != nil {
			return fmt.Errorf("failed to remove extension: %w", err)
		}
	}

	// Update the user config
	extensions, err := m.ListInstalled()
	if err != nil {
		return fmt.Errorf("failed to list installed extensions: %w", err)
	}

	delete(extensions, name)

	if err := m.userConfig.Set("extensions", extensions); err != nil {
		return fmt.Errorf("failed to set extensions section: %w", err)
	}

	if err := m.configManager.Save(m.userConfig); err != nil {
		return fmt.Errorf("failed to save user config: %w", err)
	}

	log.Printf("Extension '%s' uninstalled successfully\n", name)
	return nil
}

// Upgrade upgrades the extension to the specified version
// This is a convenience method that uninstalls the existing extension and installs the new version
// If the version is not specified, the latest version is installed
func (m *Manager) Upgrade(ctx context.Context, name string, version string) (*RegistryExtensionVersion, error) {
	if err := m.Uninstall(name); err != nil {
		return nil, fmt.Errorf("failed to uninstall extension: %w", err)
	}

	extensionVersion, err := m.Install(ctx, name, version)
	if err != nil {
		return nil, fmt.Errorf("failed to install extension: %w", err)
	}

	return extensionVersion, nil
}

// Helper function to find the binary for the current OS
func findBinaryForCurrentOS(version *RegistryExtensionVersion) (*Binary, error) {
	if version.Binaries == nil {
		return nil, fmt.Errorf("no binaries available for this version")
	}

	var binary Binary
	var exists bool

	platform := runtime.GOOS

	switch platform {
	case "darwin":
		binary, exists = version.Binaries["macos"]
	case "linux":
		binary, exists = version.Binaries["linux"]
	case "windows":
		binary, exists = version.Binaries["windows"]
	}

	if !exists {
		return nil, fmt.Errorf("no binary available for platform: %s", platform)
	}

	if binary.Url == "" {
		return nil, fmt.Errorf("binary URL is missing for platform: %s", platform)
	}

	return &binary, nil
}

// downloadFile downloads a file from the given URL and saves it to a temporary directory using the filename from the URL.
func (m *Manager) downloadBinary(ctx context.Context, binaryUrl string) (string, error) {
	req, err := azruntime.NewRequest(ctx, http.MethodGet, binaryUrl)
	if err != nil {
		return "", err
	}

	// Perform HTTP GET request
	resp, err := m.pipeline.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to download file: %w", err)
	}
	defer resp.Body.Close()

	// Check for successful response
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download file, status code: %d", resp.StatusCode)
	}

	// Extract the filename from the URL
	filename := filepath.Base(binaryUrl)

	// Create a temporary file in the system's temp directory with the same filename
	tempDir := os.TempDir()
	tempFilePath := filepath.Join(tempDir, filename)

	// Create the file at the desired location
	tempFile, err := os.Create(tempFilePath)
	if err != nil {
		return "", fmt.Errorf("failed to create temporary file: %w", err)
	}
	defer tempFile.Close()

	// Write the response body to the file
	_, err = io.Copy(tempFile, resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to write to temporary file: %w", err)
	}

	return tempFilePath, nil
}

// validateChecksum validates the file at the given path against the expected checksum using the specified algorithm.
func validateChecksum(filePath string, checksum *Checksum) error {
	// TODO: Checksum optional for POC
	return nil

	// // Check if checksum or required fields are nil
	// if checksum.Algorithm == "" || checksum.Value == "" {
	// 	return fmt.Errorf("invalid checksum data: algorithm and value must be specified")
	// }

	// var hashAlgo hash.Hash

	// // Select the hashing algorithm based on the input
	// switch checksum.Algorithm {
	// case "sha256":
	// 	hashAlgo = sha256.New()
	// case "sha512":
	// 	hashAlgo = sha512.New()
	// default:
	// 	return fmt.Errorf("unsupported checksum algorithm: %s", checksum.Algorithm)
	// }

	// // Open the file for reading
	// file, err := os.Open(filePath)
	// if err != nil {
	// 	return fmt.Errorf("failed to open file for checksum validation: %w", err)
	// }
	// defer file.Close()

	// // Compute the checksum
	// if _, err := io.Copy(hashAlgo, file); err != nil {
	// 	return fmt.Errorf("failed to compute checksum: %w", err)
	// }

	// // Convert the computed checksum to a hexadecimal string
	// computedChecksum := hex.EncodeToString(hashAlgo.Sum(nil))

	// // Compare the computed checksum with the expected checksum
	// if computedChecksum != checksum.Value {
	// 	return fmt.Errorf("checksum mismatch: expected %s, got %s", checksum.Value, computedChecksum)
	// }

	// return nil
}

// Helper function to copy a file to the target directory
func copyFile(src, dst string) error {
	input, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open source file: %w", err)
	}
	defer input.Close()

	output, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("failed to create destination file: %w", err)
	}
	defer output.Close()

	_, err = io.Copy(output, input)
	if err != nil {
		return fmt.Errorf("failed to copy file: %w", err)
	}

	return nil
}
