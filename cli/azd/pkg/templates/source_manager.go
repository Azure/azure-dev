package templates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
	"github.com/azure/azure-dev/cli/azd/resources"
)

const baseConfigKey string = "template.sources"

var (
	SourceDefault = &SourceConfig{
		Key:  "default",
		Name: "Default",
		Type: SourceResource,
	}

	SourceAwesomeAzd = &SourceConfig{
		Key:      "awesome-azd",
		Name:     "Awesome AZD",
		Type:     SourceUrl,
		Location: "https://raw.githubusercontent.com/wbreza/azure-dev/template-source/cli/azd/resources/awesome-templates.json",
	}

	WellKnownSources = map[string]*SourceConfig{
		SourceDefault.Key:    SourceDefault,
		SourceAwesomeAzd.Key: SourceAwesomeAzd,
	}

	ErrSourceNotFound = errors.New("template source not found")
	ErrSourceExists   = errors.New("template source already exists")
)

// SourceManager manages template sources used in azd template list and azd init experiences.
type SourceManager interface {
	// List returns a list of template sources.
	List(ctx context.Context) ([]*SourceConfig, error)
	// Get returns a template source by name.
	Get(ctx context.Context, name string) (*SourceConfig, error)
	// Add adds a new template source.
	Add(ctx context.Context, key string, source *SourceConfig) error
	// Remove removes a template source.
	Remove(ctx context.Context, name string) error
	// CreateSource creates a new template source from a source configuration
	CreateSource(ctx context.Context, source *SourceConfig) (Source, error)
}

type sourceManager struct {
	configManager config.UserConfigManager
	httpClient    httputil.HttpClient
}

// NewSourceManager creates a new SourceManager.
func NewSourceManager(configManager config.UserConfigManager, httpClient httputil.HttpClient) SourceManager {
	return &sourceManager{
		configManager: configManager,
		httpClient:    httpClient,
	}
}

// List returns a list of template sources.
func (sm *sourceManager) List(ctx context.Context) ([]*SourceConfig, error) {
	config, err := sm.configManager.Load()
	if err != nil {
		return nil, fmt.Errorf("unable to load user configuration: %w", err)
	}

	sourceConfigs := []*SourceConfig{}
	rawSources, ok := config.Get(baseConfigKey)
	if ok {
		sourceMap := rawSources.(map[string]interface{})
		for key, rawSource := range sourceMap {
			var sourceConfig *SourceConfig

			if wellKnownSource, ok := WellKnownSources[key]; ok {
				sourceConfig = wellKnownSource
			} else {
				jsonBytes, err := json.Marshal(rawSource)
				if err != nil {
					return nil, fmt.Errorf("unable to parse source '%s': %w", key, err)
				}

				err = json.Unmarshal(jsonBytes, &sourceConfig)
				if err != nil {
					return nil, fmt.Errorf("unable to parse source '%s': %w", key, err)
				}
			}

			sourceConfig.Key = key
			sourceConfigs = append(sourceConfigs, sourceConfig)
		}
	}

	// If not sources have been registered, add the default source.
	if len(sourceConfigs) == 0 {
		sourceConfigs = append(sourceConfigs, SourceDefault)
	}

	return sourceConfigs, nil
}

// Get returns a template source by key.
func (sm *sourceManager) Get(ctx context.Context, key string) (*SourceConfig, error) {
	sources, err := sm.List(ctx)
	if err != nil {
		return nil, err
	}

	for _, source := range sources {
		if source.Key == key {
			return source, nil
		}
	}

	return nil, fmt.Errorf("template source '%s' not found, %w", key, ErrSourceNotFound)
}

// Add adds a new template source at the specified key and configuration
func (sm *sourceManager) Add(ctx context.Context, key string, source *SourceConfig) error {
	existing, err := sm.Get(ctx, key)
	if existing != nil && err == nil {
		return fmt.Errorf("template source '%s' already exists, %w", key, ErrSourceExists)
	}

	config, err := sm.configManager.Load()
	if err != nil {
		return fmt.Errorf("unable to load user configuration: %w", err)
	}

	path := fmt.Sprintf("%s.%s", baseConfigKey, key)
	err = config.Set(path, source)
	if err != nil {
		return fmt.Errorf("unable to add template source '%s': %w", source.Key, err)
	}

	err = sm.configManager.Save(config)
	if err != nil {
		return fmt.Errorf("updating user configuration: %w", err)
	}

	return nil
}

// Remove removes a template source by the specified key.
func (sm *sourceManager) Remove(ctx context.Context, key string) error {
	_, err := sm.Get(ctx, key)
	if err != nil && errors.Is(err, ErrSourceNotFound) {
		return fmt.Errorf("template source '%s' not found, %w", key, err)
	}

	config, err := sm.configManager.Load()
	if err != nil {
		return fmt.Errorf("unable to load user configuration: %w", err)
	}

	path := fmt.Sprintf("%s.%s", baseConfigKey, key)
	_, ok := config.Get(path)
	if !ok {
		return nil
	}

	err = config.Unset(path)
	if err != nil {
		return fmt.Errorf("unable to remove template source '%s': %w", key, err)
	}

	err = sm.configManager.Save(config)
	if err != nil {
		return fmt.Errorf("updating user configuration: %w", err)
	}

	return nil
}

// Source returns a hydrated template source for the current config.
func (sm *sourceManager) CreateSource(ctx context.Context, config *SourceConfig) (Source, error) {
	var source Source
	var err error

	switch config.Type {
	case SourceFile:
		source, err = NewFileTemplateSource(config.Name, config.Location)
	case SourceUrl:
		source, err = NewUrlTemplateSource(ctx, config.Name, config.Location, sm.httpClient)
	case SourceResource:
		source, err = NewJsonTemplateSource(config.Name, string(resources.TemplatesJson))
	default:
		err = fmt.Errorf("unknown template source type '%s'", config.Type)
	}

	if err != nil {
		return nil, fmt.Errorf("unable to create template source '%s': %w", config.Key, err)
	}

	return source, nil
}
