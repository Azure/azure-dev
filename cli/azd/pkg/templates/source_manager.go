package templates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/resources"
)

const baseConfigKey string = "template.sources"

var (
	SourceDefault = &SourceConfig{
		Key:  "default",
		Name: "Default",
		Type: SourceKindResource,
	}

	SourceAwesomeAzd = &SourceConfig{
		Key:      "awesome-azd",
		Name:     "Awesome AZD",
		Type:     SourceKindAwesomeAzd,
		Location: "https://aka.ms/awesome-azd/templates.json",
	}

	WellKnownSources = map[string]*SourceConfig{
		SourceDefault.Key:    SourceDefault,
		SourceAwesomeAzd.Key: SourceAwesomeAzd,
	}

	ErrSourceNotFound    = errors.New("template source not found")
	ErrSourceExists      = errors.New("template source already exists")
	ErrSourceTypeInvalid = errors.New("invalid template source type")
)

// SourceOptions defines options for the SourceManager.
type SourceOptions struct {
	// List of default template sources to use for listing templates
	DefaultSources []*SourceConfig
	// Whether to load template sources from azd configuration
	LoadConfiguredSources bool
}

// NewSourceOptions creates a new SourceOptions with default values
func NewSourceOptions() *SourceOptions {
	return &SourceOptions{
		DefaultSources:        []*SourceConfig{},
		LoadConfiguredSources: true,
	}
}

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
	options        *SourceOptions
	serviceLocator ioc.ServiceLocator
	configManager  config.UserConfigManager
	httpClient     httputil.HttpClient
}

// NewSourceManager creates a new SourceManager.
func NewSourceManager(
	options *SourceOptions,
	serviceLocator ioc.ServiceLocator,
	configManager config.UserConfigManager,
	httpClient httputil.HttpClient,
) SourceManager {
	if options == nil {
		options = NewSourceOptions()
	}

	return &sourceManager{
		options:        options,
		serviceLocator: serviceLocator,
		configManager:  configManager,
		httpClient:     httpClient,
	}
}

// List returns a list of template sources.
func (sm *sourceManager) List(ctx context.Context) ([]*SourceConfig, error) {
	config, err := sm.configManager.Load()
	if err != nil {
		return nil, fmt.Errorf("unable to load user configuration: %w", err)
	}

	allSourceConfigs := []*SourceConfig{}

	if sm.options.DefaultSources != nil && len(sm.options.DefaultSources) > 0 {
		allSourceConfigs = append(allSourceConfigs, sm.options.DefaultSources...)
	}

	if !sm.options.LoadConfiguredSources {
		return allSourceConfigs, nil
	}

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
			allSourceConfigs = append(allSourceConfigs, sourceConfig)
		}
	} else {
		// In the use case where template sources have never been configured,
		// add Awesome-Azd as the default template source.
		if err := sm.addInternal(ctx, SourceAwesomeAzd.Key, SourceAwesomeAzd); err != nil {
			return nil, fmt.Errorf("unable to default template source '%s': %w", SourceAwesomeAzd.Key, err)
		}
		allSourceConfigs = append(allSourceConfigs, SourceAwesomeAzd)
	}

	return allSourceConfigs, nil
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

	return nil, fmt.Errorf("%w, '%s'", ErrSourceNotFound, key)
}

// Add adds a new template source at the specified key and configuration
func (sm *sourceManager) Add(ctx context.Context, key string, source *SourceConfig) error {
	newKey := normalizeKey(key)

	existing, err := sm.Get(ctx, newKey)
	if existing != nil && err == nil {
		return fmt.Errorf("template source '%s' already exists, %w", key, ErrSourceExists)
	}

	if source.Name == "" {
		source.Name = key
	}

	source.Key = newKey

	return sm.addInternal(ctx, source.Key, source)
}

// Remove removes a template source by the specified key.
func (sm *sourceManager) Remove(ctx context.Context, key string) error {
	key = normalizeKey(key)

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
	case SourceKindFile:
		source, err = NewFileTemplateSource(config.Name, config.Location)
	case SourceKindUrl:
		source, err = NewUrlTemplateSource(ctx, config.Name, config.Location, sm.httpClient)
	case SourceKindAwesomeAzd:
		source, err = NewAwesomeAzdTemplateSource(ctx, SourceAwesomeAzd.Name, SourceAwesomeAzd.Location, sm.httpClient)
	case SourceKindResource:
		source, err = NewJsonTemplateSource(SourceDefault.Name, string(resources.TemplatesJson))
	default:
		err = sm.serviceLocator.ResolveNamed(string(config.Type), &source)
		if err != nil {
			err = fmt.Errorf("%w, '%s', %w", ErrSourceTypeInvalid, config.Type, err)
		}
	}

	if err != nil {
		return nil, fmt.Errorf("unable to create template source '%s': %w", config.Key, err)
	}

	return source, nil
}

func (sm *sourceManager) addInternal(ctx context.Context, key string, source *SourceConfig) error {
	config, err := sm.configManager.Load()
	if err != nil {
		return fmt.Errorf("unable to load user configuration: %w", err)
	}

	path := fmt.Sprintf("%s.%s", baseConfigKey, source.Key)
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

func normalizeKey(key string) string {
	key = strings.ToLower(key)
	key = strings.ReplaceAll(key, " ", "-")

	return key
}
