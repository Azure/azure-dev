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
	// EnvVar is the exact process environment key for this feature.
	EnvVar string
	// Description is an optional short summary for help.
	Description string
}

// IsEnabled reads only the feature's current process environment.
// Only true values accepted by strconv.ParseBool enable a feature.
// Invalid declarations return an error; invalid values leave it off.
func IsEnabled(feature Feature) (bool, error) {
	if feature.ID == "" || feature.EnvVar == "" {
		return false, errors.New("featureflags.IsEnabled: feature ID and EnvVar must not be empty")
	}

	enabled, err := strconv.ParseBool(os.Getenv(feature.EnvVar))
	return err == nil && enabled, nil
}
