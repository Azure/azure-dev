// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tool"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolCommandGating(t *testing.T) {
	// The "tool" command group is always registered, regardless of any
	// alpha feature gating.
	configDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", configDir)

	root := NewRootCmd(true, nil, nil)
	found := false
	for _, c := range root.Commands() {
		if c.Name() == "tool" {
			found = true
			break
		}
	}
	require.True(t, found, "expected 'tool' subcommand to always be present")
}

func TestRunToolOperationUnsuccessfulResultReturnsError(t *testing.T) {
	toolDef := &tool.ToolDefinition{
		Id:   "az-cli",
		Name: "Azure CLI",
	}
	console := mockinput.NewMockConsole()

	outcome := runToolOperation(
		t.Context(),
		[]*tool.ToolDefinition{toolDef},
		func(ctx context.Context, ids []string) ([]*tool.InstallResult, error) {
			return []*tool.InstallResult{
				{
					Tool:    toolDef,
					Success: false,
				},
			}, nil
		},
		"Installing",
		"install",
		console,
	)
	results, err := outcome.Items, outcome.Err

	require.Error(t, err)
	require.Len(t, results, 1)
	assert.False(t, results[0].Success)
	require.NotEmpty(t, console.Output())
	assert.Contains(t, console.Output()[0], "Some tools could not be")
}

func TestRunToolOperationSuccessfulResultReturnsNoError(t *testing.T) {
	toolDef := &tool.ToolDefinition{
		Id:   "az-cli",
		Name: "Azure CLI",
	}
	console := mockinput.NewMockConsole()

	outcome := runToolOperation(
		t.Context(),
		[]*tool.ToolDefinition{toolDef},
		func(ctx context.Context, ids []string) ([]*tool.InstallResult, error) {
			assert.Equal(t, []string{"az-cli"}, ids)

			return []*tool.InstallResult{
				{
					Tool:             toolDef,
					Success:          true,
					InstalledVersion: "2.73.0",
				},
			}, nil
		},
		"Installing",
		"install",
		console,
	)
	results, err := outcome.Items, outcome.Err

	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.True(t, results[0].Success)
	assert.Equal(t, "az-cli", results[0].Id)
	assert.Equal(t, "Azure CLI", results[0].Name)
	assert.Equal(t, "install", results[0].Action)
	assert.Equal(t, "2.73.0", results[0].InstalledVersion)
	assert.Empty(t, console.Output())
}

// lookupToolStrUsage returns the most recent string-valued usage attribute
// for the given key (assumes the key was set via .String()).
func lookupToolStrUsage(key string) (string, bool) {
	for _, a := range tracing.GetUsageAttributes() {
		if string(a.Key) == key {
			return a.Value.AsString(), true
		}
	}
	return "", false
}

// lookupToolIntUsage returns the most recent int64-valued usage attribute
// for the given key (assumes the key was set via .Int() / .Int64()).
func lookupToolIntUsage(key string) (int64, bool) {
	for _, a := range tracing.GetUsageAttributes() {
		if string(a.Key) == key {
			return a.Value.AsInt64(), true
		}
	}
	return 0, false
}

func TestEmitToolInstallTelemetry_AllSucceeded(t *testing.T) {
	tracing.ResetUsageAttributesForTest()

	results := []*tool.InstallResult{
		{Tool: &tool.ToolDefinition{Id: "kubectl"}, Success: true},
		{Tool: &tool.ToolDefinition{Id: "helm"}, Success: true},
	}
	emitToolInstallTelemetry(results, 250*time.Millisecond, nil, nil)

	got, ok := lookupToolIntUsage(string(fields.ToolInstallSuccessCountKey.Key))
	require.True(t, ok)
	assert.Equal(t, int64(2), got)

	got, ok = lookupToolIntUsage(string(fields.ToolInstallFailureCountKey.Key))
	require.True(t, ok)
	assert.Equal(t, int64(0), got)

	got, ok = lookupToolIntUsage(string(fields.ToolInstallDurationMsKey.Key))
	require.True(t, ok)
	assert.Equal(t, int64(250), got)

	_, ok = lookupToolStrUsage(string(fields.ToolInstallFailedIdsKey.Key))
	assert.False(t, ok, "failed_ids should not be emitted when there are no failures")
}

func TestEmitToolInstallTelemetry_MixedSuccessAndFailure(t *testing.T) {
	tracing.ResetUsageAttributesForTest()

	results := []*tool.InstallResult{
		{Tool: &tool.ToolDefinition{Id: "kubectl"}, Success: true},
		{Tool: &tool.ToolDefinition{Id: "helm"}, Success: false, Error: errors.New("network failure")},
		{Tool: &tool.ToolDefinition{Id: "terraform"}, Success: false, Error: errors.New("not found")},
	}
	emitToolInstallTelemetry(results, 1*time.Second, nil, nil)

	got, ok := lookupToolIntUsage(string(fields.ToolInstallSuccessCountKey.Key))
	require.True(t, ok)
	assert.Equal(t, int64(1), got)

	got, ok = lookupToolIntUsage(string(fields.ToolInstallFailureCountKey.Key))
	require.True(t, ok)
	assert.Equal(t, int64(2), got)

	got, ok = lookupToolIntUsage(string(fields.ToolInstallDurationMsKey.Key))
	require.True(t, ok)
	assert.Equal(t, int64(1000), got)

	gotStr, ok := lookupToolStrUsage(string(fields.ToolInstallFailedIdsKey.Key))
	require.True(t, ok)
	assert.Equal(t, "helm,terraform", gotStr)
}

func TestEmitToolInstallTelemetry_FailureWithNilTool(t *testing.T) {
	tracing.ResetUsageAttributesForTest()

	results := []*tool.InstallResult{
		{Tool: nil, Success: false, Error: errors.New("orphan failure")},
	}
	emitToolInstallTelemetry(results, 10*time.Millisecond, nil, nil)

	got, ok := lookupToolIntUsage(string(fields.ToolInstallFailureCountKey.Key))
	require.True(t, ok)
	assert.Equal(t, int64(1), got)

	_, ok = lookupToolStrUsage(string(fields.ToolInstallFailedIdsKey.Key))
	assert.False(t, ok, "failed_ids must not be emitted when the failure has no Tool reference")
}

func TestEmitToolInstallTelemetry_EmptyResults(t *testing.T) {
	emitToolInstallTelemetry(nil, 0, nil, nil)

	got, ok := lookupToolIntUsage(string(fields.ToolInstallSuccessCountKey.Key))
	require.True(t, ok)
	assert.Equal(t, int64(0), got)

	got, ok = lookupToolIntUsage(string(fields.ToolInstallFailureCountKey.Key))
	require.True(t, ok)
	assert.Equal(t, int64(0), got)
}

// TestEmitToolInstallTelemetry_OperationErrorSynthesizesFailures asserts that
// when the batch operation itself fails (opErr != nil, no per-tool results),
// every requested tool is recorded as a failure so the span is distinguishable
// from a no-op and the invariant success_count + failure_count ==
// len(requested) is preserved.
func TestEmitToolInstallTelemetry_OperationErrorSynthesizesFailures(t *testing.T) {
	tracing.ResetUsageAttributesForTest()

	requested := []*tool.ToolDefinition{
		{Id: "kubectl"},
		{Id: "helm"},
	}
	emitToolInstallTelemetry(nil, 42*time.Millisecond, errors.New("network failure"), requested)

	got, ok := lookupToolIntUsage(string(fields.ToolInstallSuccessCountKey.Key))
	require.True(t, ok)
	assert.Equal(t, int64(0), got)

	got, ok = lookupToolIntUsage(string(fields.ToolInstallFailureCountKey.Key))
	require.True(t, ok)
	assert.Equal(t, int64(2), got)

	got, ok = lookupToolIntUsage(string(fields.ToolInstallDurationMsKey.Key))
	require.True(t, ok)
	assert.Equal(t, int64(42), got)

	gotStr, ok := lookupToolStrUsage(string(fields.ToolInstallFailedIdsKey.Key))
	require.True(t, ok)
	assert.Equal(t, "helm,kubectl", gotStr)
}

// TestEmitToolInstallTelemetry_OperationErrorWithResultsUsesResults asserts
// that when results are present, the opErr / requested fallback path is
// skipped and counts come from the per-tool results (the normal case where
// some tools succeeded and some failed but the taskList reported an
// aggregate error).
func TestEmitToolInstallTelemetry_OperationErrorWithResultsUsesResults(t *testing.T) {
	tracing.ResetUsageAttributesForTest()

	requested := []*tool.ToolDefinition{
		{Id: "kubectl"},
		{Id: "helm"},
	}
	results := []*tool.InstallResult{
		{Tool: &tool.ToolDefinition{Id: "kubectl"}, Success: true},
		{Tool: &tool.ToolDefinition{Id: "helm"}, Success: false, Error: errors.New("boom")},
	}
	emitToolInstallTelemetry(results, 10*time.Millisecond, errors.New("partial"), requested)

	got, ok := lookupToolIntUsage(string(fields.ToolInstallSuccessCountKey.Key))
	require.True(t, ok)
	assert.Equal(t, int64(1), got)

	got, ok = lookupToolIntUsage(string(fields.ToolInstallFailureCountKey.Key))
	require.True(t, ok)
	assert.Equal(t, int64(1), got)

	gotStr, ok := lookupToolStrUsage(string(fields.ToolInstallFailedIdsKey.Key))
	require.True(t, ok)
	assert.Equal(t, "helm", gotStr)
}

// TestToolShowAction_InvalidArgDoesNotEmitToolId locks the privacy
// contract: when a user passes an unknown / bogus tool ID to
// `azd tool show`, `tool.id` must not be emitted, because the value
// would otherwise be a raw user-supplied string.
func TestToolShowAction_InvalidArgDoesNotEmitToolId(t *testing.T) {
	tracing.ResetUsageAttributesForTest()

	manager := tool.NewManager(nil, nil, nil)
	action := newToolShowAction(
		[]string{"definitely-not-a-real-tool"},
		manager,
		mockinput.NewMockConsole(),
		nil,
		nil,
	)

	_, err := action.Run(t.Context())
	require.Error(t, err)

	_, ok := lookupToolStrUsage(string(fields.ToolIdKey.Key))
	assert.False(t, ok,
		"tool.id must not be emitted when FindTool fails on an unknown tool")
}

// lookupToolBoolUsage returns the most recent bool-valued usage attribute
// for the given key (assumes the key was set via .Bool()).
func lookupToolBoolUsage(key string) (bool, bool) {
	for _, a := range tracing.GetUsageAttributes() {
		if string(a.Key) == key {
			return a.Value.AsBool(), true
		}
	}
	return false, false
}

// ---------------------------------------------------------------------------
// cmd-level mock Detector and Installer for behavioral action tests.
// ---------------------------------------------------------------------------

type cmdMockDetector struct {
	detectTool func(ctx context.Context, t *tool.ToolDefinition) (*tool.ToolStatus, error)
	detectAll  func(ctx context.Context, tools []*tool.ToolDefinition) ([]*tool.ToolStatus, error)
}

func (m *cmdMockDetector) DetectTool(ctx context.Context, t *tool.ToolDefinition) (*tool.ToolStatus, error) {
	if m.detectTool != nil {
		return m.detectTool(ctx, t)
	}
	return &tool.ToolStatus{Tool: t}, nil
}

func (m *cmdMockDetector) DetectAll(
	ctx context.Context, tools []*tool.ToolDefinition,
) ([]*tool.ToolStatus, error) {
	if m.detectAll != nil {
		return m.detectAll(ctx, tools)
	}
	results := make([]*tool.ToolStatus, len(tools))
	for i, t := range tools {
		results[i] = &tool.ToolStatus{Tool: t}
	}
	return results, nil
}

func (m *cmdMockDetector) DetectSkillHosts(
	ctx context.Context, t *tool.ToolDefinition,
) ([]tool.InstalledSkillHost, error) {
	return nil, nil
}

type cmdMockInstaller struct {
	install func(
		ctx context.Context, t *tool.ToolDefinition, opts ...tool.InstallOption,
	) (*tool.InstallResult, error)
	upgrade func(
		ctx context.Context, t *tool.ToolDefinition, opts ...tool.InstallOption,
	) (*tool.InstallResult, error)
	uninstall func(
		ctx context.Context, t *tool.ToolDefinition, opts ...tool.InstallOption,
	) (*tool.InstallResult, error)
	availableSkillHosts func(ctx context.Context, t *tool.ToolDefinition) (commands []string, names []string)
}

func (m *cmdMockInstaller) Install(
	ctx context.Context, t *tool.ToolDefinition, opts ...tool.InstallOption,
) (*tool.InstallResult, error) {
	if m.install != nil {
		return m.install(ctx, t, opts...)
	}
	return &tool.InstallResult{Tool: t, Success: true}, nil
}

func (m *cmdMockInstaller) Upgrade(
	ctx context.Context, t *tool.ToolDefinition, opts ...tool.InstallOption,
) (*tool.InstallResult, error) {
	if m.upgrade != nil {
		return m.upgrade(ctx, t, opts...)
	}
	return &tool.InstallResult{Tool: t, Success: true}, nil
}

func (m *cmdMockInstaller) AvailableSkillHosts(
	ctx context.Context,
	t *tool.ToolDefinition,
) (commands []string, names []string) {
	if m.availableSkillHosts != nil {
		return m.availableSkillHosts(ctx, t)
	}
	return nil, nil
}

// mockAvailableSkillHosts returns commands unchanged plus the display name for
// each, derived from the tool's SkillHosts (falling back to the command when
// no host matches). It mirrors installer.AvailableSkillHosts so the mock
// yields the same (commands, names) shape from a plain list of commands.
func mockAvailableSkillHosts(td *tool.ToolDefinition, commands []string) ([]string, []string) {
	if len(commands) == 0 {
		return nil, nil
	}
	names := make([]string, len(commands))
	for i, c := range commands {
		names[i] = c
		for _, h := range td.SkillHosts {
			if h.Command == c {
				names[i] = h.Host
				break
			}
		}
	}
	return commands, names
}

func (m *cmdMockInstaller) Uninstall(
	ctx context.Context, t *tool.ToolDefinition, opts ...tool.InstallOption,
) (*tool.InstallResult, error) {
	if m.uninstall != nil {
		return m.uninstall(ctx, t, opts...)
	}
	return &tool.InstallResult{Tool: t, Success: true}, nil
}

// ---------------------------------------------------------------------------
// Behavioral tests for toolInstallAction.Run / toolUpgradeAction.Run
// ---------------------------------------------------------------------------

// TestToolInstallAction_DryRun_SingleTool_EmitsToolIdAndDryRun verifies that a
// single-tool dry-run install emits tool.id (not tool.ids) and tool.dry_run=true,
// honoring the mutual-exclusion contract between tool.id and tool.ids.
func TestToolInstallAction_DryRun_SingleTool_EmitsToolIdAndDryRun(t *testing.T) {
	tracing.ResetUsageAttributesForTest()

	manager := tool.NewManager(&cmdMockDetector{}, &cmdMockInstaller{}, nil)
	action := newToolInstallAction(
		[]string{"az-cli"},
		&toolInstallFlags{dryRun: true},
		manager,
		mockinput.NewMockConsole(),
		&output.NoneFormatter{},
		io.Discard,
	)

	_, err := action.Run(t.Context())
	require.NoError(t, err)

	gotID, ok := lookupToolStrUsage(string(fields.ToolIdKey.Key))
	require.True(t, ok, "tool.id must be emitted on single-tool dry-run")
	assert.Equal(t, "az-cli", gotID)

	_, ok = lookupToolStrUsage(string(fields.ToolIdsKey.Key))
	assert.False(t, ok, "tool.ids must NOT be emitted alongside tool.id (mutual exclusion)")

	gotDry, ok := lookupToolBoolUsage(string(fields.ToolDryRunKey.Key))
	require.True(t, ok, "tool.dry_run must be emitted alongside tool.id")
	assert.True(t, gotDry)
}

// TestToolInstallAction_DryRun_MultiTool_EmitsSortedToolIds verifies that a
// multi-tool dry-run install emits a sorted tool.ids (no tool.id), keeping
// attribute cardinality bounded.
func TestToolInstallAction_DryRun_MultiTool_EmitsSortedToolIds(t *testing.T) {
	tracing.ResetUsageAttributesForTest()

	manager := tool.NewManager(&cmdMockDetector{}, &cmdMockInstaller{}, nil)
	// Args intentionally in reverse-sorted order to verify sorting in the emit.
	action := newToolInstallAction(
		[]string{"github-copilot-cli", "az-cli"},
		&toolInstallFlags{dryRun: true},
		manager,
		mockinput.NewMockConsole(),
		&output.NoneFormatter{},
		io.Discard,
	)

	_, err := action.Run(t.Context())
	require.NoError(t, err)

	gotIDs, ok := lookupToolStrUsage(string(fields.ToolIdsKey.Key))
	require.True(t, ok, "tool.ids must be emitted on multi-tool dry-run")
	assert.Equal(t, "az-cli,github-copilot-cli", gotIDs,
		"tool.ids must be a sorted, comma-joined list (cardinality control)")

	_, ok = lookupToolStrUsage(string(fields.ToolIdKey.Key))
	assert.False(t, ok, "tool.id must NOT be emitted alongside tool.ids (mutual exclusion)")
}

// TestToolInstallAction_AllFailureBatch_EmitsCorrectAggregates exercises the
// install path end-to-end against a mock Installer that fails every per-tool
// install. It verifies the aggregate telemetry contract:
//   - success_count == 0
//   - failure_count == len(requested)
//   - failed_ids is the sorted set of requested IDs
//   - tool.ids is the sorted set of requested IDs
//
// This locks the C1 (operation-failure) and H5 (mutual exclusion + sort) fixes
// at the integration level — previously only emitToolInstallTelemetry was
// covered by unit tests in isolation.
func TestToolInstallAction_AllFailureBatch_EmitsCorrectAggregates(t *testing.T) {
	tracing.ResetUsageAttributesForTest()

	installer := &cmdMockInstaller{
		install: func(_ context.Context, td *tool.ToolDefinition, _ ...tool.InstallOption) (*tool.InstallResult, error) {
			return &tool.InstallResult{
				Tool:    td,
				Success: false,
				Error:   errors.New("install failed for " + td.Id),
			}, errors.New("install failed for " + td.Id)
		},
	}
	manager := tool.NewManager(&cmdMockDetector{}, installer, nil)

	action := newToolInstallAction(
		[]string{"github-copilot-cli", "az-cli"},
		&toolInstallFlags{},
		manager,
		mockinput.NewMockConsole(),
		&output.NoneFormatter{},
		io.Discard,
	)

	// The action signals partial/total failure by returning a non-nil result;
	// the error may or may not propagate depending on runToolOperation's
	// aggregation, but the telemetry contract must hold either way.
	_, _ = action.Run(t.Context())

	gotIDs, ok := lookupToolStrUsage(string(fields.ToolIdsKey.Key))
	require.True(t, ok, "tool.ids must be emitted on multi-tool install")
	assert.Equal(t, "az-cli,github-copilot-cli", gotIDs)

	gotSuccess, ok := lookupToolIntUsage(string(fields.ToolInstallSuccessCountKey.Key))
	require.True(t, ok)
	assert.Equal(t, int64(0), gotSuccess)

	gotFail, ok := lookupToolIntUsage(string(fields.ToolInstallFailureCountKey.Key))
	require.True(t, ok)
	assert.Equal(t, int64(2), gotFail, "all per-tool failures must be counted (or synthesized) on a total-failure batch")

	gotFailedIDs, ok := lookupToolStrUsage(string(fields.ToolInstallFailedIdsKey.Key))
	require.True(t, ok)
	assert.Equal(t, "az-cli,github-copilot-cli", gotFailedIDs,
		"failed_ids must be a sorted, comma-joined list matching the failed tools")
}

// TestToolInstallAction_Failure_ReturnsErrorNotSuccess is a regression test
// for a bug where a failed install still returned a success ActionResult,
// causing the UX middleware to print "SUCCESS: Tool installation complete"
// after the per-tool failures. On failure the action must return a non-nil
// error and no success message so the command exits non-zero.
func TestToolInstallAction_Failure_ReturnsErrorNotSuccess(t *testing.T) {
	tracing.ResetUsageAttributesForTest()

	installer := &cmdMockInstaller{
		install: func(_ context.Context, td *tool.ToolDefinition, _ ...tool.InstallOption) (*tool.InstallResult, error) {
			return &tool.InstallResult{
				Tool:    td,
				Success: false,
				Error:   errors.New("install failed for " + td.Id),
			}, errors.New("install failed for " + td.Id)
		},
	}
	manager := tool.NewManager(&cmdMockDetector{}, installer, nil)

	action := newToolInstallAction(
		[]string{"az-cli"},
		&toolInstallFlags{},
		manager,
		mockinput.NewMockConsole(),
		&output.NoneFormatter{},
		io.Discard,
	)

	result, err := action.Run(t.Context())

	require.Error(t, err, "a failed install must return a non-nil error")
	assert.Nil(t, result,
		"a failed install must not return a success ActionResult (would print SUCCESS)")
}

// TestToolUpgradeAction_SuccessEmitsFromAndToVersion exercises the upgrade
// path end-to-end and verifies:
//   - tool.upgrade.from_version is emitted from DetectTool (H2: no UX change,
//     reuses the existing detector)
//   - tool.upgrade.to_version is emitted only on Success and reflects the
//     installer's InstalledVersion (H3)
//   - tool.id is emitted (single-tool, not tool.ids)
func TestToolUpgradeAction_SuccessEmitsFromAndToVersion(t *testing.T) {
	tracing.ResetUsageAttributesForTest()

	detector := &cmdMockDetector{
		detectTool: func(_ context.Context, td *tool.ToolDefinition) (*tool.ToolStatus, error) {
			return &tool.ToolStatus{
				Tool:             td,
				Installed:        true,
				InstalledVersion: "1.0.0",
			}, nil
		},
	}
	installer := &cmdMockInstaller{
		upgrade: func(_ context.Context, td *tool.ToolDefinition, _ ...tool.InstallOption) (*tool.InstallResult, error) {
			return &tool.InstallResult{
				Tool:             td,
				Success:          true,
				InstalledVersion: "2.0.0",
				Strategy:         "winget",
			}, nil
		},
	}
	manager := tool.NewManager(detector, installer, nil)

	action := newToolUpgradeAction(
		[]string{"az-cli"},
		&toolUpgradeFlags{},
		manager,
		mockinput.NewMockConsole(),
		&output.NoneFormatter{},
		io.Discard,
	)

	_, err := action.Run(t.Context())
	require.NoError(t, err)

	gotID, ok := lookupToolStrUsage(string(fields.ToolIdKey.Key))
	require.True(t, ok)
	assert.Equal(t, "az-cli", gotID)

	gotFrom, ok := lookupToolStrUsage(string(fields.ToolUpgradeFromVersionKey.Key))
	require.True(t, ok, "tool.upgrade.from_version must be emitted from detector output")
	assert.Equal(t, "1.0.0", gotFrom)

	gotTo, ok := lookupToolStrUsage(string(fields.ToolUpgradeToVersionKey.Key))
	require.True(t, ok, "tool.upgrade.to_version must be emitted on success")
	assert.Equal(t, "2.0.0", gotTo)
}

// TestToolUpgradeAction_FailureDoesNotEmitToVersion verifies the H3 contract:
// when the upgrade fails, tool.upgrade.to_version is NOT emitted (since there
// is no successfully-installed version to report).
func TestToolUpgradeAction_FailureDoesNotEmitToVersion(t *testing.T) {
	tracing.ResetUsageAttributesForTest()

	detector := &cmdMockDetector{
		detectTool: func(_ context.Context, td *tool.ToolDefinition) (*tool.ToolStatus, error) {
			return &tool.ToolStatus{
				Tool:             td,
				Installed:        true,
				InstalledVersion: "1.0.0",
			}, nil
		},
	}
	installer := &cmdMockInstaller{
		upgrade: func(_ context.Context, td *tool.ToolDefinition, _ ...tool.InstallOption) (*tool.InstallResult, error) {
			return &tool.InstallResult{
				Tool:    td,
				Success: false,
				Error:   errors.New("upgrade failed"),
			}, errors.New("upgrade failed")
		},
	}
	manager := tool.NewManager(detector, installer, nil)

	action := newToolUpgradeAction(
		[]string{"az-cli"},
		&toolUpgradeFlags{},
		manager,
		mockinput.NewMockConsole(),
		&output.NoneFormatter{},
		io.Discard,
	)

	_, _ = action.Run(t.Context())

	// from_version still emits (detected before the failed upgrade).
	gotFrom, ok := lookupToolStrUsage(string(fields.ToolUpgradeFromVersionKey.Key))
	require.True(t, ok)
	assert.Equal(t, "1.0.0", gotFrom)

	// to_version must be absent — there is no installed version to report.
	_, ok = lookupToolStrUsage(string(fields.ToolUpgradeToVersionKey.Key))
	assert.False(t, ok, "tool.upgrade.to_version must not be emitted on upgrade failure")
}

// ---------------------------------------------------------------------------
// resolveHostOptions — --agent / --all-agents flag handling
// ---------------------------------------------------------------------------

func TestResolveHostOptions(t *testing.T) {
	skill := &tool.ToolDefinition{
		Id:       "azure-skills",
		Name:     "Azure Skills",
		Category: tool.ToolCategorySkill,
		SkillHosts: []tool.SkillHost{
			{Host: "GitHub Copilot CLI", Command: "copilot"},
			{Host: "Claude Code CLI", Command: "claude"},
		},
	}
	nonSkill := &tool.ToolDefinition{
		Id:       "azure-mcp-server",
		Category: tool.ToolCategoryServer,
	}

	newAction := func(args []string, flags *toolInstallFlags, present []string) *toolInstallAction {
		installer := &cmdMockInstaller{
			availableSkillHosts: func(_ context.Context, td *tool.ToolDefinition) ([]string, []string) {
				return mockAvailableSkillHosts(td, present)
			},
		}
		manager := tool.NewManager(&cmdMockDetector{}, installer, nil)
		return newToolInstallAction(
			args, flags, manager,
			mockinput.NewMockConsole(), &output.NoneFormatter{}, io.Discard,
		).(*toolInstallAction)
	}

	ctx := context.Background()

	t.Run("HostWithoutSkillTool", func(t *testing.T) {
		a := newAction(nil, &toolInstallFlags{hosts: []string{"copilot"}}, nil)
		_, err := a.resolveHostOptions(ctx, []*tool.ToolDefinition{nonSkill})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "only applies to skill tools")
	})

	t.Run("HostAllCannotMixWithSpecificHosts", func(t *testing.T) {
		a := newAction(nil, &toolInstallFlags{hosts: []string{"all", "copilot"}}, []string{"copilot", "claude"})
		_, err := a.resolveHostOptions(ctx, []*tool.ToolDefinition{skill})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be combined with specific agents")
	})

	t.Run("ExplicitHostsReturnsOptions", func(t *testing.T) {
		a := newAction(nil, &toolInstallFlags{hosts: []string{"claude"}}, []string{"copilot", "claude"})
		opts, err := a.resolveHostOptions(ctx, []*tool.ToolDefinition{skill})
		require.NoError(t, err)
		assert.Len(t, opts, 1)
	})

	t.Run("HostAllReturnsDeferredOption", func(t *testing.T) {
		a := newAction(nil, &toolInstallFlags{hosts: []string{"all"}}, []string{"copilot", "claude"})
		opts, err := a.resolveHostOptions(ctx, []*tool.ToolDefinition{skill})
		require.NoError(t, err)
		assert.Len(t, opts, 1)
	})

	t.Run("HostAllDefersEvenWhenNoneDetected", func(t *testing.T) {
		// --agent all resolves at install time, so it returns an option
		// even when no host is on PATH yet (the installer surfaces the
		// no-host guidance later).
		a := newAction(nil, &toolInstallFlags{hosts: []string{"all"}}, nil)
		opts, err := a.resolveHostOptions(ctx, []*tool.ToolDefinition{skill})
		require.NoError(t, err)
		assert.Len(t, opts, 1)
	})

	t.Run("ExplicitlyNamedMultipleHostsAsksToChoose", func(t *testing.T) {
		// `azd tool install azure-skills` (skill named in args) with
		// several hosts present in a non-interactive terminal must surface
		// the guidance error asking the user to choose.
		a := newAction([]string{"azure-skills"}, &toolInstallFlags{}, []string{"copilot", "claude"})
		_, err := a.resolveHostOptions(ctx, []*tool.ToolDefinition{skill})
		require.Error(t, err)
		var sug *internal.ErrorWithSuggestion
		require.ErrorAs(t, err, &sug)
		assert.Contains(t, sug.Message, "GitHub Copilot CLI, Claude Code CLI")
		assert.Contains(t, sug.Suggestion, "--agent all")
	})

	t.Run("ExplicitlyNamedMultipleHostsInteractivePrompts", func(t *testing.T) {
		// In an interactive terminal the user is prompted to pick the
		// host(s) instead of erroring out. The picker shows friendly Host
		// display names, and the user's selection maps back to the command.
		a := newAction([]string{"azure-skills"}, &toolInstallFlags{}, []string{"copilot", "claude"})
		mockConsole := a.console.(*mockinput.MockConsole)
		mockConsole.SetTerminal(true)
		var prompted []string
		mockConsole.WhenMultiSelect(func(options input.ConsoleOptions) bool {
			prompted = options.Options
			return true
		}).Respond([]string{"Claude Code CLI"})

		opts, err := a.resolveHostOptions(ctx, []*tool.ToolDefinition{skill})
		require.NoError(t, err)
		assert.Len(t, opts, 1)
		// The picker offers friendly display names, not command identities.
		assert.Equal(t, []string{"GitHub Copilot CLI", "Claude Code CLI"}, prompted)
	})

	t.Run("ExplicitlyNamedMultipleHostsPromptErrorPropagates", func(t *testing.T) {
		// A failing prompt surfaces the error rather than silently
		// falling back.
		a := newAction([]string{"azure-skills"}, &toolInstallFlags{}, []string{"copilot", "claude"})
		mockConsole := a.console.(*mockinput.MockConsole)
		mockConsole.SetTerminal(true)
		mockConsole.WhenMultiSelect(func(options input.ConsoleOptions) bool {
			return true
		}).RespondFn(func(_ input.ConsoleOptions) (any, error) {
			return []string(nil), errors.New("prompt boom")
		})

		_, err := a.resolveHostOptions(ctx, []*tool.ToolDefinition{skill})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "prompt boom")
	})

	t.Run("ExplicitlyNamedMultipleHostsEmptySelectionFallsBackToError", func(t *testing.T) {
		// Selecting nothing in the picker falls back to the guidance
		// error telling the user to re-run with --agent.
		a := newAction([]string{"azure-skills"}, &toolInstallFlags{}, []string{"copilot", "claude"})
		mockConsole := a.console.(*mockinput.MockConsole)
		mockConsole.SetTerminal(true)
		mockConsole.WhenMultiSelect(func(options input.ConsoleOptions) bool {
			return true
		}).Respond([]string{})

		_, err := a.resolveHostOptions(ctx, []*tool.ToolDefinition{skill})
		require.Error(t, err)
		var sug *internal.ErrorWithSuggestion
		require.ErrorAs(t, err, &sug)
		assert.Contains(t, sug.Suggestion, "--agent all")
	})

	t.Run("ExplicitUnavailableHostInteractivePrompts", func(t *testing.T) {
		// `--agent gemini` names a host that isn't supported/available.
		// In an interactive terminal we prompt over the hosts that ARE
		// on PATH instead of hard-failing.
		a := newAction(
			[]string{"azure-skills"},
			&toolInstallFlags{hosts: []string{"gemini"}},
			[]string{"copilot", "claude"},
		)
		mockConsole := a.console.(*mockinput.MockConsole)
		mockConsole.SetTerminal(true)
		var prompted []string
		mockConsole.WhenMultiSelect(func(options input.ConsoleOptions) bool {
			prompted = options.Options
			return true
		}).Respond([]string{"GitHub Copilot CLI"})

		opts, err := a.resolveHostOptions(ctx, []*tool.ToolDefinition{skill})
		require.NoError(t, err)
		assert.Len(t, opts, 1)
		// The picker offered the available hosts (friendly display names),
		// not the bogus request.
		assert.Equal(t, []string{"GitHub Copilot CLI", "Claude Code CLI"}, prompted)
	})

	t.Run("ExplicitUnavailableHostNonInteractivePassesThrough", func(t *testing.T) {
		// Without a TTY we cannot prompt, so the request is passed
		// through unchanged for the installer to validate and reject.
		a := newAction(
			[]string{"azure-skills"},
			&toolInstallFlags{hosts: []string{"gemini"}},
			[]string{"copilot", "claude"},
		)
		mockConsole := a.console.(*mockinput.MockConsole)
		prompted := false
		mockConsole.WhenMultiSelect(func(options input.ConsoleOptions) bool {
			prompted = true
			return true
		}).Respond([]string{"copilot"})

		opts, err := a.resolveHostOptions(ctx, []*tool.ToolDefinition{skill})
		require.NoError(t, err)
		assert.Len(t, opts, 1)
		assert.False(t, prompted, "must not prompt without a terminal")
	})

	t.Run("ExplicitUnavailableHostNoneOnPathDefersToGuidance", func(t *testing.T) {
		// `--agent gemini` with no supported host on PATH: skip the picker
		// and target every available host so the installer surfaces its
		// install-a-CLI-host guidance.
		a := newAction(
			[]string{"azure-skills"},
			&toolInstallFlags{hosts: []string{"gemini"}},
			nil,
		)
		mockConsole := a.console.(*mockinput.MockConsole)
		mockConsole.SetTerminal(true)
		prompted := false
		mockConsole.WhenMultiSelect(func(options input.ConsoleOptions) bool {
			prompted = true
			return true
		}).Respond([]string{"copilot"})

		opts, err := a.resolveHostOptions(ctx, []*tool.ToolDefinition{skill})
		require.NoError(t, err)
		assert.Len(t, opts, 1)
		assert.False(t, prompted, "must not prompt when no host is available")
	})

	t.Run("ExplicitUnavailableHostMultiSelectErrorPropagates", func(t *testing.T) {
		// A failing picker during the unavailable-host fallback surfaces
		// the error.
		a := newAction(
			[]string{"azure-skills"},
			&toolInstallFlags{hosts: []string{"gemini"}},
			[]string{"copilot", "claude"},
		)
		mockConsole := a.console.(*mockinput.MockConsole)
		mockConsole.SetTerminal(true)
		mockConsole.WhenMultiSelect(func(options input.ConsoleOptions) bool {
			return true
		}).RespondFn(func(_ input.ConsoleOptions) (any, error) {
			return []string(nil), errors.New("picker boom")
		})

		_, err := a.resolveHostOptions(ctx, []*tool.ToolDefinition{skill})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "picker boom")
	})

	t.Run("ExplicitUnavailableHostEmptySelectionPassesThrough", func(t *testing.T) {
		// Selecting nothing leaves the original request intact so the
		// installer surfaces its validation error for the bad host.
		a := newAction(
			[]string{"azure-skills"},
			&toolInstallFlags{hosts: []string{"gemini"}},
			[]string{"copilot", "claude"},
		)
		mockConsole := a.console.(*mockinput.MockConsole)
		mockConsole.SetTerminal(true)
		mockConsole.WhenMultiSelect(func(options input.ConsoleOptions) bool {
			return true
		}).Respond([]string{})

		opts, err := a.resolveHostOptions(ctx, []*tool.ToolDefinition{skill})
		require.NoError(t, err)
		assert.Len(t, opts, 1)
	})

	t.Run("ExplicitAvailableHostSkipsPrompt", func(t *testing.T) {
		// A valid, available --agent is used directly without prompting,
		// even in an interactive terminal.
		a := newAction(
			[]string{"azure-skills"},
			&toolInstallFlags{hosts: []string{"copilot"}},
			[]string{"copilot", "claude"},
		)
		mockConsole := a.console.(*mockinput.MockConsole)
		mockConsole.SetTerminal(true)
		prompted := false
		mockConsole.WhenMultiSelect(func(options input.ConsoleOptions) bool {
			prompted = true
			return true
		}).Respond([]string{"claude"})

		opts, err := a.resolveHostOptions(ctx, []*tool.ToolDefinition{skill})
		require.NoError(t, err)
		assert.Len(t, opts, 1)
		assert.False(t, prompted, "available host must not trigger a prompt")
	})

	t.Run("ExplicitlyNamedSingleHostReturnsNil", func(t *testing.T) {
		a := newAction([]string{"azure-skills"}, &toolInstallFlags{}, []string{"copilot"})
		opts, err := a.resolveHostOptions(ctx, []*tool.ToolDefinition{skill})
		require.NoError(t, err)
		assert.Nil(t, opts)
	})

	t.Run("BatchInstallsAllAvailableHosts", func(t *testing.T) {
		// A skill pulled in by --all / the interactive picker (not named
		// in args) installs through every available host instead of
		// aborting on ambiguity.
		a := newAction(nil, &toolInstallFlags{all: true}, []string{"copilot", "claude"})
		opts, err := a.resolveHostOptions(ctx, []*tool.ToolDefinition{skill})
		require.NoError(t, err)
		assert.Len(t, opts, 1)
	})

	t.Run("NonSkillNoFlagReturnsNil", func(t *testing.T) {
		a := newAction(nil, &toolInstallFlags{}, nil)
		opts, err := a.resolveHostOptions(ctx, []*tool.ToolDefinition{nonSkill})
		require.NoError(t, err)
		assert.Nil(t, opts)
	})
}

func TestResolveHostOptions_Upgrade(t *testing.T) {
	skill := &tool.ToolDefinition{
		Id:       "azure-skills",
		Name:     "Azure Skills",
		Category: tool.ToolCategorySkill,
	}
	nonSkill := &tool.ToolDefinition{
		Id:       "azure-mcp-server",
		Category: tool.ToolCategoryServer,
	}

	newAction := func(flags *toolUpgradeFlags, present []string) *toolUpgradeAction {
		installer := &cmdMockInstaller{
			availableSkillHosts: func(_ context.Context, td *tool.ToolDefinition) ([]string, []string) {
				return mockAvailableSkillHosts(td, present)
			},
		}
		manager := tool.NewManager(&cmdMockDetector{}, installer, nil)
		return newToolUpgradeAction(
			nil, flags, manager,
			mockinput.NewMockConsole(), &output.NoneFormatter{}, io.Discard,
		).(*toolUpgradeAction)
	}

	t.Run("HostWithoutSkillTool", func(t *testing.T) {
		a := newAction(&toolUpgradeFlags{hosts: []string{"copilot"}}, nil)
		_, err := a.resolveHostOptions([]*tool.ToolDefinition{nonSkill})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "only applies to skill tools")
	})

	t.Run("HostAllCannotMixWithSpecificHosts", func(t *testing.T) {
		a := newAction(&toolUpgradeFlags{hosts: []string{"all", "copilot"}}, []string{"copilot", "claude"})
		_, err := a.resolveHostOptions([]*tool.ToolDefinition{skill})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be combined with specific agents")
	})

	t.Run("ExplicitHostsReturnsOptions", func(t *testing.T) {
		a := newAction(&toolUpgradeFlags{hosts: []string{"claude"}}, []string{"copilot", "claude"})
		opts, err := a.resolveHostOptions([]*tool.ToolDefinition{skill})
		require.NoError(t, err)
		assert.Len(t, opts, 1)
	})

	t.Run("HostAllIteratesDetectedHosts", func(t *testing.T) {
		a := newAction(&toolUpgradeFlags{hosts: []string{"all"}}, []string{"copilot", "claude"})
		opts, err := a.resolveHostOptions([]*tool.ToolDefinition{skill})
		require.NoError(t, err)
		assert.Len(t, opts, 1)
	})

	t.Run("HostAllDefersEvenWhenNoneDetected", func(t *testing.T) {
		// --agent all resolves at install time, so it returns an option
		// even when no host is on PATH yet.
		a := newAction(&toolUpgradeFlags{hosts: []string{"all"}}, nil)
		opts, err := a.resolveHostOptions([]*tool.ToolDefinition{skill})
		require.NoError(t, err)
		assert.Len(t, opts, 1)
	})

	// Unlike install, upgrade with no --agent never errors on multiple
	// hosts: the installer upgrades the host the skill is installed
	// through, so no explicit choice is required.
	t.Run("MultipleHostsNoFlagReturnsNil", func(t *testing.T) {
		a := newAction(&toolUpgradeFlags{}, []string{"copilot", "claude"})
		opts, err := a.resolveHostOptions([]*tool.ToolDefinition{skill})
		require.NoError(t, err)
		assert.Nil(t, opts)
	})
}

func TestResolveHostOptions_Uninstall(t *testing.T) {
	skill := &tool.ToolDefinition{
		Id:       "azure-skills",
		Name:     "Azure Skills",
		Category: tool.ToolCategorySkill,
	}
	nonSkill := &tool.ToolDefinition{
		Id:       "azure-mcp-server",
		Category: tool.ToolCategoryServer,
	}

	newAction := func(flags *toolUninstallFlags) *toolUninstallAction {
		manager := tool.NewManager(&cmdMockDetector{}, &cmdMockInstaller{}, nil)
		return newToolUninstallAction(
			nil, flags, manager,
			mockinput.NewMockConsole(), &output.NoneFormatter{}, io.Discard,
		).(*toolUninstallAction)
	}

	t.Run("NoFlagReturnsNil", func(t *testing.T) {
		a := newAction(&toolUninstallFlags{})
		opts, err := a.resolveHostOptions([]*tool.ToolDefinition{skill})
		require.NoError(t, err)
		assert.Nil(t, opts, "no --agent removes from every installed host")
	})

	t.Run("HostWithoutSkillTool", func(t *testing.T) {
		a := newAction(&toolUninstallFlags{hosts: []string{"copilot"}})
		_, err := a.resolveHostOptions([]*tool.ToolDefinition{nonSkill})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "only applies to skill tools")
	})

	t.Run("ExplicitHostReturnsOptions", func(t *testing.T) {
		a := newAction(&toolUninstallFlags{hosts: []string{"claude"}})
		opts, err := a.resolveHostOptions([]*tool.ToolDefinition{skill})
		require.NoError(t, err)
		assert.Len(t, opts, 1)
	})

	t.Run("HostAllReturnsOptions", func(t *testing.T) {
		a := newAction(&toolUninstallFlags{hosts: []string{"all"}})
		opts, err := a.resolveHostOptions([]*tool.ToolDefinition{skill})
		require.NoError(t, err)
		assert.Len(t, opts, 1)
	})

	t.Run("HostAllCannotMixWithSpecificHosts", func(t *testing.T) {
		a := newAction(&toolUninstallFlags{hosts: []string{"all", "copilot"}})
		_, err := a.resolveHostOptions([]*tool.ToolDefinition{skill})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be combined with specific agents")
	})
}

// TestToolUninstallAction_SingleTool_DelegatesAndEmitsToolId verifies that an
// explicit single-tool uninstall delegates to the installer and emits tool.id
// (not tool.ids), honoring the mutual-exclusion contract.
func TestToolUninstallAction_SingleTool_DelegatesAndEmitsToolId(t *testing.T) {
	tracing.ResetUsageAttributesForTest()

	var uninstalledID string
	installer := &cmdMockInstaller{
		uninstall: func(
			_ context.Context, td *tool.ToolDefinition, _ ...tool.InstallOption,
		) (*tool.InstallResult, error) {
			uninstalledID = td.Id
			return &tool.InstallResult{Tool: td, Success: true, Strategy: "winget"}, nil
		},
	}
	manager := tool.NewManager(&cmdMockDetector{}, installer, nil)

	action := newToolUninstallAction(
		[]string{"az-cli"},
		&toolUninstallFlags{},
		manager,
		mockinput.NewMockConsole(),
		&output.NoneFormatter{},
		io.Discard,
	)

	_, err := action.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "az-cli", uninstalledID)

	gotID, ok := lookupToolStrUsage(string(fields.ToolIdKey.Key))
	require.True(t, ok, "tool.id must be emitted on single-tool uninstall")
	assert.Equal(t, "az-cli", gotID)

	_, ok = lookupToolStrUsage(string(fields.ToolIdsKey.Key))
	assert.False(t, ok, "tool.ids must NOT be emitted alongside tool.id (mutual exclusion)")
}

// TestToolUninstallAction_DryRun_DoesNotDelegate verifies that a dry-run
// uninstall emits tool.dry_run=true without invoking the installer.
func TestToolUninstallAction_DryRun_DoesNotDelegate(t *testing.T) {
	tracing.ResetUsageAttributesForTest()

	detector := &cmdMockDetector{
		detectTool: func(_ context.Context, td *tool.ToolDefinition) (*tool.ToolStatus, error) {
			return &tool.ToolStatus{Tool: td, Installed: true, InstalledVersion: "1.0.0"}, nil
		},
	}
	uninstallCalled := false
	installer := &cmdMockInstaller{
		uninstall: func(
			_ context.Context, td *tool.ToolDefinition, _ ...tool.InstallOption,
		) (*tool.InstallResult, error) {
			uninstallCalled = true
			return &tool.InstallResult{Tool: td, Success: true}, nil
		},
	}
	manager := tool.NewManager(detector, installer, nil)

	action := newToolUninstallAction(
		[]string{"az-cli"},
		&toolUninstallFlags{dryRun: true},
		manager,
		mockinput.NewMockConsole(),
		&output.NoneFormatter{},
		io.Discard,
	)

	_, err := action.Run(t.Context())
	require.NoError(t, err)
	assert.False(t, uninstallCalled, "dry-run must not invoke the installer")

	gotDry, ok := lookupToolBoolUsage(string(fields.ToolDryRunKey.Key))
	require.True(t, ok, "tool.dry_run must be emitted on dry-run uninstall")
	assert.True(t, gotDry)
}

// TestToolUpgradeAction_All_UpgradesInstalledTools verifies that
// `azd tool upgrade --all` upgrades every installed tool (and only those),
// without an interactive selection prompt.
func TestToolUpgradeAction_All_UpgradesInstalledTools(t *testing.T) {
	tracing.ResetUsageAttributesForTest()

	var installedIDs []string
	detector := &cmdMockDetector{
		detectAll: func(_ context.Context, tools []*tool.ToolDefinition) ([]*tool.ToolStatus, error) {
			statuses := make([]*tool.ToolStatus, len(tools))
			for i, td := range tools {
				// Mark the first two manifest tools installed; the rest not.
				installed := i < 2
				statuses[i] = &tool.ToolStatus{Tool: td, Installed: installed}
				if installed {
					statuses[i].InstalledVersion = "1.0.0"
					installedIDs = append(installedIDs, td.Id)
				}
			}
			return statuses, nil
		},
	}

	var upgradedIDs []string
	installer := &cmdMockInstaller{
		upgrade: func(_ context.Context, td *tool.ToolDefinition, _ ...tool.InstallOption) (*tool.InstallResult, error) {
			upgradedIDs = append(upgradedIDs, td.Id)
			return &tool.InstallResult{Tool: td, Success: true, InstalledVersion: "2.0.0"}, nil
		},
	}
	manager := tool.NewManager(detector, installer, nil)

	action := newToolUpgradeAction(
		nil,
		&toolUpgradeFlags{all: true},
		manager,
		mockinput.NewMockConsole(),
		&output.NoneFormatter{},
		io.Discard,
	)

	_, err := action.Run(t.Context())
	require.NoError(t, err)

	require.Len(t, installedIDs, 2)
	assert.ElementsMatch(t, installedIDs, upgradedIDs,
		"--all must upgrade exactly the installed tools")
}

// TestToolUpgradeAction_NoPrompt_UpgradesAllInstalled verifies that
// `azd tool upgrade --no-prompt` (even in a TTY) skips the interactive picker
// and upgrades every installed tool, so automation never blocks on input.
func TestToolUpgradeAction_NoPrompt_UpgradesAllInstalled(t *testing.T) {
	tracing.ResetUsageAttributesForTest()

	var installedIDs []string
	detector := &cmdMockDetector{
		detectAll: func(_ context.Context, tools []*tool.ToolDefinition) ([]*tool.ToolStatus, error) {
			statuses := make([]*tool.ToolStatus, len(tools))
			for i, td := range tools {
				installed := i < 2
				statuses[i] = &tool.ToolStatus{Tool: td, Installed: installed}
				if installed {
					statuses[i].InstalledVersion = "1.0.0"
					installedIDs = append(installedIDs, td.Id)
				}
			}
			return statuses, nil
		},
	}

	var upgradedIDs []string
	installer := &cmdMockInstaller{
		upgrade: func(_ context.Context, td *tool.ToolDefinition, _ ...tool.InstallOption) (*tool.InstallResult, error) {
			upgradedIDs = append(upgradedIDs, td.Id)
			return &tool.InstallResult{Tool: td, Success: true, InstalledVersion: "2.0.0"}, nil
		},
	}
	manager := tool.NewManager(detector, installer, nil)

	console := mockinput.NewMockConsole()
	console.SetTerminal(true)     // a TTY ...
	console.SetNoPromptMode(true) // ... but --no-prompt

	action := newToolUpgradeAction(
		nil,
		&toolUpgradeFlags{},
		manager,
		console,
		&output.NoneFormatter{},
		io.Discard,
	)

	_, err := action.Run(t.Context())
	require.NoError(t, err)

	require.Len(t, installedIDs, 2)
	assert.ElementsMatch(t, installedIDs, upgradedIDs,
		"--no-prompt must skip the picker and upgrade all installed tools")
}

// TestToolUpgradeAction_UnchangedVersion_ReportsUpToDate verifies that a
// non-skill tool whose detected version is identical before and after the
// upgrade reports "already up to date" — the version-comparison path that
// makes the message work for every tool, not just skills.
func TestToolUpgradeAction_UnchangedVersion_ReportsUpToDate(t *testing.T) {
	tracing.ResetUsageAttributesForTest()

	detector := &cmdMockDetector{
		detectTool: func(_ context.Context, td *tool.ToolDefinition) (*tool.ToolStatus, error) {
			return &tool.ToolStatus{Tool: td, Installed: true, InstalledVersion: "1.0.0"}, nil
		},
	}
	installer := &cmdMockInstaller{
		upgrade: func(_ context.Context, td *tool.ToolDefinition, _ ...tool.InstallOption) (*tool.InstallResult, error) {
			// Same version as detected before: nothing changed.
			return &tool.InstallResult{Tool: td, Success: true, InstalledVersion: "1.0.0"}, nil
		},
	}
	manager := tool.NewManager(detector, installer, nil)

	action := newToolUpgradeAction(
		[]string{"az-cli"},
		&toolUpgradeFlags{},
		manager,
		mockinput.NewMockConsole(),
		&output.NoneFormatter{},
		io.Discard,
	)

	result, err := action.Run(t.Context())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Message)
	assert.Equal(t, "Tool is already up to date (v1.0.0).", result.Message.Header)
}

// TestToolUpgradeAction_ChangedVersion_ReportsUpgraded verifies that when the
// detected version differs after the upgrade, the header reads "upgraded".
func TestToolUpgradeAction_ChangedVersion_ReportsUpgraded(t *testing.T) {
	tracing.ResetUsageAttributesForTest()

	detector := &cmdMockDetector{
		detectTool: func(_ context.Context, td *tool.ToolDefinition) (*tool.ToolStatus, error) {
			return &tool.ToolStatus{Tool: td, Installed: true, InstalledVersion: "1.0.0"}, nil
		},
	}
	installer := &cmdMockInstaller{
		upgrade: func(_ context.Context, td *tool.ToolDefinition, _ ...tool.InstallOption) (*tool.InstallResult, error) {
			return &tool.InstallResult{Tool: td, Success: true, InstalledVersion: "2.0.0"}, nil
		},
	}
	manager := tool.NewManager(detector, installer, nil)

	action := newToolUpgradeAction(
		[]string{"az-cli"},
		&toolUpgradeFlags{},
		manager,
		mockinput.NewMockConsole(),
		&output.NoneFormatter{},
		io.Discard,
	)

	result, err := action.Run(t.Context())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Message)
	assert.Equal(t, "Tool is upgraded to v2.0.0.", result.Message.Header)
}

// TestSkillHostDisplayName verifies an installed host's command identity is
// mapped to the agent's display name from the tool manifest (e.g. "copilot"
// -> "GitHub Copilot CLI"), falling back to the command when unmatched.
func TestSkillHostDisplayName(t *testing.T) {
	td := &tool.ToolDefinition{
		SkillHosts: []tool.SkillHost{
			{Host: "GitHub Copilot CLI", Command: "copilot"},
			{Host: "Claude Code CLI", Command: "claude"},
		},
	}
	assert.Equal(t, "GitHub Copilot CLI", skillHostDisplayName(td, "copilot"))
	assert.Equal(t, "Claude Code CLI", skillHostDisplayName(td, "claude"))
	// An unknown command falls back to itself.
	assert.Equal(t, "gemini", skillHostDisplayName(td, "gemini"))
}

// TestToolInstallAction_resolveUnavailableHostPrompt_CaseInsensitive verifies
// that an explicit --agent value is matched against the available hosts
// case-insensitively (like findSkillHost). "--agent Copilot" must match the
// available "copilot" command and NOT be reported unavailable or open a
// prompt.
func TestToolInstallAction_resolveUnavailableHostPrompt_CaseInsensitive(t *testing.T) {
	installer := &cmdMockInstaller{
		availableSkillHosts: func(_ context.Context, _ *tool.ToolDefinition) ([]string, []string) {
			return []string{"copilot"}, []string{"GitHub Copilot CLI"}
		},
	}
	manager := tool.NewManager(&cmdMockDetector{}, installer, nil)

	console := mockinput.NewMockConsole()
	console.SetTerminal(true) // interactive, so the unavailable-host path is reachable

	action := newToolInstallAction(
		nil,
		&toolInstallFlags{hosts: []string{"Copilot"}},
		manager, console, &output.NoneFormatter{}, io.Discard,
	).(*toolInstallAction)

	skill := &tool.ToolDefinition{Id: "azure-skills", Category: tool.ToolCategorySkill}
	opts, handled, err := action.resolveUnavailableHostPrompt(t.Context(), skill)
	require.NoError(t, err)
	assert.False(t, handled,
		"--agent Copilot must match available 'copilot' case-insensitively, not prompt")
	assert.Nil(t, opts)
}

// TestToolInstallAction_resolveToolIds_NoPromptDefaultsToRecommended verifies
// that --no-prompt (even in a TTY) skips the interactive picker and defaults to
// the recommended, not-yet-installed tools — its --all behavior, mirroring
// upgrade — so automation never blocks or errors on input.
func TestToolInstallAction_resolveToolIds_NoPromptDefaultsToRecommended(t *testing.T) {
	detector := &cmdMockDetector{
		detectAll: func(_ context.Context, _ []*tool.ToolDefinition) ([]*tool.ToolStatus, error) {
			return []*tool.ToolStatus{
				{Tool: &tool.ToolDefinition{Id: "rec", Priority: tool.ToolPriorityRecommended}},
				{Tool: &tool.ToolDefinition{Id: "opt", Priority: tool.ToolPriorityOptional}},
				{Tool: &tool.ToolDefinition{Id: "already", Priority: tool.ToolPriorityRecommended},
					Installed: true},
			}, nil
		},
	}
	manager := tool.NewManager(detector, &cmdMockInstaller{}, nil)

	console := mockinput.NewMockConsole()
	console.SetTerminal(true)     // a TTY ...
	console.SetNoPromptMode(true) // ... but --no-prompt

	action := newToolInstallAction(
		nil, &toolInstallFlags{}, manager, console, &output.NoneFormatter{}, io.Discard,
	).(*toolInstallAction)

	ids, err := action.resolveToolIds(t.Context())
	require.NoError(t, err)
	assert.Equal(t, []string{"rec"}, ids,
		"--no-prompt must skip the picker and default to recommended uninstalled tools")
}

// TestToolUninstallAction_resolveToolIds_NoPromptWithoutTarget_Errors verifies
// that uninstall — unlike install/upgrade, which only add — never treats "no
// target" as "all". With --no-prompt (or a non-interactive terminal) and
// neither tool IDs nor --all, it fails with guidance instead of silently
// removing every installed tool.
func TestToolUninstallAction_resolveToolIds_NoPromptWithoutTarget_Errors(t *testing.T) {
	detector := &cmdMockDetector{
		detectAll: func(_ context.Context, _ []*tool.ToolDefinition) ([]*tool.ToolStatus, error) {
			return []*tool.ToolStatus{
				{Tool: &tool.ToolDefinition{Id: "a"}, Installed: true},
				{Tool: &tool.ToolDefinition{Id: "b"}, Installed: true},
				{Tool: &tool.ToolDefinition{Id: "c"}},
			}, nil
		},
	}
	manager := tool.NewManager(detector, &cmdMockInstaller{}, nil)

	console := mockinput.NewMockConsole()
	console.SetTerminal(true)
	console.SetNoPromptMode(true)

	action := newToolUninstallAction(
		nil, &toolUninstallFlags{}, manager, console, &output.NoneFormatter{}, io.Discard,
	).(*toolUninstallAction)

	ids, err := action.resolveToolIds(t.Context())
	require.Error(t, err, "no target in non-interactive mode must not default to all")
	assert.Nil(t, ids)

	var ews *internal.ErrorWithSuggestion
	require.ErrorAs(t, err, &ews)
	assert.Contains(t, ews.Suggestion, "--all",
		"the guidance must tell the user to pass tool IDs or --all")
}

// TestToolUninstallAction_resolveToolIds_AllFlag_NoPrompt verifies the explicit
// destructive path still works: --all (even with --no-prompt) selects every
// installed tool.
func TestToolUninstallAction_resolveToolIds_AllFlag_NoPrompt(t *testing.T) {
	detector := &cmdMockDetector{
		detectAll: func(_ context.Context, _ []*tool.ToolDefinition) ([]*tool.ToolStatus, error) {
			return []*tool.ToolStatus{
				{Tool: &tool.ToolDefinition{Id: "a"}, Installed: true},
				{Tool: &tool.ToolDefinition{Id: "b"}, Installed: true},
				{Tool: &tool.ToolDefinition{Id: "c"}},
			}, nil
		},
	}
	manager := tool.NewManager(detector, &cmdMockInstaller{}, nil)

	console := mockinput.NewMockConsole()
	console.SetTerminal(true)
	console.SetNoPromptMode(true)

	action := newToolUninstallAction(
		nil, &toolUninstallFlags{all: true}, manager, console, &output.NoneFormatter{}, io.Discard,
	).(*toolUninstallAction)

	ids, err := action.resolveToolIds(t.Context())
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"a", "b"}, ids,
		"--all must select every installed tool")
}

// TestToolUpgradeAction_MultiHostSkill_UpgradedNotUpToDate reproduces the
// multi-host skill case: the aggregate InstalledVersion (first host) is
// unchanged, but the installer set AlreadyUpToDate=false because another host
// WAS upgraded. The header must read "upgraded", not "already up to date" —
// version comparison must not run for skills.
func TestToolUpgradeAction_MultiHostSkill_UpgradedNotUpToDate(t *testing.T) {
	tracing.ResetUsageAttributesForTest()

	detector := &cmdMockDetector{
		detectTool: func(_ context.Context, td *tool.ToolDefinition) (*tool.ToolStatus, error) {
			// First host is current before the upgrade.
			return &tool.ToolStatus{Tool: td, Installed: true, InstalledVersion: "1.1.87"}, nil
		},
	}
	installer := &cmdMockInstaller{
		upgrade: func(_ context.Context, td *tool.ToolDefinition, _ ...tool.InstallOption) (*tool.InstallResult, error) {
			// Aggregate version unchanged (first host current), but a different
			// host was upgraded, so AlreadyUpToDate is false.
			return &tool.InstallResult{
				Tool:             td,
				Success:          true,
				AlreadyUpToDate:  false,
				InstalledVersion: "1.1.87",
			}, nil
		},
	}
	manager := tool.NewManager(detector, installer, nil)

	action := newToolUpgradeAction(
		[]string{"azure-skills"}, // a manifest skill tool
		&toolUpgradeFlags{},
		manager,
		mockinput.NewMockConsole(),
		&output.NoneFormatter{},
		io.Discard,
	)

	result, err := action.Run(t.Context())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Message)
	assert.Equal(t, "Tool is upgraded to v1.1.87.", result.Message.Header,
		"a multi-host skill with an upgraded host must not read as already up to date")
}

// TestColorAgentPrefix verifies the NAME-column ColorFunc colors the "[agent]"
// label — including when the table wraps it across lines — while leaving plain
// tool names untouched. Expected values are built with the same formatter the
// implementation uses, so the assertions hold whether or not color is enabled.
func TestColorAgentPrefix(t *testing.T) {
	// Plain names (no brackets) are returned untouched.
	assert.Equal(t, "Azure CLI", colorAgentPrefix("Azure CLI"))
	assert.Equal(t, "", colorAgentPrefix(""))

	// A full label on one line: only the "[...]" label is formatted.
	assert.Equal(t,
		output.WithWarningFormat("[Claude Code CLI]")+" Azure Skills",
		colorAgentPrefix("[Claude Code CLI] Azure Skills"))

	// A label the table wrapped across lines: BOTH the opening line ("[..."
	// with no "]") and the tail line ("...]" with no "[") are formatted — the
	// bug was that neither was.
	assert.Equal(t,
		output.WithWarningFormat("[GitHub Copilot"),
		colorAgentPrefix("[GitHub Copilot"))
	assert.Equal(t,
		output.WithWarningFormat("CLI]")+" Azure Skills",
		colorAgentPrefix("CLI] Azure Skills"))
}

// TestToolNameColumn_PlainValueWrapsUnlikeAnsiValue guards the narrow-terminal
// wrapping fix: the NAME cell value must be plain text (with color applied via
// the ColorFunc), because the pretty table refuses to wrap a cell whose value
// embeds ANSI escapes. It renders the same long skill name two ways at a narrow
// width — plain (the fix) vs. ANSI embedded in the value (the old bug) — and
// asserts the plain value wraps onto more lines.
func TestToolNameColumn_PlainValueWrapsUnlikeAnsiValue(t *testing.T) {
	render := func(displayName string, colorFn func(string) string) string {
		var buf bytes.Buffer
		formatter := &output.PrettyTableFormatter{ConsoleWidthFn: func() int { return 70 }}
		err := formatter.Format(
			[]struct {
				DisplayName string `json:"displayName"`
			}{{DisplayName: displayName}},
			&buf,
			output.PrettyTableFormatterOptions{
				Columns: []output.PrettyColumn{
					{
						Column:    output.Column{Heading: "NAME", ValueTemplate: "{{.DisplayName}}"},
						Priority:  2,
						Wrappable: true,
						ColorFunc: colorFn,
					},
					{Column: output.Column{Heading: "STATUS", ValueTemplate: "Installed"}, Priority: 1},
					{Column: output.Column{Heading: "INSTALLED", ValueTemplate: "1.1.87"}, Priority: 1},
				},
			},
		)
		require.NoError(t, err)
		return buf.String()
	}

	const tail = " Azure Skills Extended Preview Bundle"
	// The fix: a plain NAME value, with color applied via the ColorFunc.
	plain := render("[GitHub Copilot CLI]"+tail, colorAgentPrefix)
	// The old bug: ANSI escapes embedded directly in the cell value. A literal
	// SGR sequence keeps this deterministic regardless of the environment's
	// color settings. The pretty table refuses to wrap such a value, so it
	// stays on one line.
	embedded := render("\x1b[33m[GitHub Copilot CLI]\x1b[0m"+tail, nil)

	assert.Greater(t, strings.Count(plain, "\n"), strings.Count(embedded, "\n"),
		"a plain NAME value must wrap at narrow width, unlike an ANSI-embedded value")
}

// TestToolListAction_JsonFormat_SkillPerHostRows locks the machine-readable
// contract for `azd tool list --output json`: a skill installed on multiple
// agents expands into one row per host, each carrying the original tool name,
// the command-valued agent, and that host's installed version.
func TestToolListAction_JsonFormat_SkillPerHostRows(t *testing.T) {
	t.Parallel()

	skill := &tool.ToolDefinition{
		Id:       "azure-skills",
		Name:     "Azure Skills",
		Category: tool.ToolCategorySkill,
		Priority: tool.ToolPriorityRecommended,
		SkillHosts: []tool.SkillHost{
			{Host: "GitHub Copilot CLI", Command: "copilot"},
			{Host: "Claude Code CLI", Command: "claude"},
		},
	}
	detector := &cmdMockDetector{
		detectAll: func(_ context.Context, _ []*tool.ToolDefinition) ([]*tool.ToolStatus, error) {
			return []*tool.ToolStatus{{
				Tool:      skill,
				Installed: true,
				SkillHosts: []tool.InstalledSkillHost{
					{Host: "copilot", Version: "1.0.0"},
					{Host: "claude", Version: "2.0.0"},
				},
			}}, nil
		},
	}
	manager := tool.NewManager(detector, &cmdMockInstaller{}, nil)

	var buf bytes.Buffer
	action := newToolListAction(manager, mockinput.NewMockConsole(), &output.JsonFormatter{}, &buf)
	_, err := action.Run(t.Context())
	require.NoError(t, err)

	var rows []toolListItem
	require.NoError(t, json.Unmarshal(buf.Bytes(), &rows))

	byAgent := make(map[string]toolListItem)
	for _, r := range rows {
		if r.Agent != "" {
			byAgent[r.Agent] = r
		}
	}
	require.Len(t, byAgent, 2, "a two-host skill must produce two agent rows")
	assert.Equal(t, "Azure Skills", byAgent["copilot"].Name)
	assert.Equal(t, "1.0.0", byAgent["copilot"].Version)
	assert.Equal(t, "Azure Skills", byAgent["claude"].Name)
	assert.Equal(t, "2.0.0", byAgent["claude"].Version)
}

// TestToolCheckAction_JsonFormat_SkillPerHostRows locks the machine-readable
// contract for `azd tool check --output json`: each skill host is a row with
// the command-valued agent, its installed version, the tool's latest version,
// and a per-host update flag (so a stale host reports an update while a current
// one does not).
func TestToolCheckAction_JsonFormat_SkillPerHostRows(t *testing.T) {
	tracing.ResetUsageAttributesForTest()

	detector := &cmdMockDetector{
		detectAll: func(_ context.Context, _ []*tool.ToolDefinition) ([]*tool.ToolStatus, error) {
			return []*tool.ToolStatus{{
				Tool:      &tool.ToolDefinition{Id: "azure-skills"},
				Installed: true,
				SkillHosts: []tool.InstalledSkillHost{
					{Host: "copilot", Version: "1.0.0"}, // behind latest
					{Host: "claude", Version: "2.0.0"},  // current
				},
			}}, nil
		},
	}

	cacheDir := t.TempDir()
	updateChecker := tool.NewUpdateChecker(
		&memUserConfigManager{},
		detector,
		func() (string, error) { return cacheDir, nil },
		map[string]tool.LatestVersionProvider{
			"azure-skills": stubVersionProvider{version: "2.0.0"},
		},
	)
	manager := tool.NewManager(detector, &cmdMockInstaller{}, updateChecker)

	var buf bytes.Buffer
	action := newToolCheckAction(manager, mockinput.NewMockConsole(), &output.JsonFormatter{}, &buf)
	_, err := action.Run(t.Context())
	require.NoError(t, err)

	var rows []toolCheckItem
	require.NoError(t, json.Unmarshal(buf.Bytes(), &rows))

	byAgent := make(map[string]toolCheckItem)
	for _, r := range rows {
		if r.Agent != "" {
			byAgent[r.Agent] = r
		}
	}
	require.Len(t, byAgent, 2, "a two-host skill must produce two agent rows")

	assert.Equal(t, "1.0.0", byAgent["copilot"].InstalledVersion)
	assert.Equal(t, "2.0.0", byAgent["copilot"].LatestVersion)
	assert.True(t, byAgent["copilot"].UpdateAvailable, "a stale host must report an update")

	assert.Equal(t, "2.0.0", byAgent["claude"].InstalledVersion)
	assert.Equal(t, "2.0.0", byAgent["claude"].LatestVersion)
	assert.False(t, byAgent["claude"].UpdateAvailable, "a current host must not report an update")
}

// memUserConfigManager is an in-memory config.UserConfigManager for tests that
// drive a real UpdateChecker without touching the user's config on disk.
type memUserConfigManager struct{ cfg config.Config }

func (m *memUserConfigManager) Load() (config.Config, error) {
	if m.cfg == nil {
		m.cfg = config.NewEmptyConfig()
	}
	return m.cfg, nil
}

func (m *memUserConfigManager) Save(c config.Config) error {
	m.cfg = c
	return nil
}

// stubVersionProvider is a tool.LatestVersionProvider that returns a fixed
// version, so update checks are deterministic and offline.
type stubVersionProvider struct{ version string }

func (s stubVersionProvider) GetLatestVersion(
	context.Context, *tool.ToolDefinition,
) (string, error) {
	return s.version, nil
}
