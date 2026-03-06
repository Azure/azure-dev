// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/platform"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mocktracing"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
)

func Test_Telemetry_Run(t *testing.T) {
	lazyPlatformConfig := lazy.NewLazy(func() (*platform.Config, error) {
		return &platform.Config{
			Type: "devcenter",
		}, nil
	})

	t.Run("WithRootAction", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())

		options := &Options{
			CommandPath: "azd provision",
			Name:        "provision",
		}
		middleware := NewTelemetryMiddleware(options, lazyPlatformConfig, nil)

		ran := false
		var actualContext context.Context

		nextFn := func(ctx context.Context) (*actions.ActionResult, error) {
			ran = true
			actualContext = ctx
			return nil, nil
		}

		_, _ = middleware.Run(*mockContext.Context, nextFn)

		require.True(t, ran)
		require.NotEqual(
			t,
			*mockContext.Context,
			actualContext,
			"Context should be a different instance since telemetry creates a new context",
		)
	})

	t.Run("WithChildAction", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())

		options := &Options{
			CommandPath: "azd provision",
			Name:        "provision",
		}
		middleware := NewTelemetryMiddleware(options, lazyPlatformConfig, nil)

		ran := false
		var actualContext context.Context

		nextFn := func(ctx context.Context) (*actions.ActionResult, error) {
			ran = true
			actualContext = ctx
			return nil, nil
		}

		ctx := WithChildAction(*mockContext.Context)
		_, _ = middleware.Run(ctx, nextFn)

		require.True(t, ran)
		require.NotEqual(
			t,
			*mockContext.Context,
			actualContext,
			"Context should be a different instance since telemetry creates a new context",
		)
	})

	t.Run("WithInstalledExtensions", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())

		// Set up installed extensions in config
		userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
		userConfig, err := userConfigManager.Load()
		require.NoError(t, err)

		// Use extensions whose alphabetical order differs from insertion order
		// to verify sorting behavior
		installedExtensions := map[string]*extensions.Extension{
			"microsoft.azd.demo": {
				Id:      "microsoft.azd.demo",
				Version: "0.5.0",
			},
			"microsoft.azd.ai": {
				Id:      "microsoft.azd.ai",
				Version: "1.2.0",
			},
		}
		err = userConfig.Set("extension.installed", installedExtensions)
		require.NoError(t, err)

		lazyRunner := lazy.NewLazy(func() (*extensions.Runner, error) {
			return nil, nil
		})
		manager, err := extensions.NewManager(userConfigManager, nil, lazyRunner, mockContext.HttpClient)
		require.NoError(t, err)

		options := &Options{
			CommandPath: "azd provision",
			Name:        "provision",
		}
		middleware := NewTelemetryMiddleware(options, lazyPlatformConfig, manager)

		// Call the method directly with a mock span to verify attributes
		span := &mocktracing.Span{}
		middleware.(*TelemetryMiddleware).setInstalledExtensionsAttributes(span)
		var installedAttr *attribute.KeyValue
		for i := range span.Attributes {
			if span.Attributes[i].Key == "extension.installed" {
				installedAttr = &span.Attributes[i]
				break
			}
		}
		require.NotNil(t, installedAttr, "extension.installed attribute should be set")
		require.Equal(t,
			[]string{"microsoft.azd.ai@1.2.0", "microsoft.azd.demo@0.5.0"},
			installedAttr.Value.AsStringSlice(),
		)
	})

	t.Run("WithNoInstalledExtensions", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())

		userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)

		lazyRunner := lazy.NewLazy(func() (*extensions.Runner, error) {
			return nil, nil
		})
		manager, err := extensions.NewManager(userConfigManager, nil, lazyRunner, mockContext.HttpClient)
		require.NoError(t, err)

		options := &Options{
			CommandPath: "azd provision",
			Name:        "provision",
		}
		middleware := NewTelemetryMiddleware(options, lazyPlatformConfig, manager)

		span := &mocktracing.Span{}
		middleware.(*TelemetryMiddleware).setInstalledExtensionsAttributes(span)

		require.Len(t, span.Attributes, 1, "extension.installed attribute should be set")
		require.Equal(t, "extension.installed", string(span.Attributes[0].Key))
		require.Empty(t, span.Attributes[0].Value.AsStringSlice(), "should be an empty slice when no extensions are installed")
	})

	t.Run("WithAllNilExtensionEntries", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())

		userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
		userConfig, err := userConfigManager.Load()
		require.NoError(t, err)

		// Simulate corrupted config where all extension values are nil
		installedExtensions := map[string]*extensions.Extension{
			"microsoft.azd.demo": nil,
			"microsoft.azd.ai":   nil,
		}
		err = userConfig.Set("extension.installed", installedExtensions)
		require.NoError(t, err)

		lazyRunner := lazy.NewLazy(func() (*extensions.Runner, error) {
			return nil, nil
		})
		manager, err := extensions.NewManager(userConfigManager, nil, lazyRunner, mockContext.HttpClient)
		require.NoError(t, err)

		options := &Options{
			CommandPath: "azd provision",
			Name:        "provision",
		}
		middleware := NewTelemetryMiddleware(options, lazyPlatformConfig, manager)

		span := &mocktracing.Span{}
		middleware.(*TelemetryMiddleware).setInstalledExtensionsAttributes(span)

		require.Len(t, span.Attributes, 1, "extension.installed attribute should be set")
		require.Equal(t, "extension.installed", string(span.Attributes[0].Key))
		require.Empty(t, span.Attributes[0].Value.AsStringSlice(), "should be an empty slice when all entries are nil")
	})

	t.Run("WithNilExtensionManager", func(t *testing.T) {
		options := &Options{
			CommandPath: "azd provision",
			Name:        "provision",
		}
		middleware := NewTelemetryMiddleware(options, lazyPlatformConfig, nil)

		// Should not panic when extensionManager is nil
		span := &mocktracing.Span{}
		middleware.(*TelemetryMiddleware).setInstalledExtensionsAttributes(span)

		require.Empty(t, span.Attributes, "no attributes should be set when manager is nil")
	})
}
