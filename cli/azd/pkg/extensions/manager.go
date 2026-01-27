// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
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
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	azruntime "github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Masterminds/semver/v3"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/events"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
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
)

// FilterOptions is used to filter, lookup, and list extensions with various criteria
type FilterOptions struct {
	// Id is used to specify the id of the extension to install
	Id string
	// Namespace is used to specify the namespace of the extension to install
	Namespace string
	// Version is used to specify the version of the extension to install
	Version string
	// Source is used to specify the source of the extension to install
	Source string
	// Tags is used to specify the tags of the extension to install
	Tags []string
	// Capability is used to filter extensions by capability type
	Capability CapabilityType
	// Provider is used to filter extensions by provider name
	Provider string
}

type sourceFilterPredicate func(config *SourceConfig) bool
type extensionFilterPredicate func(extension *ExtensionMetadata) bool

// createExtensionFilter creates a comprehensive filter that checks ALL criteria with AND logic
func createExtensionFilter(options *FilterOptions) extensionFilterPredicate {
	return func(extension *ExtensionMetadata) bool {
		// Check Id filter
		if options.Id != "" {
			if !strings.EqualFold(extension.Id, options.Id) {
				return false
			}
		}

		// Check Namespace filter
		if options.Namespace != "" {
			if !strings.EqualFold(extension.Namespace, options.Namespace) {
				return false
			}
		}

		// Check Version filter - extension must have the specified version
		if options.Version != "" && options.Version != "latest" {
			hasVersion := slices.ContainsFunc(extension.Versions, func(version ExtensionVersion) bool {
				return strings.EqualFold(version.Version, options.Version)
			})
			if !hasVersion {
				return false
			}
		}

		// Check Source filter
		if options.Source != "" {
			if !strings.EqualFold(extension.Source, options.Source) {
				return false
			}
		}

		// Check Tags filter - extension must have ALL specified tags
		if len(options.Tags) > 0 {
			for _, optionTag := range options.Tags {
				hasTag := slices.ContainsFunc(extension.Tags, func(extensionTag string) bool {
					return strings.EqualFold(optionTag, extensionTag)
				})
				if !hasTag {
					return false
				}
			}
		}

		// Check Capability filter - extension must have at least one version with the specified capability
		if options.Capability != "" {
			hasCapability := slices.ContainsFunc(extension.Versions, func(version ExtensionVersion) bool {
				return slices.Contains(version.Capabilities, options.Capability)
			})
			if !hasCapability {
				return false
			}
		}

		// Check Provider filter - extension must have at least one version with a provider matching the specified name
		if options.Provider != "" {
			hasProvider := slices.ContainsFunc(extension.Versions, func(version ExtensionVersion) bool {
				return slices.ContainsFunc(version.Providers, func(provider Provider) bool {
					return strings.EqualFold(provider.Name, options.Provider)
				})
			})
			if !hasProvider {
				return false
			}
		}

		// All criteria passed
		return true
	}
}

// Manager is responsible for managing extensions
type Manager struct {
	sourceManager *SourceManager
	sources       []Source
	installed     map[string]*Extension
	configManager config.UserConfigManager
	userConfig    config.Config
	pipeline      azruntime.Pipeline

	// Lazy runner to avoid circular dependency issues since extension manager is used during command bootstrapping
	lazyRunner *lazy.Lazy[*Runner]
}

// NewManager creates a new extension manager
func NewManager(
	configManager config.UserConfigManager,
	sourceManager *SourceManager,
	lazyRunner *lazy.Lazy[*Runner],
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
		lazyRunner:    lazyRunner,
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

	m.installed = extensions

	return m.installed, nil
}

// GetInstalled retrieves an installed extension by filter criteria
func (m *Manager) GetInstalled(options FilterOptions) (*Extension, error) {
	extensions, err := m.ListInstalled()
	if err != nil {
		return nil, err
	}

	isExtensionMatch := createExtensionFilter(&options)

	// Convert installed extensions to ExtensionMetadata for filtering
	for _, extension := range extensions {
		// Create metadata representation for filtering
		metadata := &ExtensionMetadata{
			Id:        extension.Id,
			Namespace: extension.Namespace,
			Source:    extension.Source,
			Tags:      []string{}, // Installed extensions don't store tags
		}

		// Apply the same filter logic as other methods
		if isExtensionMatch(metadata) {
			return extension, nil
		}
	}

	return nil, ErrInstalledExtensionNotFound
}

// UpdateInstalled updates an installed extension's metadata in the config
func (m *Manager) UpdateInstalled(extension *Extension) error {
	extensions, err := m.ListInstalled()
	if err != nil {
		return fmt.Errorf("failed to list installed extensions: %w", err)
	}

	if _, exists := extensions[extension.Id]; !exists {
		return ErrInstalledExtensionNotFound
	}

	extensions[extension.Id] = extension

	if err := m.userConfig.Set(installedConfigKey, extensions); err != nil {
		return fmt.Errorf("failed to set extensions section: %w", err)
	}

	if err := m.configManager.Save(m.userConfig); err != nil {
		return fmt.Errorf("failed to save user config: %w", err)
	}

	// Invalidate cache so subsequent calls reflect the updated extension
	m.installed = nil

	return nil
}

func (m *Manager) FindExtensions(ctx context.Context, options *FilterOptions) ([]*ExtensionMetadata, error) {
	allExtensions := []*ExtensionMetadata{}

	if options == nil {
		options = &FilterOptions{}
	}

	var sourceFilterPredicate sourceFilterPredicate
	if options.Source != "" {
		sourceFilterPredicate = func(config *SourceConfig) bool {
			return strings.EqualFold(config.Name, options.Source)
		}
	}

	// Use the centralized extension filter
	extensionFilter := createExtensionFilter(options)

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
			if extensionFilter(extension) {
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

// Install an extension from metadata with optional version preference
// If no version is provided, the latest version is installed
// Latest version is determined by the last element in the Versions slice
func (m *Manager) Install(
	ctx context.Context,
	extension *ExtensionMetadata,
	versionPreference string,
) (extVersion *ExtensionVersion, err error) {
	if extension == nil {
		return nil, fmt.Errorf("extension metadata cannot be nil")
	}

	ctx, span := tracing.Start(ctx, events.ExtensionInstallEvent)
	defer func() {
		span.EndWithStatus(err)
	}()

	installed, err := m.GetInstalled(FilterOptions{Id: extension.Id})
	if err == nil && installed != nil {
		return nil, fmt.Errorf("%s %w", extension.Id, ErrExtensionInstalled)
	}

	// Step 1: Determine the version to install
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

	if versionPreference == "" || versionPreference == "latest" {
		latestVersion := availableVersions[len(availableVersions)-1]
		selectedVersion = availableVersionMap[latestVersion]
	} else {
		// Find the best match for the version constraint
		constraint, err := semver.NewConstraint(versionPreference)
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
				extension.Id, versionPreference,
			)
		}

		selectedVersion = availableVersionMap[bestMatch]
	}

	if selectedVersion == nil {
		return nil, fmt.Errorf("no compatible version found for extension: %s", extension.Id)
	}

	// Binaries are optional as long as dependencies are provided
	// This allows for extensions that are just extension packs
	if len(selectedVersion.Artifacts) == 0 && len(selectedVersion.Dependencies) == 0 {
		return nil, fmt.Errorf("no binaries or dependencies available for this version")
	}

	// Install dependencies
	if len(selectedVersion.Dependencies) > 0 {
		for _, dependency := range selectedVersion.Dependencies {
			// Find the dependency extension metadata first
			dependencyOptions := &FilterOptions{
				Id:      dependency.Id,
				Version: dependency.Version,
				Source:  extension.Source, // Use same source as parent extension
			}

			dependencyMatches, err := m.FindExtensions(ctx, dependencyOptions)
			if err != nil {
				return nil, fmt.Errorf("failed to find dependency %s: %w", dependency.Id, err)
			}

			if len(dependencyMatches) == 0 {
				return nil, fmt.Errorf("dependency %s not found", dependency.Id)
			}

			if len(dependencyMatches) > 1 {
				return nil, fmt.Errorf("dependency %s found in multiple sources, specify exact source", dependency.Id)
			}

			dependencyMetadata := dependencyMatches[0]

			if _, err := m.Install(ctx, dependencyMetadata, dependency.Version); err != nil {
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

	extensions[extension.Id] = &Extension{
		Id:           extension.Id,
		Capabilities: selectedVersion.Capabilities,
		Namespace:    extension.Namespace,
		DisplayName:  extension.DisplayName,
		Description:  extension.Description,
		Version:      selectedVersion.Version,
		Usage:        selectedVersion.Usage,
		Path:         relativeExtensionPath,
		Source:       extension.Source,
		Providers:    selectedVersion.Providers,
		McpConfig:    selectedVersion.McpConfig,
	}

	if err := m.userConfig.Set(installedConfigKey, extensions); err != nil {
		return nil, fmt.Errorf("failed to set extensions section: %w", err)
	}

	if err := m.configManager.Save(m.userConfig); err != nil {
		return nil, fmt.Errorf("failed to save user config: %w", err)
	}

	span.SetAttributes(
		fields.ExtensionId.String(extension.Id),
		fields.ExtensionVersion.String(selectedVersion.Version))

	log.Printf(
		"Extension '%s' (version %s) installed successfully to %s\n",
		extension.Id,
		selectedVersion.Version,
		targetPath,
	)

	// Fetch and cache metadata if extension supports it
	installedExtension := extensions[extension.Id]
	if installedExtension.HasCapability(MetadataCapability) {
		if err := m.fetchAndCacheMetadata(ctx, installedExtension); err != nil {
			// Log warning but don't fail installation
			log.Printf("Warning: Failed to fetch extension metadata for '%s': %v\n", extension.Id, err)
		}
	}

	return selectedVersion, nil
}

// Uninstall an extension by name
func (m *Manager) Uninstall(id string) error {
	// Get the installed extension
	extension, err := m.GetInstalled(FilterOptions{Id: id})
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
func (m *Manager) Upgrade(
	ctx context.Context,
	extension *ExtensionMetadata,
	versionPreference string,
) (*ExtensionVersion, error) {
	if extension == nil {
		return nil, fmt.Errorf("extension metadata cannot be nil")
	}

	if err := m.Uninstall(extension.Id); err != nil {
		return nil, fmt.Errorf("failed to uninstall extension: %w", err)
	}

	extensionVersion, err := m.Install(ctx, extension, versionPreference)
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

const (
	metadataFileName    = "metadata.json"
	metadataCommandName = "metadata"
	metadataTimeout     = 10 * time.Second
)

// fetchAndCacheMetadata fetches metadata from an extension and caches it to disk.
// Caller must verify that extension has MetadataCapability before calling.
// Returns nil error if metadata was successfully fetched and cached.
func (m *Manager) fetchAndCacheMetadata(
	ctx context.Context,
	extension *Extension,
) error {
	userConfigDir, err := config.GetUserConfigDir()
	if err != nil {
		return fmt.Errorf("failed to get user config directory: %w", err)
	}

	extensionDir := filepath.Join(userConfigDir, "extensions", extension.Id)
	metadataPath := filepath.Join(extensionDir, metadataFileName)

	// Check if metadata.json already exists (pre-packaged)
	if _, err := os.Stat(metadataPath); err == nil {
		log.Printf("Extension '%s' has pre-packaged metadata.json, skipping metadata command", extension.Id)
		return nil
	}

	// Execute metadata command with timeout using the runner
	cmdCtx, cancel := context.WithTimeout(ctx, metadataTimeout)
	defer cancel()

	runner, err := m.lazyRunner.GetValue()
	if err != nil {
		return fmt.Errorf("failed to resolve extension runner: %w", err)
	}

	runResult, err := runner.Invoke(cmdCtx, extension, &InvokeOptions{
		Args: []string{metadataCommandName},
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("metadata command timed out after %v", metadataTimeout)
		}
		return fmt.Errorf("metadata command failed: %w", err)
	}
	if runResult.ExitCode != 0 {
		return fmt.Errorf("metadata command exited with code %d", runResult.ExitCode)
	}

	// Parse metadata JSON from stdout
	var metadata ExtensionCommandMetadata
	if err := json.Unmarshal([]byte(runResult.Stdout), &metadata); err != nil {
		return fmt.Errorf("failed to parse metadata JSON: %w", err)
	}

	// Validate metadata
	if metadata.ID != extension.Id {
		return fmt.Errorf(
			"metadata ID '%s' does not match extension ID '%s'",
			metadata.ID,
			extension.Id,
		)
	}

	// Write metadata to cache
	metadataJSON, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	if err := os.WriteFile(metadataPath, metadataJSON, 0600); err != nil {
		return fmt.Errorf("failed to write metadata file: %w", err)
	}

	log.Printf("Extension '%s' metadata cached successfully", extension.Id)
	return nil
}

// LoadMetadata loads cached metadata for an extension
func (m *Manager) LoadMetadata(extensionId string) (*ExtensionCommandMetadata, error) {
	userConfigDir, err := config.GetUserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get user config directory: %w", err)
	}

	extensionDir := filepath.Join(userConfigDir, "extensions", extensionId)
	metadataPath := filepath.Join(extensionDir, metadataFileName)

	data, err := os.ReadFile(metadataPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("metadata not found for extension '%s'", extensionId)
		}
		return nil, fmt.Errorf("failed to read metadata file: %w", err)
	}

	var metadata ExtensionCommandMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, fmt.Errorf("failed to parse metadata JSON: %w", err)
	}

	return &metadata, nil
}

// DeleteMetadata removes cached metadata for an extension
func (m *Manager) DeleteMetadata(extensionId string) error {
	userConfigDir, err := config.GetUserConfigDir()
	if err != nil {
		return fmt.Errorf("failed to get user config directory: %w", err)
	}

	extensionDir := filepath.Join(userConfigDir, "extensions", extensionId)
	metadataPath := filepath.Join(extensionDir, metadataFileName)

	if err := os.Remove(metadataPath); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove metadata file: %w", err)
		}
		// File doesn't exist, which is fine
	}

	return nil
}

// MetadataExists checks if cached metadata exists for an extension
func (m *Manager) MetadataExists(extensionId string) bool {
	userConfigDir, err := config.GetUserConfigDir()
	if err != nil {
		return false
	}

	extensionDir := filepath.Join(userConfigDir, "extensions", extensionId)
	metadataPath := filepath.Join(extensionDir, metadataFileName)

	_, err = os.Stat(metadataPath)
	return err == nil
}

// LoadMetadataFromDir loads extension metadata from a specific config directory.
// This is a standalone function useful for testing or when a Manager instance is not available.
func LoadMetadataFromDir(configDir, extensionId string) (*ExtensionCommandMetadata, error) {
	extensionDir := filepath.Join(configDir, "extensions", extensionId)
	metadataPath := filepath.Join(extensionDir, metadataFileName)

	data, err := os.ReadFile(metadataPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("metadata not found for extension '%s'", extensionId)
		}
		return nil, fmt.Errorf("failed to read metadata file: %w", err)
	}

	var metadata ExtensionCommandMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, fmt.Errorf("failed to parse metadata JSON: %w", err)
	}

	return &metadata, nil
}
