// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package optionout

import (
	"fmt"
	"log"
	"strings"

	"github.com/azure/azure-dev/cli/azd/resources"
	"github.com/braydonk/yaml"
)

// Feature defines the structure for a feature in option-out mode.
type Feature struct {
	Id          string `yaml:"id"`
	Description string `yaml:"description"`
	Status      string
}

// constant keys are used within source code to pull the OptionOutFeature
type FeatureId string

const (
	// the key for overriding all option-out features value.
	AllId FeatureId = "all"

	disabledText  string = "Off"
	disabledValue string = "off"
	enabledText   string = "On"
	enabledValue  string = "on"
	parentKey     string = "optionout"
)

var allFeatures []Feature

func init() {
	err := yaml.Unmarshal(resources.OptionOutFeatures, &allFeatures)
	if err != nil {
		log.Panic("Can't marshall option out features!! %w", err)
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

// IsFeatureKey inspect if `key` is an option-out feature. Returns the OptionOutFeatureId and true in case it is.
// otherwise returns empty OptionOutFeatureId and false.
func IsFeatureKey(key string) (featureId FeatureId, isOptionOut bool) {
	for _, optionOutF := range allFeatures {
		if key == optionOutF.Id {
			featureId, isOptionOut = FeatureId(optionOutF.Id), true
			break
		}
	}
	return featureId, isOptionOut
}

// GetEnableCommand provides a message for how to enable the option-out feature.
func GetEnableCommand(key FeatureId) string {
	return fmt.Sprintf("azd config set %s on", strings.Join([]string{parentKey, string(key)}, "."))
}
