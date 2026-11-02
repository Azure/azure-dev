// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore featureflags

// Package featureflags resolves agents extension preview switches.
package featureflags

import (
	"errors"
	"os"
	"strconv"
)

// Feature declares one extension feature switch.
type Feature struct {
	// ID identifies the capability, not a user config key.
	ID string
	// EnvVar is the process environment key for this feature.
	// A value already in the process counts, including one the azd host injected.
	EnvVar string
	// Description is an optional short summary for help.
	Description string
	// DefaultEnabled applies only when EnvVar is absent.
	DefaultEnabled bool
}

// IsEnabled reports whether the feature is enabled in the current process environment.
// It uses the declared default only when EnvVar is absent.
// Explicit values, including those injected by the azd host, use strconv.ParseBool.
// Invalid declarations return an error; invalid values leave the feature off.
// IsEnabled does not load azd config or dotenv files from disk.
func IsEnabled(feature Feature) (bool, error) {
	if feature.ID == "" || feature.EnvVar == "" {
		return false, errors.New("featureflags.IsEnabled: feature ID and EnvVar must not be empty")
	}

	value, present := os.LookupEnv(feature.EnvVar)
	if !present {
		return feature.DefaultEnabled, nil
	}

	enabled, err := strconv.ParseBool(value)
	return err == nil && enabled, nil
}
