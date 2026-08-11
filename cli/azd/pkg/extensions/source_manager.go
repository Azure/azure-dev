// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
)

// SourceKind represents the type of extension source.
type SourceKind string

const (
	SourceKindFile SourceKind = "file"
	SourceKindUrl  SourceKind = "url"
	// SourceKindBundle is a self-contained extension bundle extracted from a
	// portable .zip. It behaves like a file source but anchors relative
	// artifact paths to the extracted bundle directory.
	SourceKindBundle SourceKind = "bundle"

	baseConfigKey      string = "extension.sources"
	installedConfigKey string = "extension.installed"

	// SourceNameMaxLength is the maximum number of ASCII characters in an extension source name.
	SourceNameMaxLength = 64

	// BundleSourceName is the reserved source recorded for extensions installed
	// from a self-contained bundle (.zip). The bundle's own source is ephemeral
	// and removed after install, so the extension is marked with this name; it has
	// no live registry to track updates against and cannot be user-configured.
	BundleSourceName string = "bundle"
)

var (
	ErrSourceNotFound    = errors.New("extension source not found")
	ErrSourceExists      = errors.New("extension source already exists")
	ErrSourceNameInvalid = errors.New("invalid extension source name")
	ErrSourceTypeInvalid = errors.New("invalid extension source type")
	ErrSourceReserved    = errors.New("extension source name is reserved")
)

// SourceConfig represents the configuration for an extension source.
type SourceConfig struct {
	Name     string     `json:"name,omitempty"`
	Type     SourceKind `json:"type,omitempty"`
	Location string     `json:"location,omitempty"`
}

// SourceManager manages extension sources.
type SourceManager struct {
	serviceLocator ioc.ServiceLocator
	configManager  config.UserConfigManager
	transport      policy.Transporter
}

func NewSourceManager(
	serviceLocator ioc.ServiceLocator,
	configManager config.UserConfigManager,
	transport policy.Transporter,
) *SourceManager {
	return &SourceManager{
		serviceLocator: serviceLocator,
		configManager:  configManager,
		transport:      transport,
	}
}

// Get returns an extension source by name.
func (sm *SourceManager) Get(ctx context.Context, name string) (*SourceConfig, error) {
	sources, err := sm.List(ctx)
	if err != nil {
		return nil, err
	}

	for _, source := range sources {
		if strings.EqualFold(source.Name, name) {
			return source, nil
		}
	}

	return nil, fmt.Errorf("%w, '%s'", ErrSourceNotFound, name)
}

// Add adds a new extension source.
func (sm *SourceManager) Add(ctx context.Context, name string, source *SourceConfig) error {
	if err := ValidateSourceName(name); err != nil {
		return err
	}

	existing, err := sm.Get(ctx, name)
	if existing != nil && err == nil {
		return fmt.Errorf("extension source '%s' already exists, %w", name, ErrSourceExists)
	}
	if err != nil && !errors.Is(err, ErrSourceNotFound) {
		return fmt.Errorf("checking extension source '%s': %w", name, err)
	}

	source.Name = name

	return sm.addInternal(source)
}

// Remove removes an extension source.
func (sm *SourceManager) Remove(ctx context.Context, name string) error {
	config, err := sm.configManager.Load()
	if err != nil {
		return fmt.Errorf("unable to load user configuration: %w", err)
	}

	rawSources, ok := config.Get(baseConfigKey)
	if !ok {
		return fmt.Errorf("extension source '%s' not found, %w", name, ErrSourceNotFound)
	}

	sourceMap, ok := rawSources.(map[string]any)
	if !ok {
		return fmt.Errorf("unable to parse extension sources")
	}

	matches := sourcePathsMatchingName(sourceMap, name, false)
	if len(matches) == 0 {
		matches = sourcePathsMatchingName(sourceMap, name, true)
	}
	if len(matches) == 0 {
		return fmt.Errorf("extension source '%s' not found, %w", name, ErrSourceNotFound)
	}
	if len(matches) > 1 {
		return fmt.Errorf("extension source name '%s' matches multiple configured sources", name)
	}

	deleteSourcePath(sourceMap, matches[0])
	if err := config.Set(baseConfigKey, sourceMap); err != nil {
		return fmt.Errorf("unable to remove extension source '%s': %w", name, err)
	}

	err = sm.configManager.Save(config)
	if err != nil {
		return fmt.Errorf("updating user configuration: %w", err)
	}

	return nil
}

// List returns a list of extension sources.
func (sm *SourceManager) List(ctx context.Context) ([]*SourceConfig, error) {
	config, err := sm.configManager.Load()
	if err != nil {
		return nil, fmt.Errorf("unable to load user configuration: %w", err)
	}

	allSourceConfigs := []*SourceConfig{}

	rawSources, ok := config.Get(baseConfigKey)
	if ok {
		sourceMap, ok := rawSources.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("unable to parse extension sources")
		}
		sourceEntries, err := configuredSourceEntries(sourceMap)
		if err != nil {
			return nil, err
		}
		for _, entry := range sourceEntries {
			if err := validateConfiguredSource(entry.name, entry.config); err != nil {
				return nil, err
			}

			allSourceConfigs = append(allSourceConfigs, entry.config)
		}
	} else {
		defaultSource := &SourceConfig{
			Name:     "azd",
			Type:     SourceKindUrl,
			Location: extensionRegistryUrl,
		}

		if err := sm.addInternal(defaultSource); err != nil {
			return nil, fmt.Errorf("unable to default template source '%s': %w", defaultSource.Name, err)
		}

		allSourceConfigs = append(allSourceConfigs, defaultSource)
	}

	slices.SortFunc(allSourceConfigs, func(a, b *SourceConfig) int {
		return strings.Compare(a.Name, b.Name)
	})

	return allSourceConfigs, nil
}

// Source returns a hydrated extension source for the current config.
func (sm *SourceManager) CreateSource(ctx context.Context, config *SourceConfig) (Source, error) {
	var source Source
	var err error

	if config.Name == "" {
		return nil, errors.New("extension source name is required")
	}

	if config.Location == "" {
		return nil, errors.New("extension source location is required")
	}

	switch config.Type {
	case SourceKindFile:
		source, err = newFileSource(config.Name, config.Location)
	case SourceKindBundle:
		source, err = newBundleSource(config.Name, config.Location)
	case SourceKindUrl:
		source, err = newUrlSource(ctx, config.Name, config.Location, sm.transport)
	default:
		err = sm.serviceLocator.ResolveNamed(string(config.Type), &source)
		if err != nil {
			err = fmt.Errorf("%w, '%s', %w", ErrSourceTypeInvalid, config.Type, err)
		}
	}

	if err != nil {
		return nil, fmt.Errorf("unable to create extension source '%s': %w", config.Name, err)
	}

	return source, nil
}

// addInternal adds a new extension source to the user configuration.
func (sm *SourceManager) addInternal(source *SourceConfig) error {
	config, err := sm.configManager.Load()
	if err != nil {
		return fmt.Errorf("unable to load user configuration: %w", err)
	}

	path := fmt.Sprintf("%s.%s", baseConfigKey, source.Name)
	err = config.Set(path, source)
	if err != nil {
		return fmt.Errorf("unable to add extension source '%s': %w", source.Name, err)
	}

	err = sm.configManager.Save(config)
	if err != nil {
		return fmt.Errorf("updating user configuration: %w", err)
	}

	return nil
}

// ValidateSourceName validates a source name for configuration and command-line use.
func ValidateSourceName(name string) error {
	if len(name) == 0 || len(name) > SourceNameMaxLength {
		return fmt.Errorf(
			"%w: must be between 1 and %d characters",
			ErrSourceNameInvalid,
			SourceNameMaxLength,
		)
	}
	if strings.EqualFold(name, BundleSourceName) {
		return fmt.Errorf(
			"'%s' is reserved for extensions installed from a self-contained bundle, %w",
			BundleSourceName,
			ErrSourceReserved,
		)
	}
	for i, char := range []byte(name) {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' {
			continue
		}
		if (char == '-' || char == '_') && i > 0 && i < len(name)-1 {
			continue
		}
		return fmt.Errorf(
			"%w: must use lowercase ASCII letters, digits, hyphens, or underscores "+
				"and begin and end with a letter or digit",
			ErrSourceNameInvalid,
		)
	}

	return nil
}

func validateConfiguredSource(key string, source *SourceConfig) error {
	if err := ValidateSourceName(key); err != nil {
		return fmt.Errorf(
			"configured extension source %q has an invalid name: %w; run "+
				"`azd extension source remove <source-name>` using the exact name shown above, "+
				"then add it again with a valid name",
			key,
			err,
		)
	}
	if source == nil {
		return fmt.Errorf("configured extension source %q is empty", key)
	}
	if err := ValidateSourceName(source.Name); err != nil {
		return fmt.Errorf(
			"configured extension source %q has an invalid stored name %q: %w; run "+
				"`azd extension source remove <source-name>` using the source key shown above, "+
				"then add it again with a valid name",
			key,
			source.Name,
			err,
		)
	}
	if source.Name != key {
		return fmt.Errorf(
			"configured extension source key %q does not match its stored name %q; run "+
				"`azd extension source remove <source-name>` using the source key shown above, "+
				"then add it again",
			key,
			source.Name,
		)
	}
	return nil
}

type configuredSourceEntry struct {
	name   string
	path   []string
	config *SourceConfig
}

func configuredSourceEntries(sourceMap map[string]any) ([]configuredSourceEntry, error) {
	entries := []configuredSourceEntry{}
	if err := walkConfiguredSources(sourceMap, nil, func(entry configuredSourceEntry) {
		entries = append(entries, entry)
	}); err != nil {
		return nil, err
	}
	return entries, nil
}

func walkConfiguredSources(
	sourceMap map[string]any,
	parentPath []string,
	visit func(configuredSourceEntry),
) error {
	for _, key := range slices.Sorted(maps.Keys(sourceMap)) {
		rawSource := sourceMap[key]
		path := append(slices.Clone(parentPath), key)
		name := strings.Join(path, ".")

		if nested, ok := rawSource.(map[string]any); ok && !isSourceConfigMap(nested) {
			if err := walkConfiguredSources(nested, path, visit); err != nil {
				return err
			}
			continue
		}

		var sourceConfig *SourceConfig
		jsonBytes, err := json.Marshal(rawSource)
		if err != nil {
			return fmt.Errorf("unable to parse source '%s': %w", name, err)
		}
		if err := json.Unmarshal(jsonBytes, &sourceConfig); err != nil {
			return fmt.Errorf("unable to parse source '%s': %w", name, err)
		}

		visit(configuredSourceEntry{
			name:   name,
			path:   path,
			config: sourceConfig,
		})
	}

	return nil
}

func isSourceConfigMap(value map[string]any) bool {
	_, hasName := value["name"].(string)
	_, hasType := value["type"].(string)
	_, hasLocation := value["location"].(string)
	return hasName || hasType || hasLocation
}

func sourcePathsMatchingName(sourceMap map[string]any, name string, ignoreCase bool) [][]string {
	matches := [][]string{}
	equal := func(a, b string) bool {
		if ignoreCase {
			return strings.EqualFold(a, b)
		}
		return a == b
	}

	_ = walkConfiguredSources(sourceMap, nil, func(entry configuredSourceEntry) {
		if equal(entry.name, name) || entry.config != nil && equal(entry.config.Name, name) {
			matches = append(matches, entry.path)
		}
	})

	return matches
}

func deleteSourcePath(sourceMap map[string]any, path []string) bool {
	if len(path) == 1 {
		delete(sourceMap, path[0])
		return len(sourceMap) == 0
	}

	nested, ok := sourceMap[path[0]].(map[string]any)
	if !ok {
		return false
	}
	if deleteSourcePath(nested, path[1:]) {
		delete(sourceMap, path[0])
	}
	return len(sourceMap) == 0
}
