package alphafeatures

import (
	"fmt"
	"log"
	"strings"

	"github.com/azure/azure-dev/cli/azd/resources"
	"gopkg.in/yaml.v3"
)

// AlphaFeature defines the structure for a feature in alpha mode.
type AlphaFeature struct {
	Id          string `yaml:"id"`
	Description string `yaml:"description"`
	Status      string
}

// constant keys are used within source code to pull the AlphaFeature
type AlphaFeatureId string

const (
	// the key for overriding all alpha features value.
	AllId AlphaFeatureId = "all"

	disabledText  string = "Off"
	disabledValue string = "off"
	enabledText   string = "On"
	enabledValue  string = "on"
	parentKey     string = "alpha"
)

// mustUnmarshalAlphaFeatures parsed the alpha features from resources into a list of AlphaFeature
func mustUnmarshalAlphaFeatures() []AlphaFeature {
	var alphaFeatures []AlphaFeature
	err := yaml.Unmarshal(resources.AlphaFeatures, &alphaFeatures)
	if err != nil {
		log.Panic("Can't marshall alpha features!! %w", err)
	}
	return alphaFeatures
}

// IsAlphaKey inspect if `key` is an alpha feature. Returns the AlphaFeatureId and true in case it is.
// otherwise returns empty AlphaFeatureId and false.
func IsAlphaKey(key string) (featureId AlphaFeatureId, isAlpha bool) {
	alphaFeatures := mustUnmarshalAlphaFeatures()

	for _, alphaF := range alphaFeatures {
		if key == alphaF.Id {
			featureId, isAlpha = AlphaFeatureId(alphaF.Id), true
			break
		}
	}
	return featureId, isAlpha
}

// GetEnableCommand provides a message for how to enable the alpha feature.
func GetEnableCommand(key AlphaFeatureId) string {
	return fmt.Sprintf("azd config set %s on", strings.Join([]string{parentKey, string(key)}, "."))
}
