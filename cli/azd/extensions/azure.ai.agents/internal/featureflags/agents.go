// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package featureflags

import (
	"context"
	"fmt"
	"os"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

var (
	// ManagedHarness controls prompt agents using a Copilot harness.
	ManagedHarness = Feature{
		ID:     "agents.managed_harness",
		EnvVar: "AZD_FOUNDRY_EXT_ENABLE_AGENTS_MANAGED_HARNESS",
	}
	// Voice controls managed voice and hosted voice agents.
	Voice = Feature{
		ID:     "agents.voice",
		EnvVar: "AZD_FOUNDRY_EXT_ENABLE_AGENTS_VOICE",
	}
	// LongRunning controls starting background Responses invocations.
	LongRunning = Feature{
		ID:     "agents.long_running",
		EnvVar: "AZD_FOUNDRY_EXT_ENABLE_AGENTS_LONG_RUNNING",
	}
)

type environmentKey struct{}

// WithEnvironment preserves the SDK environment for nested calls.
func WithEnvironment(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, environmentKey{}, name)
}

// Environment returns the SDK or host lifecycle selection.
func Environment(ctx context.Context) string {
	if name, ok := ctx.Value(environmentKey{}).(string); ok {
		return name
	}
	return os.Getenv("AZD_ENVIRONMENT")
}

// Require rejects a disabled preview before its operation starts.
// Service values include opt-ins from definitions not yet persisted.
func Require(ctx context.Context, client *azdext.AzdClient, feature Feature, serviceValues ...string) error {
	enabled, err := IsEnabled(ctx, client, feature, Environment(ctx))
	if err != nil {
		return err
	}
	for _, value := range serviceValues {
		enabled = envEnabled(value) || enabled
	}
	if enabled {
		return nil
	}
	return exterrors.Compatibility(
		exterrors.CodeFeatureNotEnabled,
		fmt.Sprintf("preview feature %q is disabled", feature.ID),
		fmt.Sprintf(
			"enable with 'azd config set alpha.%s on' or 'azd env set %s true' "+
				"in the selected environment; alternatively set %s=true in the process environment",
			feature.ID, feature.EnvVar, feature.EnvVar,
		),
	)
}
