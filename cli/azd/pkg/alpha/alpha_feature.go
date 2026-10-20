// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package alpha

import (
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/resources"
	"github.com/braydonk/yaml"
)

// Feature defines the structure for a feature in alpha mode.
type Feature struct {
	Id          string `yaml:"id"`
	Description string `yaml:"description"`
	Status      string
	// EnvVar names the sole env flag for additive evaluation, if set.
	EnvVar string `yaml:"envVar,omitempty" json:",omitempty"`
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
	feature, found := GetFeature(FeatureId(key))
	return FeatureId(feature.Id), found
}

// GetFeature returns a declaration from the shared alpha registry.
func GetFeature(id FeatureId) (Feature, bool) {
	for _, feature := range allFeatures {
		if feature.Id == string(id) {
			return feature, true
		}
	}

	return Feature{}, false
}

// IsEnabledWithSources evaluates additive extension opt-ins.
// Sources are caller-provided snapshots.
// Only "on" in alpha.<id> or alpha.all, or a true strconv.ParseBool
// env value, enables the feature. Other values contribute no signal.
// EnvVar, when set, replaces the default AZD_ALPHA_ENABLE_<ID> names.
// Pass each service's env map separately, without merging sources.
// This method does no I/O and ignores process env and experimentation
// defaults. Callers must propagate snapshot-loading errors.
func (f Feature) IsEnabledWithSources(userConfig config.Config, envSources ...map[string]string) bool {
	if userConfig != nil {
		for _, id := range []string{string(AllId), f.Id} {
			if value, ok := userConfig.GetString(parentKey + "." + id); ok && strings.EqualFold(value, enabledValue) {
				return true
			}
		}
	}

	envNames := []string{f.EnvVar}
	if f.EnvVar == "" {
		id := strings.ToUpper(f.Id)
		envNames = []string{
			"AZD_ALPHA_ENABLE_" + id,
			"AZD_ALPHA_ENABLE_" + strings.ReplaceAll(id, ".", "_"),
		}
	}

	for _, source := range envSources {
		for _, name := range envNames {
			if enabled, err := strconv.ParseBool(source[name]); err == nil && enabled {
				return true
			}
		}
	}

	return false
}

// GetEnableCommand provides a message for how to enable the alpha feature.
func GetEnableCommand(key FeatureId) string {
	return fmt.Sprintf("azd config set %s on", strings.Join([]string{parentKey, string(key)}, "."))
}
