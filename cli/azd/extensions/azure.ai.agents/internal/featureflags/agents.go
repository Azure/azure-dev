// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package featureflags

import (
	"fmt"

	"azureaiagent/internal/exterrors"
)

// Require rejects a disabled preview before its operation starts.
func Require(feature Feature) error {
	enabled, err := IsEnabled(feature)
	if err != nil {
		return err
	}
	if enabled {
		return nil
	}
	return exterrors.Compatibility(
		exterrors.CodeFeatureNotEnabled,
		fmt.Sprintf("preview feature %q is disabled", feature.ID),
		fmt.Sprintf(
			"enable by setting %s=true in your shell or CI before running azd",
			feature.EnvVar,
		),
	)
}
