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

type environmentKey struct{}
type environmentErrorKey struct{}
type initScopeKey struct{}

// WithInitScope permits reads before init creates its local scopes.
// It preserves the selected name and genuine source errors.
func WithInitScope(ctx context.Context) context.Context {
	return context.WithValue(ctx, initScopeKey{}, true)
}

// WithEnvironment preserves the SDK environment for nested calls.
func WithEnvironment(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, environmentKey{}, name)
}

// WithEnvironmentError binds a flag error for nested preview checks.
func WithEnvironmentError(ctx context.Context, err error) context.Context {
	return context.WithValue(ctx, environmentErrorKey{}, struct{ err error }{err})
}

// EnvironmentError returns the bound environment flag error.
func EnvironmentError(ctx context.Context) error {
	value, _ := ctx.Value(environmentErrorKey{}).(struct{ err error })
	return value.err
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
	if err := EnvironmentError(ctx); err != nil {
		return err
	}
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

// ProcessEnabled reports whether the process environment enables feature.
// Help rendering uses it when the azd host cannot be read.
func ProcessEnabled(feature Feature) bool {
	return envEnabled(os.Getenv(feature.EnvVar))
}
