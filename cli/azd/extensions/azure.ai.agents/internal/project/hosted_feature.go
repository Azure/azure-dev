// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore featureflags

package project

import (
	"context"
	"fmt"
	"os"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/extensions/featureflags"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// HostedAgentFeature is the Hosted Agent opt-in.
//
// Enable with any of:
//   - azd config set alpha.agents.hosted on
//   - process or azd environment AZD_FOUNDRY_EXT_ENABLE_AGENTS_HOSTED=true
//   - AZD_FOUNDRY_EXT_ENABLE_AGENTS_HOSTED=true in any azure.yaml service env map
var HostedAgentFeature = featureflags.Feature{
	ID:     "agents.hosted",
	EnvVar: "AZD_FOUNDRY_EXT_ENABLE_AGENTS_HOSTED",
}

// IsHostedAgentFeatureEnabled reports whether the Hosted Agent opt-in is on.
// The host passes -e/--environment to extensions as AZD_ENVIRONMENT.
func IsHostedAgentFeatureEnabled(ctx context.Context, client *azdext.AzdClient) (bool, error) {
	if client == nil {
		return false, nil
	}

	enabled, err := featureflags.IsEnabled(ctx, client, HostedAgentFeature, os.Getenv("AZD_ENVIRONMENT"))
	if err != nil {
		return false, fmt.Errorf("checking hosted agent feature: %w", err)
	}
	return enabled, nil
}

// RequireHostedAgentFeature returns a compatibility error when the Hosted Agent
// opt-in is off. Call this before Package or Deploy work for kind: hosted.
func RequireHostedAgentFeature(ctx context.Context, client *azdext.AzdClient) error {
	enabled, err := IsHostedAgentFeatureEnabled(ctx, client)
	if err != nil {
		return err
	}
	if enabled {
		return nil
	}

	return exterrors.Compatibility(
		exterrors.CodeFeatureNotEnabled,
		"hosted agent support is disabled",
		"enable with 'azd config set alpha.agents.hosted on' or set "+
			HostedAgentFeature.EnvVar+"=true (process, .env, or azure.yaml services[].env)",
	)
}
