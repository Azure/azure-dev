// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package copilot

import (
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
)

// FeatureCopilot is the feature key for the Copilot agent feature.
// The underlying alpha key remains "llm" for backward compatibility with
// existing user configurations (alpha.llm = on).
var FeatureCopilot = alpha.MustFeatureKey("llm")

// IsFeatureEnabled checks if the Copilot agent feature is enabled.
func IsFeatureEnabled(alphaManager *alpha.FeatureManager) error {
	if alphaManager == nil {
		panic("alphaManager cannot be nil")
	}
	if !alphaManager.IsEnabled(FeatureCopilot) {
		return fmt.Errorf(
			"the %s feature is not enabled. Please enable it using the command: \"%s\"",
			DisplayTitle, alpha.GetEnableCommand(FeatureCopilot))
	}
	return nil
}
