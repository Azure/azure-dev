// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func TestVersionAction_NoneFormat(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(context.Background())

	action := newVersionAction(
		&versionFlags{},
		&output.NoneFormatter{},
		&bytes.Buffer{},
		mockContext.Console,
		mockContext.AlphaFeaturesManager,
	)

	result, err := action.Run(t.Context())
	require.NoError(t, err)
	require.Nil(t, result)
}

func TestVersionAction_JsonFormat(t *testing.T) {
	t.Parallel()
	buf := &bytes.Buffer{}
	mockContext := mocks.NewMockContext(context.Background())

	action := newVersionAction(
		&versionFlags{},
		&output.JsonFormatter{},
		buf,
		mockContext.Console,
		mockContext.AlphaFeaturesManager,
	)

	result, err := action.Run(t.Context())
	require.NoError(t, err)
	require.Nil(t, result)

	var versionResult contracts.VersionResult
	err = json.Unmarshal(buf.Bytes(), &versionResult)
	require.NoError(t, err)

	versionSpec := internal.VersionInfo()
	require.Equal(t, versionSpec.Version.String(), versionResult.Azd.Version)
	require.Equal(t, versionSpec.Commit, versionResult.Azd.Commit)
}

func TestVersionAction_ChannelSuffix(t *testing.T) {
	t.Parallel()

	t.Run("update_feature_disabled", func(t *testing.T) {
		t.Parallel()
		mockContext := mocks.NewMockContext(context.Background())

		va := &versionAction{
			flags:               &versionFlags{},
			formatter:           &output.NoneFormatter{},
			writer:              &bytes.Buffer{},
			console:             mockContext.Console,
			alphaFeatureManager: mockContext.AlphaFeaturesManager,
		}

		suffix := va.channelSuffix()
		require.Equal(t, "", suffix)
	})

	t.Run("update_feature_enabled_stable", func(t *testing.T) {
		t.Parallel()

		cfg := config.NewEmptyConfig()
		_ = cfg.Set("alpha.update", "on")
		fm := alpha.NewFeaturesManagerWithConfig(cfg)

		va := &versionAction{
			flags:               &versionFlags{},
			formatter:           &output.NoneFormatter{},
			writer:              &bytes.Buffer{},
			console:             nil, // not needed for channelSuffix
			alphaFeatureManager: fm,
		}

		suffix := va.channelSuffix()
		// In test builds, internal.Version is "0.0.0-dev.0" (not daily format)
		// so it will either return " (stable)" or " (daily)" depending on version
		require.NotEqual(t, "", suffix)
	})
}
