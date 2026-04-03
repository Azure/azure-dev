// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
package cmd

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =======================================================
// extensionShowAction.Run arg validation tests
// =======================================================

func Test_ExtensionShowAction_Run_NoArgs(t *testing.T) {
	t.Parallel()
	action := &extensionShowAction{
		args:    []string{},
		flags:   &extensionShowFlags{global: &internal.GlobalCommandOptions{}},
		console: mockinput.NewMockConsole(),
	}
	_, err := action.Run(context.Background())
	require.Error(t, err)
	var suggestion *internal.ErrorWithSuggestion
	require.ErrorAs(t, err, &suggestion)
	assert.ErrorIs(t, suggestion.Err, internal.ErrNoArgsProvided)
}

func Test_ExtensionShowAction_Run_TooManyArgs(t *testing.T) {
	t.Parallel()
	action := &extensionShowAction{
		args:    []string{"ext1", "ext2"},
		flags:   &extensionShowFlags{global: &internal.GlobalCommandOptions{}},
		console: mockinput.NewMockConsole(),
	}
	_, err := action.Run(context.Background())
	require.Error(t, err)
	var suggestion *internal.ErrorWithSuggestion
	require.ErrorAs(t, err, &suggestion)
	assert.ErrorIs(t, suggestion.Err, internal.ErrInvalidFlagCombination)
}

// =======================================================
// extensionInstallAction.Run arg validation tests
// =======================================================

func Test_ExtensionInstallAction_Run_NoArgs(t *testing.T) {
	t.Parallel()
	action := &extensionInstallAction{
		args:    []string{},
		flags:   &extensionInstallFlags{global: &internal.GlobalCommandOptions{}},
		console: mockinput.NewMockConsole(),
	}
	_, err := action.Run(context.Background())
	require.Error(t, err)
	var suggestion *internal.ErrorWithSuggestion
	require.ErrorAs(t, err, &suggestion)
	assert.ErrorIs(t, suggestion.Err, internal.ErrNoArgsProvided)
}

func Test_ExtensionInstallAction_Run_VersionWithMultipleArgs(t *testing.T) {
	t.Parallel()
	action := &extensionInstallAction{
		args:    []string{"ext1", "ext2"},
		flags:   &extensionInstallFlags{version: "1.0.0", global: &internal.GlobalCommandOptions{}},
		console: mockinput.NewMockConsole(),
	}
	_, err := action.Run(context.Background())
	require.Error(t, err)
	var suggestion *internal.ErrorWithSuggestion
	require.ErrorAs(t, err, &suggestion)
	assert.ErrorIs(t, suggestion.Err, internal.ErrInvalidFlagCombination)
}

// =======================================================
// extensionUninstallAction.Run arg validation tests
// =======================================================

func Test_ExtensionUninstallAction_Run_ArgsWithAllFlag(t *testing.T) {
	t.Parallel()
	action := &extensionUninstallAction{
		args:    []string{"ext1"},
		flags:   &extensionUninstallFlags{all: true},
		console: mockinput.NewMockConsole(),
	}
	_, err := action.Run(context.Background())
	require.Error(t, err)
	var suggestion *internal.ErrorWithSuggestion
	require.ErrorAs(t, err, &suggestion)
	assert.ErrorIs(t, suggestion.Err, internal.ErrInvalidFlagCombination)
}

func Test_ExtensionUninstallAction_Run_NoArgsNoAll(t *testing.T) {
	t.Parallel()
	action := &extensionUninstallAction{
		args:    []string{},
		flags:   &extensionUninstallFlags{all: false},
		console: mockinput.NewMockConsole(),
	}
	_, err := action.Run(context.Background())
	require.Error(t, err)
	var suggestion *internal.ErrorWithSuggestion
	require.ErrorAs(t, err, &suggestion)
	assert.ErrorIs(t, suggestion.Err, internal.ErrNoArgsProvided)
}

// =======================================================
// extensionUpgradeAction.Run arg validation tests
// =======================================================

func Test_ExtensionUpgradeAction_Run_ArgsWithAllFlag(t *testing.T) {
	t.Parallel()
	action := &extensionUpgradeAction{
		args:    []string{"ext1"},
		flags:   &extensionUpgradeFlags{all: true, global: &internal.GlobalCommandOptions{}},
		console: mockinput.NewMockConsole(),
	}
	_, err := action.Run(context.Background())
	require.Error(t, err)
	var suggestion *internal.ErrorWithSuggestion
	require.ErrorAs(t, err, &suggestion)
	assert.ErrorIs(t, suggestion.Err, internal.ErrInvalidFlagCombination)
}

func Test_ExtensionUpgradeAction_Run_VersionWithMultipleArgs(t *testing.T) {
	t.Parallel()
	action := &extensionUpgradeAction{
		args:    []string{"ext1", "ext2"},
		flags:   &extensionUpgradeFlags{version: "1.0.0", global: &internal.GlobalCommandOptions{}},
		console: mockinput.NewMockConsole(),
	}
	_, err := action.Run(context.Background())
	require.Error(t, err)
	var suggestion *internal.ErrorWithSuggestion
	require.ErrorAs(t, err, &suggestion)
	assert.ErrorIs(t, suggestion.Err, internal.ErrInvalidFlagCombination)
}

func Test_ExtensionUpgradeAction_Run_NoArgsNoAll(t *testing.T) {
	t.Parallel()
	action := &extensionUpgradeAction{
		args:    []string{},
		flags:   &extensionUpgradeFlags{all: false, global: &internal.GlobalCommandOptions{}},
		console: mockinput.NewMockConsole(),
	}
	_, err := action.Run(context.Background())
	require.Error(t, err)
	var suggestion *internal.ErrorWithSuggestion
	require.ErrorAs(t, err, &suggestion)
	assert.ErrorIs(t, suggestion.Err, internal.ErrNoArgsProvided)
}

// =======================================================
// extensionSourceValidateAction.Run tests
// =======================================================

func Test_ExtensionSourceValidateAction_Run_NoArgs_Guard(t *testing.T) {
	t.Parallel()
	action := &extensionSourceValidateAction{
		args:    []string{},
		flags:   &extensionSourceValidateFlags{},
		console: mockinput.NewMockConsole(),
	}
	_, err := action.Run(context.Background())
	require.Error(t, err)
	var suggestion *internal.ErrorWithSuggestion
	require.ErrorAs(t, err, &suggestion)
	assert.ErrorIs(t, suggestion.Err, internal.ErrNoArgsProvided)
}

func Test_ExtensionSourceValidateAction_Run_TooManyArgs_Guard(t *testing.T) {
	t.Parallel()
	action := &extensionSourceValidateAction{
		args:    []string{"src1", "src2"},
		flags:   &extensionSourceValidateFlags{},
		console: mockinput.NewMockConsole(),
	}
	_, err := action.Run(context.Background())
	require.Error(t, err)
	var suggestion *internal.ErrorWithSuggestion
	require.ErrorAs(t, err, &suggestion)
	assert.ErrorIs(t, suggestion.Err, internal.ErrInvalidFlagCombination)
}

// =======================================================
// getTargetServiceName tests
// =======================================================

func Test_GetTargetServiceName_AllAndService_Conflict(t *testing.T) {
	t.Parallel()
	_, err := getTargetServiceName(context.Background(), nil, nil, nil, "build", "myservice", true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot specify both --all and <service>")
}

// =======================================================
// extension flag constructor tests for coverage
// =======================================================

func Test_NewExtensionListFlags_Constructor(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	flags := newExtensionListFlags(cmd)
	require.NotNil(t, flags)
}

func Test_NewExtensionShowFlags_Constructor(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newExtensionShowFlags(cmd, global)
	require.NotNil(t, flags)
	assert.Equal(t, global, flags.global)
}

func Test_NewExtensionInstallFlags_Constructor(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newExtensionInstallFlags(cmd, global)
	require.NotNil(t, flags)
	assert.Equal(t, global, flags.global)
}

func Test_NewExtensionUninstallFlags_Constructor(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	flags := newExtensionUninstallFlags(cmd)
	require.NotNil(t, flags)
}

func Test_NewExtensionUpgradeFlags_Constructor(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newExtensionUpgradeFlags(cmd, global)
	require.NotNil(t, flags)
	assert.Equal(t, global, flags.global)
}

func Test_NewExtensionSourceAddFlags_Constructor(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	flags := newExtensionSourceAddFlags(cmd)
	require.NotNil(t, flags)
}

func Test_NewExtensionSourceValidateFlags_Constructor(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	flags := newExtensionSourceValidateFlags(cmd)
	require.NotNil(t, flags)
}

// =======================================================
// extensionListItem tests
// =======================================================

func Test_ExtensionListItem_Fields(t *testing.T) {
	t.Parallel()
	item := extensionListItem{
		Id:        "ext.test",
		Name:      "Test Extension",
		Version:   "1.0.0",
		Namespace: "test",
		Source:    "default",
	}
	assert.Equal(t, "ext.test", item.Id)
	assert.Equal(t, "Test Extension", item.Name)
}

// =======================================================
// since() helper test
// =======================================================

func Test_Since_ReturnsNonNegative(t *testing.T) {
	// Reset interact time for clean test
	tracing.InteractTimeMs.Store(0)
	t.Cleanup(func() { tracing.InteractTimeMs.Store(0) })
	import_time := since(time.Now())
	assert.GreaterOrEqual(t, import_time.Nanoseconds(), int64(0))
}

// =======================================================
// updateAction constructor test
// =======================================================

func Test_NewUpdateAction_Fields(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	formatter := &output.NoneFormatter{}
	writer := &bytes.Buffer{}
	flags := &updateFlags{}
	action := newUpdateAction(flags, console, formatter, writer, nil, nil, nil)
	require.NotNil(t, action)
}

func Test_NewUpdateCmd(t *testing.T) {
	t.Parallel()
	cmd := newUpdateCmd()
	require.NotNil(t, cmd)
	assert.Equal(t, "update", cmd.Use)
}

func Test_UpdateFlags_Bind(t *testing.T) {
	t.Parallel()
	flags := &updateFlags{}
	cmd := newUpdateCmd()
	global := &internal.GlobalCommandOptions{}
	flags.Bind(cmd.Flags(), global)
	assert.Equal(t, global, flags.global)
}

// =======================================================
// More cmd constructors for coverage
// =======================================================

func Test_NewBuildCmd(t *testing.T) {
	t.Parallel()
	cmd := newBuildCmd()
	require.NotNil(t, cmd)
	assert.Equal(t, "build <service>", cmd.Use)
}

func Test_NewDownCmd(t *testing.T) {
	t.Parallel()
	cmd := newDownCmd()
	require.NotNil(t, cmd)
	assert.Equal(t, "down [<layer>]", cmd.Use)
}

func Test_NewRestoreCmd(t *testing.T) {
	t.Parallel()
	cmd := newRestoreCmd()
	require.NotNil(t, cmd)
	assert.Contains(t, cmd.Use, "restore")
}

func Test_NewPackageCmd(t *testing.T) {
	t.Parallel()
	cmd := newPackageCmd()
	require.NotNil(t, cmd)
	assert.Contains(t, cmd.Use, "package")
}

func Test_NewMonitorCmd(t *testing.T) {
	t.Parallel()
	cmd := newMonitorCmd()
	require.NotNil(t, cmd)
	assert.Equal(t, "monitor", cmd.Use)
}

func Test_NewUpCmd(t *testing.T) {
	t.Parallel()
	cmd := newUpCmd()
	require.NotNil(t, cmd)
	assert.Equal(t, "up", cmd.Use)
}

func Test_NewPipelineConfigCmd(t *testing.T) {
	t.Parallel()
	cmd := newPipelineConfigCmd()
	require.NotNil(t, cmd)
	assert.Equal(t, "config", cmd.Use)
}

func Test_NewInfraCreateCmd(t *testing.T) {
	t.Parallel()
	cmd := newInfraCreateCmd()
	require.NotNil(t, cmd)
	assert.Contains(t, cmd.Use, "create")
}

func Test_NewInfraDeleteCmd(t *testing.T) {
	t.Parallel()
	cmd := newInfraDeleteCmd()
	require.NotNil(t, cmd)
	assert.Contains(t, cmd.Use, "delete")
}

// =======================================================
// ErrorWithSuggestion formatting
// =======================================================

func Test_ErrorWithSuggestion_Error(t *testing.T) {
	t.Parallel()
	err := &internal.ErrorWithSuggestion{
		Err:        fmt.Errorf("test error"),
		Suggestion: "try again",
	}
	assert.Contains(t, err.Error(), "test error")
}

func Test_ErrorWithSuggestion_Unwrap(t *testing.T) {
	t.Parallel()
	inner := fmt.Errorf("inner error")
	err := &internal.ErrorWithSuggestion{
		Err:        inner,
		Suggestion: "suggestion",
	}
	assert.ErrorIs(t, err, inner)
}
