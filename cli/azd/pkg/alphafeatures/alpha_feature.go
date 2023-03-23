package alphafeatures

import (
	"fmt"
	"log"
	"strings"

	"github.com/azure/azure-dev/cli/azd/resources"
	"gopkg.in/yaml.v3"
)

type AlphaFeature struct {
	Id          string `yaml:"id"`
	Description string `yaml:"description"`
	Status      string
}

// constant keys are used within source code to pull the AlphaFeature
type AlphaFeatureId string

const (
	parentKey   AlphaFeatureId = "experimental"
	AllId       AlphaFeatureId = "all"
	TerraformId AlphaFeatureId = "terraform"
)

func mustUnmarshalAlphaFeatures() []AlphaFeature {
	var alphaFeatures []AlphaFeature
	err := yaml.Unmarshal(resources.AlphaFeatures, &alphaFeatures)
	if err != nil {
		log.Panic("Can't marshall alpha features!! %w", err)
	}
	return alphaFeatures
}

func IsAlphaKey(key string) bool {
	alphaFeatures := mustUnmarshalAlphaFeatures()

	for _, alphaF := range alphaFeatures {
		if key == alphaF.Id {
			return true
		}
	}
	return false
}

func GetEnableCommand(key AlphaFeatureId) string {
	return fmt.Sprintf("azd config set %s on", strings.Join([]string{string(parentKey), string(key)}, "."))
}
