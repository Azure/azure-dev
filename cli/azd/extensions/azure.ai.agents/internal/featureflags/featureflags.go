// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore featureflags

// Package featureflags resolves agents extension preview opt-ins.
package featureflags

import (
	"errors"
	"os"
	"strconv"
)

// Feature declares one extension opt-in.
type Feature struct {
	// ID identifies the capability, not a user config key.
	ID string
	// EnvVar is the process environment key for this feature.
	// A value already in the process counts, including one the azd host injected.
	EnvVar string
	// Description is an optional short summary for help.
	Description string
}

// IsEnabled reports whether the feature is enabled in the current process environment.
// It reads os.Getenv, so values the azd host already injected count.
// Only true values accepted by strconv.ParseBool enable a feature.
// Invalid declarations return an error; invalid values leave the feature off.
// IsEnabled does not load azd config or dotenv files from disk.
func IsEnabled(feature Feature) (bool, error) {
	if feature.ID == "" || feature.EnvVar == "" {
		return false, errors.New("featureflags.IsEnabled: feature ID and EnvVar must not be empty")
	}

	enabled, err := strconv.ParseBool(os.Getenv(feature.EnvVar))
	return err == nil && enabled, nil
}
