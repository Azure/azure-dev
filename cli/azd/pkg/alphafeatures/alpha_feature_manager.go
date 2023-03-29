package alphafeatures

import (
	"log"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
)

type AlphaFeatureManager struct {
	configManager   config.UserConfigManager
	userConfigCache config.Config
	// used for mocking alpha features on testing
	alphaFeaturesResolver func() []AlphaFeature
}

func NewAlphaFeaturesManager(configManager config.UserConfigManager) *AlphaFeatureManager {
	return &AlphaFeatureManager{
		configManager: configManager,
	}
}

func (m *AlphaFeatureManager) ListFeatures() (map[string]AlphaFeature, error) {
	result := make(map[string]AlphaFeature)

	var alphaFeatures []AlphaFeature
	if m.alphaFeaturesResolver != nil {
		alphaFeatures = m.alphaFeaturesResolver()
	} else {
		alphaFeatures = mustUnmarshalAlphaFeatures()
	}

	for _, aFeature := range alphaFeatures {
		// cast is safe here from string to AlphaFeatureId
		status := disabledText
		if m.IsEnabled(AlphaFeatureId(aFeature.Id)) {
			status = enabledText
		}

		result[aFeature.Id] = AlphaFeature{
			Id:          aFeature.Id,
			Description: aFeature.Description,
			Status:      status,
		}
	}

	return result, nil
}

func (m *AlphaFeatureManager) IsEnabled(featureId AlphaFeatureId) bool {
	if m.userConfigCache == nil {
		config, err := m.configManager.Load()
		if err != nil {
			log.Panic("Can't load user config!! %w", err)
		}
		m.userConfigCache = config
	}

	//check if all features is ON
	if allOn := isEnabled(m.userConfigCache, string(AllId)); allOn {
		return true
	}

	// check if the feature is ON
	if featureOn := isEnabled(m.userConfigCache, string(featureId)); featureOn {
		return true
	}

	return false
}

func isEnabled(config config.Config, id string) bool {
	longKey := strings.Join([]string{
		string(parentKey),
		id,
	}, ".")
	value, exists := config.Get(longKey)
	if !exists {
		return exists
	}
	// safe cast -> reading from config text file
	stringValue, _ := value.(string)
	stringValue = strings.ToLower(stringValue)

	if stringValue != disabledValue && stringValue != enabledValue {
		log.Panicf("invalid value: %s for alpha-feature config key: %s", longKey, stringValue)
	}

	// previous condition ensured that stringValue is either `enabledValue` or `disabledValue`
	return stringValue == enabledValue
}
