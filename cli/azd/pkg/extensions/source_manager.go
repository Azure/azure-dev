// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

	baseConfigKey      string = "extension.sources"
	installedConfigKey string = "extension.installed"
)

var (
	ErrSourceNotFound    = errors.New("extension source not found")
	ErrSourceExists      = errors.New("extension source already exists")
	ErrSourceTypeInvalid = errors.New("invalid extension source type")
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
	newKey := normalizeKey(name)

	existing, err := sm.Get(ctx, newKey)
	if existing != nil && err == nil {
		return fmt.Errorf("extension source '%s' already exists, %w", name, ErrSourceExists)
	}

	if source.Name == "" {
		source.Name = name
	}

	source.Name = newKey

	return sm.addInternal(source)
}

// Remove removes an extension source.
func (sm *SourceManager) Remove(ctx context.Context, name string) error {
	name = normalizeKey(name)

	_, err := sm.Get(ctx, name)
	if err != nil && errors.Is(err, ErrSourceNotFound) {
		return fmt.Errorf("extension source '%s' not found, %w", name, err)
	}

	config, err := sm.configManager.Load()
	if err != nil {
		return fmt.Errorf("unable to load user configuration: %w", err)
	}

	path := fmt.Sprintf("%s.%s", baseConfigKey, name)
	_, ok := config.Get(path)
	if !ok {
		return nil
	}

	err = config.Unset(path)
	if err != nil {
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
		sourceMap := rawSources.(map[string]interface{})
		for key, rawSource := range sourceMap {
			var sourceConfig *SourceConfig

			jsonBytes, err := json.Marshal(rawSource)
			if err != nil {
				return nil, fmt.Errorf("unable to parse source '%s': %w", key, err)
			}

			err = json.Unmarshal(jsonBytes, &sourceConfig)
			if err != nil {
				return nil, fmt.Errorf("unable to parse source '%s': %w", key, err)
			}

			allSourceConfigs = append(allSourceConfigs, sourceConfig)
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

// normalizeKey normalizes a key for use in the configuration.
func normalizeKey(key string) string {
	key = strings.ToLower(key)
	key = strings.ReplaceAll(key, " ", "-")

	return key
}
