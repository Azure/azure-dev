// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	azruntime "github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Masterminds/semver/v3"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/rzip"
)

const (
	extensionRegistryUrl = "https://aka.ms/azd/extensions/registry"
)

var (
	ErrExtensionNotFound          = errors.New("extension not found")
	ErrInstalledExtensionNotFound = errors.New("extension not found")
	ErrRegistryExtensionNotFound  = errors.New("extension not found in registry")
	ErrExtensionInstalled         = errors.New("extension already installed")

	FeatureExtensions = alpha.MustFeatureKey("extensions")
)

// ListOptions is used to filter extensions by source and tags
type ListOptions struct {
	// Source is used to specify the source of the extension to install
	Source string
	// Tags is used to specify the tags of the extension to install
	Tags []string
}

// FilterOptions is used to filter extensions by version and source
type FilterOptions struct {
	// Version is used to specify the version of the extension to install
	Version string
	// Source is used to specify the source of the extension to install
	Source string
}

// LookupOptions is used to lookup extensions by id or namespace
type LookupOptions struct {
	// Id is used to specify the id of the extension to install
	Id string
	// Namespace is used to specify the namespace of the extension to install
	Namespace string
}

type sourceFilterPredicate func(config *SourceConfig) bool
type extensionFilterPredicate func(extension *ExtensionMetadata) bool

// Manager is responsible for managing extensions
type Manager struct {
	sourceManager *SourceManager
	sources       []Source
	installed     map[string]*Extension

	configManager config.UserConfigManager
	userConfig    config.Config
	pipeline      azruntime.Pipeline
}

// NewManager creates a new extension manager
func NewManager(
	configManager config.UserConfigManager,
	sourceManager *SourceManager,
	transport policy.Transporter,
) (*Manager, error) {
	userConfig, err := configManager.Load()
	if err != nil {
		return nil, err
	}

	pipeline := azruntime.NewPipeline("azd-extensions", "1.0.0", azruntime.PipelineOptions{}, &policy.ClientOptions{
		Transport: transport,
	})

	return &Manager{
		userConfig:    userConfig,
		configManager: configManager,
		sourceManager: sourceManager,
		pipeline:      pipeline,
	}, nil
}

// ListInstalled retrieves a list of installed extensions
func (m *Manager) ListInstalled() (map[string]*Extension, error) {
	var extensions map[string]*Extension

	if m.installed != nil {
		return m.installed, nil
	}

	ok, err := m.userConfig.GetSection(installedConfigKey, &extensions)
	if err != nil {
		return nil, fmt.Errorf("failed to get extensions section: %w", err)
	}

	if !ok || extensions == nil {
		extensions = map[string]*Extension{}
	}

	// Initialize the extensions since this are instantiated from JSON unmarshalling.
	for _, extension := range extensions {
		extension.init()
	}

	m.installed = extensions

	return m.installed, nil
}

// GetInstalled retrieves an installed extension by name
func (m *Manager) GetInstalled(options LookupOptions) (*Extension, error) {
	extensions, err := m.ListInstalled()
	if err != nil {
		return nil, err
	}

	if options.Id != "" {
		extension, has := extensions[options.Id]
		if !has {
			return nil, fmt.Errorf("%s %w", options.Id, ErrInstalledExtensionNotFound)
		}

		return extension, nil
	}

	if options.Namespace != "" {
		for _, extension := range extensions {
			if strings.EqualFold(extension.Namespace, options.Namespace) {
				return extension, nil
			}
		}
	}

	return nil, ErrInstalledExtensionNotFound
}

// GetFromRegistry retrieves an extension from the registry by name
func (m *Manager) GetFromRegistry(
	ctx context.Context,
	extensionId string,
	options *FilterOptions,
) (*ExtensionMetadata, error) {
	if options == nil {
		options = &FilterOptions{}
	}

	filterPredicate := func(config *SourceConfig) bool {
		if options.Source == "" {
			return true
		}

		return strings.EqualFold(config.Name, options.Source)
	}

	sources, err := m.getSources(ctx, filterPredicate)
	if err != nil {
		return nil, fmt.Errorf("failed getting extension sources: %w", err)
	}

	var match *ExtensionMetadata
	var sourceErr error

	for _, source := range sources {
		extension, err := source.GetExtension(ctx, extensionId)
		if err != nil {
			sourceErr = err
		} else if extension != nil {
			match = extension
			break
		}
	}

	if match != nil {
		return match, nil
	}

	if sourceErr != nil {
		return nil, fmt.Errorf("failed getting extension: %w", sourceErr)
	}

	return nil, fmt.Errorf("%s %w", extensionId, ErrRegistryExtensionNotFound)
}

func (m *Manager) ListFromRegistry(ctx context.Context, options *ListOptions) ([]*ExtensionMetadata, error) {
	allExtensions := []*ExtensionMetadata{}

	if options == nil {
		options = &ListOptions{}
	}

	var sourceFilterPredicate sourceFilterPredicate
	if options.Source != "" {
		sourceFilterPredicate = func(config *SourceConfig) bool {
			return strings.EqualFold(config.Name, options.Source)
		}
	}

	var extensionFilterPredicate extensionFilterPredicate
	if len(options.Tags) > 0 {
		// Find extensions that match all the incoming tags
		extensionFilterPredicate = func(extension *ExtensionMetadata) bool {
			match := false
			for _, optionTag := range options.Tags {
				match = slices.ContainsFunc(extension.Tags, func(extensionTag string) bool {
					return strings.EqualFold(optionTag, extensionTag)
				})

				if !match {
					break
				}
			}

			return match
		}
	}

	sources, err := m.getSources(ctx, sourceFilterPredicate)
	if err != nil {
		return nil, fmt.Errorf("failed listing extensions: %w", err)
	}

	for _, source := range sources {
		filteredExtensions := []*ExtensionMetadata{}
		sourceExtensions, err := source.ListExtensions(ctx)
		if err != nil {
			return nil, fmt.Errorf("unable to list extension: %w", err)
		}

		for _, extension := range sourceExtensions {
			if extensionFilterPredicate == nil || extensionFilterPredicate(extension) {
				filteredExtensions = append(filteredExtensions, extension)
			}
		}

		// Sort by source, then repository path and finally name
		slices.SortFunc(filteredExtensions, func(a *ExtensionMetadata, b *ExtensionMetadata) int {
			if a.Source != b.Source {
				return strings.Compare(a.Source, b.Source)
			}

			return strings.Compare(a.Id, b.Id)
		})

		allExtensions = append(allExtensions, filteredExtensions...)
	}

	return allExtensions, nil
}

// Install an extension by name and optional version
// If no version is provided, the latest version is installed
// Latest version is determined by the last element in the Versions slice
func (m *Manager) Install(ctx context.Context, id string, options *FilterOptions) (*ExtensionVersion, error) {
	if options == nil {
		options = &FilterOptions{}
	}

	installed, err := m.GetInstalled(LookupOptions{Id: id})
	if err == nil && installed != nil {
		return nil, fmt.Errorf("%s %w", id, ErrExtensionInstalled)
	}

	// Step 1: Find the extension by name
	extension, err := m.GetFromRegistry(ctx, id, options)
	if err != nil {
		return nil, err
	}

	// Step 2: Determine the version to install
	var selectedVersion *ExtensionVersion

	availableVersions := []*semver.Version{}
	availableVersionMap := map[*semver.Version]*ExtensionVersion{}

	// Create a map of available versions and sort them
	// This sorts the version from lowest to highest
	for _, extensionVersion := range extension.Versions {
		version, err := semver.NewVersion(extensionVersion.Version)
		if err != nil {
			return nil, fmt.Errorf("failed to parse version: %w", err)
		}

		availableVersionMap[version] = &extensionVersion
		availableVersions = append(availableVersions, version)
	}

	sort.Sort(semver.Collection(availableVersions))

	if options.Version == "" || options.Version == "latest" {
		latestVersion := availableVersions[len(availableVersions)-1]
		selectedVersion = availableVersionMap[latestVersion]
	} else {
		// Find the best match for the version constraint
		constraint, err := semver.NewConstraint(options.Version)
		if err != nil {
			return nil, fmt.Errorf("failed to parse version constraint: %w", err)
		}

		var bestMatch *semver.Version
		for _, v := range availableVersions {
			// Find the highest version that satisfies the constraint
			if constraint.Check(v) {
				bestMatch = v
			}
		}

		if bestMatch == nil {
			return nil, fmt.Errorf(
				"no matching version found for extension: %s and constraint: %s",
				id, options.Version,
			)
		}

		selectedVersion = availableVersionMap[bestMatch]
	}

	if selectedVersion == nil {
		return nil, fmt.Errorf("no compatible version found for extension: %s", id)
	}

	// Binaries are optional as long as dependencies are provided
	// This allows for extensions that are just extension packs
	if len(selectedVersion.Artifacts) == 0 && len(selectedVersion.Dependencies) == 0 {
		return nil, fmt.Errorf("no binaries or dependencies available for this version")
	}

	// Install dependencies
	if len(selectedVersion.Dependencies) > 0 {
		for _, dependency := range selectedVersion.Dependencies {
			dependencyInstallOptions := &FilterOptions{
				Version: dependency.Version,
				Source:  options.Source,
			}
			if _, err := m.Install(ctx, dependency.Id, dependencyInstallOptions); err != nil {
				if !errors.Is(err, ErrExtensionInstalled) {
					return nil, fmt.Errorf("failed to install dependency: %w", err)
				}
			}
		}
	}

	hasArtifact := len(selectedVersion.Artifacts) > 0
	var relativeExtensionPath string
	var targetPath string

	// Install the artifacts
	if hasArtifact {
		// Step 3: Find the artifact for the current OS
		artifact, err := findArtifactForCurrentOS(selectedVersion)
		if err != nil {
			return nil, fmt.Errorf("failed to find artifact for current OS: %w", err)
		}

		// Step 4: Download the artifact to a temp location
		tempFilePath, err := m.downloadArtifact(ctx, artifact.URL)
		if err != nil {
			return nil, fmt.Errorf("failed to download artifact: %w", err)
		}

		// Clean up the temp file after all scenarios
		defer os.Remove(tempFilePath)

		// Step 5: Validate the checksum if provided
		if err := validateChecksum(tempFilePath, artifact.Checksum); err != nil {
			return nil, fmt.Errorf("checksum validation failed: %w", err)
		}

		userConfigDir, err := config.GetUserConfigDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get user config directory: %w", err)
		}

		targetDir := filepath.Join(userConfigDir, "extensions", extension.Id)
		if err := os.MkdirAll(targetDir, os.ModePerm); err != nil {
			return nil, fmt.Errorf("failed to create target directory: %w", err)
		}

		// Step 6: Copy the artifact to the target directory
		// Check if artifact is a zip file, if so extract it to the target directory
		if strings.HasSuffix(tempFilePath, ".zip") {
			if err := rzip.ExtractToDirectory(tempFilePath, targetDir); err != nil {
				return nil, fmt.Errorf("failed to extract zip file: %w", err)
			}
		} else if strings.HasSuffix(tempFilePath, ".tar.gz") {
			if err := rzip.ExtractTarGzToDirectory(tempFilePath, targetDir); err != nil {
				return nil, fmt.Errorf("failed to extract tar.gz file: %w", err)
			}
		} else {
			targetPath = filepath.Join(targetDir, filepath.Base(tempFilePath))
			if err := copyFile(tempFilePath, targetPath); err != nil {
				return nil, fmt.Errorf("failed to copy artifact to target location: %w", err)
			}
		}

		entryPoint := selectedVersion.EntryPoint
		if platformEntryPoint, has := artifact.AdditionalMetadata["entryPoint"]; has {
			entryPoint = fmt.Sprint(platformEntryPoint)
		}
		if entryPoint == "" {
			switch runtime.GOOS {
			case "windows":
				entryPoint = fmt.Sprintf("%s.exe", extension.Id)
			default:
				entryPoint = extension.Id
			}
		}

		targetPath := filepath.Join(targetDir, entryPoint)

		// Need to set the executable permission for the binary
		// This change is specifically required for Linux but will apply consistently across all platforms
		if err := os.Chmod(targetPath, osutil.PermissionExecutableFile); err != nil {
			return nil, fmt.Errorf("failed to set executable permission: %w", err)
		}

		relativeExtensionPath, err = filepath.Rel(userConfigDir, targetPath)
		if err != nil {
			return nil, fmt.Errorf("failed to get relative path: %w", err)
		}
	}

	// Step 7: Update the user config with the installed extension
	extensions, err := m.ListInstalled()
	if err != nil {
		return nil, fmt.Errorf("failed to list installed extensions: %w", err)
	}

	extensions[id] = &Extension{
		Id:           id,
		Capabilities: selectedVersion.Capabilities,
		Namespace:    extension.Namespace,
		DisplayName:  extension.DisplayName,
		Description:  extension.Description,
		Version:      selectedVersion.Version,
		Usage:        selectedVersion.Usage,
		Path:         relativeExtensionPath,
		Source:       extension.Source,
	}

	if err := m.userConfig.Set(installedConfigKey, extensions); err != nil {
		return nil, fmt.Errorf("failed to set extensions section: %w", err)
	}

	if err := m.configManager.Save(m.userConfig); err != nil {
		return nil, fmt.Errorf("failed to save user config: %w", err)
	}

	log.Printf("Extension '%s' (version %s) installed successfully to %s\n", id, selectedVersion.Version, targetPath)

	return selectedVersion, nil
}

// Uninstall an extension by name
func (m *Manager) Uninstall(id string) error {
	// Get the installed extension
	extension, err := m.GetInstalled(LookupOptions{Id: id})
	if err != nil {
		return fmt.Errorf("failed to get installed extension: %w", err)
	}

	userConfigDir, err := config.GetUserConfigDir()
	if err != nil {
		return fmt.Errorf("failed to get user config directory: %w", err)
	}

	extensionDir := filepath.Join(userConfigDir, "extensions", extension.Id)
	if err := os.MkdirAll(extensionDir, os.ModePerm); err != nil {
		return fmt.Errorf("failed to create target directory: %w", err)
	}

	// Remove the extension artifacts when it exists
	_, err = os.Stat(extensionDir)
	if err == nil {
		if err := os.RemoveAll(extensionDir); err != nil {
			return fmt.Errorf("failed to remove extension: %w", err)
		}
	}

	// Update the user config
	extensions, err := m.ListInstalled()
	if err != nil {
		return fmt.Errorf("failed to list installed extensions: %w", err)
	}

	delete(extensions, id)

	if err := m.userConfig.Set(installedConfigKey, extensions); err != nil {
		return fmt.Errorf("failed to set extensions section: %w", err)
	}

	if err := m.configManager.Save(m.userConfig); err != nil {
		return fmt.Errorf("failed to save user config: %w", err)
	}

	log.Printf("Extension '%s' uninstalled successfully\n", id)
	return nil
}

// Upgrade upgrades the extension to the specified version
// This is a convenience method that uninstalls the existing extension and installs the new version
// If the version is not specified, the latest version is installed
func (m *Manager) Upgrade(ctx context.Context, extensionId string, options *FilterOptions) (*ExtensionVersion, error) {
	if options == nil {
		options = &FilterOptions{}
	}

	if err := m.Uninstall(extensionId); err != nil {
		return nil, fmt.Errorf("failed to uninstall extension: %w", err)
	}

	extensionVersion, err := m.Install(ctx, extensionId, options)
	if err != nil {
		return nil, fmt.Errorf("failed to install extension: %w", err)
	}

	return extensionVersion, nil
}

// Helper function to find the artifact for the current OS
func findArtifactForCurrentOS(version *ExtensionVersion) (*ExtensionArtifact, error) {
	if version.Artifacts == nil {
		return nil, fmt.Errorf("no binaries available for this version")
	}

	artifactVersions := []string{
		fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
		runtime.GOOS,
	}

	for _, artifactVersion := range artifactVersions {
		artifact, exists := version.Artifacts[artifactVersion]
		if exists {
			if artifact.URL == "" {
				return nil, fmt.Errorf("artifact URL is missing for platform: %s", artifactVersion)
			}

			return &artifact, nil
		}
	}

	return nil, fmt.Errorf("no artifact available for platform: %s", strings.Join(artifactVersions, ", "))
}

// downloadFile downloads a file from the given URL and saves it to a temporary directory using the filename from the URL.
func (m *Manager) downloadArtifact(ctx context.Context, artifactUrl string) (string, error) {
	if strings.HasPrefix(artifactUrl, "http://") || strings.HasPrefix(artifactUrl, "https://") {
		return m.downloadFromRemote(ctx, artifactUrl)
	}
	return m.copyFromLocalPath(artifactUrl)
}

// Handles downloading artifacts from HTTP/HTTPS URLs
func (m *Manager) downloadFromRemote(ctx context.Context, artifactUrl string) (string, error) {
	req, err := azruntime.NewRequest(ctx, http.MethodGet, artifactUrl)
	if err != nil {
		return "", err
	}

	resp, err := m.pipeline.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to download file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download file, status code: %d", resp.StatusCode)
	}

	filename := filepath.Base(artifactUrl)
	tempFilePath := filepath.Join(os.TempDir(), filename)

	tempFile, err := os.Create(tempFilePath)
	if err != nil {
		return "", fmt.Errorf("failed to create temporary file: %w", err)
	}
	defer tempFile.Close()

	_, err = io.Copy(tempFile, resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to write to temporary file: %w", err)
	}

	return tempFilePath, nil
}

// Handles copying artifacts from local or network file paths
func (m *Manager) copyFromLocalPath(artifactPath string) (string, error) {
	// If the path is relative, resolve it against the userConfigDir
	if !filepath.IsAbs(artifactPath) {
		userConfigDir, err := config.GetUserConfigDir()
		if err != nil {
			return "", fmt.Errorf("failed to get user config directory: %w", err)
		}

		artifactPath = filepath.Join(userConfigDir, artifactPath)
	}

	if _, err := os.Stat(artifactPath); os.IsNotExist(err) {
		return "", fmt.Errorf("file does not exist at path: %s", artifactPath)
	}

	filename := filepath.Base(artifactPath)
	tempFilePath := filepath.Join(os.TempDir(), filename)

	if err := copyFile(artifactPath, tempFilePath); err != nil {
		return "", fmt.Errorf("failed to copy file to temporary location: %w", err)
	}

	return tempFilePath, nil
}

func (tm *Manager) getSources(ctx context.Context, filter sourceFilterPredicate) ([]Source, error) {
	if tm.sources != nil {
		return tm.sources, nil
	}

	configs, err := tm.sourceManager.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed parsing extension sources: %w", err)
	}

	sources, err := tm.createSourcesFromConfig(ctx, configs, filter)
	if err != nil {
		return nil, fmt.Errorf("failed initializing extension sources: %w", err)
	}

	tm.sources = sources

	return tm.sources, nil
}

func (tm *Manager) createSourcesFromConfig(
	ctx context.Context,
	configs []*SourceConfig,
	filter sourceFilterPredicate,
) ([]Source, error) {
	sources := []Source{}

	for _, config := range configs {
		if filter != nil && !filter(config) {
			continue
		}

		source, err := tm.sourceManager.CreateSource(ctx, config)
		if err != nil {
			log.Printf("failed to create source: %v", err)
			continue
		}

		sources = append(sources, source)
	}

	return sources, nil
}

// validateChecksum validates the file at the given path against the expected checksum using the specified algorithm.
func validateChecksum(filePath string, checksum ExtensionChecksum) error {
	// Check if checksum or required fields are nil
	if checksum.Algorithm == "" && checksum.Value == "" {
		log.Println("Checksum algorithm and value is missing, skipping checksum validation")
		return nil
	}

	var hashAlgo hash.Hash

	// Select the hashing algorithm based on the input
	switch checksum.Algorithm {
	case "sha256":
		hashAlgo = sha256.New()
	case "sha512":
		hashAlgo = sha512.New()
	default:
		return fmt.Errorf("unsupported checksum algorithm: %s", checksum.Algorithm)
	}

	// Open the file for reading
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file for checksum validation: %w", err)
	}
	defer file.Close()

	// Compute the checksum
	if _, err := io.Copy(hashAlgo, file); err != nil {
		return fmt.Errorf("failed to compute checksum: %w", err)
	}

	// Convert the computed checksum to a hexadecimal string
	computedChecksum := hex.EncodeToString(hashAlgo.Sum(nil))

	// Compare the computed checksum with the expected checksum
	if computedChecksum != checksum.Value {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", checksum.Value, computedChecksum)
	}

	return nil
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
