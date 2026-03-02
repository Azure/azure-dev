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
	"github.com/stretchr/testify/require"
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

		ran := false
		nextFn := func(ctx context.Context) (*actions.ActionResult, error) {
			ran = true
			return nil, nil
		}

		_, _ = middleware.Run(*mockContext.Context, nextFn)
		require.True(t, ran)

		// Verify that installed extensions were listed without error
		installed, err := manager.ListInstalled()
		require.NoError(t, err)
		require.Equal(t, 2, len(installed))
	})

	t.Run("WithNilExtensionManager", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())

		options := &Options{
			CommandPath: "azd provision",
			Name:        "provision",
		}
		middleware := NewTelemetryMiddleware(options, lazyPlatformConfig, nil)

		ran := false
		nextFn := func(ctx context.Context) (*actions.ActionResult, error) {
			ran = true
			return nil, nil
		}

		// Should not panic when extensionManager is nil
		_, _ = middleware.Run(*mockContext.Context, nextFn)
		require.True(t, ran)
	})
}
