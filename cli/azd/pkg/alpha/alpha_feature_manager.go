package alpha

import (
	"fmt"
	"log"
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
}

// NewFeaturesManager creates the alpha features manager from the user configuration
func NewFeaturesManager(configManager config.UserConfigManager) *FeatureManager {
	return &FeatureManager{
		configManager: configManager,
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

var withSync *sync.Once = &sync.Once{}

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
	withSync.Do(m.initConfigCache)

	//check if all features is ON
	if allOn := isEnabled(m.userConfigCache, AllId); allOn {
		return true
	}

	// check if the feature is ON
	if featureOn := isEnabled(m.userConfigCache, featureId); featureOn {
		return true
	}

	return false
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
