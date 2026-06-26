// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
)

func TestVersionAction_NoneFormat(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(t.Context())

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
	mockContext := mocks.NewMockContext(t.Context())

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

func Test_ChannelSuffix_FeatureDisabled(t *testing.T) {
	t.Parallel()
	v := &versionAction{}
	require.Equal(t, " (stable)", v.channelSuffix())
}

func Test_ChannelSuffix_FeatureEnabled_StableBuild(t *testing.T) {
	// Enable alpha feature and set Version to stable
	setProdVersion(t)
	v := &versionAction{}
	require.Equal(t, " (stable)", v.channelSuffix())
}

func Test_ChannelSuffix_FeatureEnabled_DailyBuild(t *testing.T) {
	// Set Version to daily-like
	orig := internal.Version
	internal.Version = "1.0.0-daily.12345 (commit aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa)"
	t.Cleanup(func() { internal.Version = orig })

	v := &versionAction{}
	require.Equal(t, " (daily)", v.channelSuffix())
}

func Test_VersionAction_Run_FormatPath(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	v := &versionAction{
		formatter: &output.JsonFormatter{},
		writer:    &buf,
	}
	_, err := v.Run(t.Context())
	require.NoError(t, err)
	require.NotEmpty(t, buf.String())
}

func Test_NewVersionFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newVersionFlags(cmd, global)
	require.NotNil(t, flags)
}

func Test_NewVersionAction(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	a := newVersionAction(&versionFlags{}, &output.JsonFormatter{}, &bytes.Buffer{}, console)
	require.NotNil(t, a)
}

func Test_VersionAction_Run(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	console := mockinput.NewMockConsole()
	a := newVersionAction(&versionFlags{}, &output.JsonFormatter{}, &buf, console)
	_, err := a.(*versionAction).Run(t.Context())
	require.NoError(t, err)
}
