package alpha

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/resources"
	"gopkg.in/yaml.v3"
)

// Feature defines the structure for a feature in alpha mode.
type Feature struct {
	Id          string `yaml:"id"`
	Description string `yaml:"description"`
	Status      string
}

// constant keys are used within source code to pull the AlphaFeature
type FeatureId string

const (
	// the key for overriding all alpha features value.
	AllId FeatureId = "all"

	disabledText  string = "Off"
	disabledValue string = "off"
	enabledText   string = "On"
	enabledValue  string = "on"
	parentKey     string = "alpha"
)

var allFeatures []Feature

func init() {
	err := yaml.Unmarshal(resources.AlphaFeatures, &allFeatures)
	if err != nil {
		log.Panic("Can't marshall alpha features!! %w", err)
	}
}

// MustFeatureKey converts the given key to a FeatureId as [IsFeatureKey] would and panics if the conversion fails.
func MustFeatureKey(key string) FeatureId {
	id, valid := IsFeatureKey(key)
	if !valid {
		panic(fmt.Sprintf("MustFeatureKey: unknown key %s", key))
	}

	return id
}

// IsFeatureKey inspect if `key` is an alpha feature. Returns the AlphaFeatureId and true in case it is.
// otherwise returns empty AlphaFeatureId and false.
func IsFeatureKey(key string) (featureId FeatureId, isAlpha bool) {
	for _, alphaF := range allFeatures {
		if key == alphaF.Id {
			featureId, isAlpha = FeatureId(alphaF.Id), true
			break
		}
	}
	return featureId, isAlpha
}

// GetEnableCommand provides a message for how to enable the alpha feature.
func GetEnableCommand(key FeatureId) string {
	return fmt.Sprintf("azd config set %s on", strings.Join([]string{parentKey, string(key)}, "."))
}

// ShouldWarn returns true if a warning should be emitted when using a given alpha feature.
func ShouldWarn(key FeatureId) bool {
	noAlphaWarnings, err := strconv.ParseBool(os.Getenv("AZD_DEBUG_NO_ALPHA_WARNINGS"))

	return err != nil || !noAlphaWarnings
}

// WarningMessage generates the output message when customer is using alpha features.
func WarningMessage(key FeatureId) ux.UxItem {
	return &ux.MultilineMessage{
		Lines: []string{
			output.WithWarningFormat("WARNING: Feature '%s' is in alpha stage.", string(key)),
			fmt.Sprintf("To learn more about alpha features and their support, visit %s.",
				output.WithLinkFormat("https://aka.ms/azd-feature-stages")),
			"",
		},
	}
}
