// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
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

	va := &versionAction{
		flags:     &versionFlags{},
		formatter: &output.NoneFormatter{},
		writer:    &bytes.Buffer{},
	}

	suffix := va.channelSuffix()
	// In test builds, internal.Version is "0.0.0-dev.0" (not daily format)
	// so it will return " (stable)"
	require.Equal(t, " (stable)", suffix)
}
