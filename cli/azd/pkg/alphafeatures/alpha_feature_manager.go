package alphafeatures

import (
	"log"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
)

type AlphaFeatureManager struct {
	configManager   config.UserConfigManager
	userConfigCache config.Config
}

func NewAlphaFeaturesManager(configManager config.UserConfigManager) *AlphaFeatureManager {
	return &AlphaFeatureManager{
		configManager: configManager,
	}
}

func (m *AlphaFeatureManager) ListFeatures() (map[string]AlphaFeature, error) {
	result := make(map[string]AlphaFeature)
	alphaFeatures := mustUnmarshalAlphaFeatures()

	for _, aFeature := range alphaFeatures {
		// cast is safe here from string to AlphaFeatureId
		status := "disabled"
		if m.IsEnabled(AlphaFeatureId(aFeature.Id)) {
			status = "enabled"
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
	_, exists := config.Get(longKey)
	return exists
}
