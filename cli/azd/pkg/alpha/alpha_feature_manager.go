package alpha

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
)

// FeatureManager provides operations for handling features within the application which are in alpha mode.
type FeatureManager struct {
	configManager   config.UserConfigManager
	userConfigCache config.Config
	// used for mocking alpha features on testing
	alphaFeaturesResolver func() []Feature
	withSync              *sync.Once
}

// NewFeaturesManager creates the alpha features manager from the user configuration
func NewFeaturesManager(configManager config.UserConfigManager) *FeatureManager {
	return &FeatureManager{
		configManager: configManager,
		withSync:      &sync.Once{},
	}
}

func NewFeaturesManagerWithConfig(config config.Config) *FeatureManager {
	return &FeatureManager{
		userConfigCache: config,
		withSync:        &sync.Once{},
	}
}

// ListFeatures pulls the list of features in alpha mode available within the application and displays its current state
// which is `on` or `off`.
func (m *FeatureManager) ListFeatures() (map[string]Feature, error) {
	result := make(map[string]Feature)

	alphaFeatures := allFeatures
	if m.alphaFeaturesResolver != nil {
		alphaFeatures = m.alphaFeaturesResolver()
	}

	for _, aFeature := range alphaFeatures {
		// cast is safe here from string to AlphaFeatureId
		status := disabledText
		if m.IsEnabled(FeatureId(aFeature.Id)) {
			status = enabledText
		}

		result[aFeature.Id] = Feature{
			Id:          aFeature.Id,
			Description: aFeature.Description,
			Status:      status,
		}
	}

	return result, nil
}

func (m *FeatureManager) initConfigCache() {
	if m.userConfigCache == nil {
		config, err := m.configManager.Load()
		if err != nil {
			log.Panic("Can't load user config!! %w", err)
		}
		m.userConfigCache = config
	}
}

// IsEnabled search and find out if the AlphaFeatureId is currently enabled
func (m *FeatureManager) IsEnabled(featureId FeatureId) bool {
	// guard from using the alphaFeatureManager from multiple routines. Only the first one will create the cache.
	m.withSync.Do(m.initConfigCache)

	// For testing, and in CI, allow enabling alpha features via the environment.
	envName := fmt.Sprintf("AZD_ALPHA_ENABLE_%s", strings.ToUpper(string(featureId)))
	if v, has := os.LookupEnv(envName); has {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		} else {
			log.Printf("could not parse %s as a bool when considering %s", v, envName)
		}
	}

	//check if all features is ON
	if allOn := isEnabled(m.userConfigCache, AllId); allOn {
		return true
	}

	// check if the feature is ON
	if featureOn := isEnabled(m.userConfigCache, featureId); featureOn {
		return true
	}

	// check if the feature has been set with a default value internally
	if val, ok := defaultEnablement[strings.ToLower(string(featureId))]; ok {
		return val
	}

	return false
}

// defaultEnablement is a map of lower-cased feature ids to their default enablement values.
//
// This is used to determine if a feature is enabled by default, when no user configuration is specified.
var defaultEnablement = map[string]bool{}

// SetDefaultEnablement sets the default enablement value for the given feature id.
func SetDefaultEnablement(id string, val bool) {
	defaultEnablement[strings.ToLower(id)] = val
}

func isEnabled(config config.Config, id FeatureId) bool {
	longKey := fmt.Sprintf("%s.%s", parentKey, string(id))
	value, exists := config.Get(longKey)
	if !exists {
		return exists
	}

	// need to check the cast here in case the config is manually updated
	stringValue, castResult := value.(string)
	if !castResult {
		log.Panicf("Invalid configuration value for '%s': %s", longKey, value)
	}
	stringValue = strings.ToLower(stringValue)

	if stringValue != disabledValue && stringValue != enabledValue {
		log.Panicf(
			"invalid configuration value for '%s': %s. Valid options are '%s' or '%s'.",
			longKey,
			stringValue,
			enabledValue,
			disabledValue,
		)
	}

	// previous condition ensured that stringValue is either `enabledValue` or `disabledValue`
	return stringValue == enabledValue
}
