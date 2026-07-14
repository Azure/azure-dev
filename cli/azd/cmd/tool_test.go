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
		false,
	)
	results, err := outcome.Items, outcome.Err

	require.Error(t, err)
	require.Len(t, results, 1)
	assert.False(t, results[0].Success)
	require.NotEmpty(t, console.Output())
	assert.Contains(t, console.Output()[0], "Some tools could not be")
}

// TestRunToolOperation_Quiet_SuppressesFailureWarning verifies that in quiet
// (JSON) mode the failure warning is NOT emitted via console.Message — which
// would otherwise inject a standalone consoleMessage object ahead of the JSON
// result array and break single-document JSON output.
func TestRunToolOperation_Quiet_SuppressesFailureWarning(t *testing.T) {
	toolDef := &tool.ToolDefinition{
		Id:   "az-cli",
		Name: "Azure CLI",
	}
	console := mockinput.NewMockConsole()

	outcome := runToolOperation(
		t.Context(),
		[]*tool.ToolDefinition{toolDef},
		func(ctx context.Context, ids []string) ([]*tool.InstallResult, error) {
			return []*tool.InstallResult{{Tool: toolDef, Success: false}}, nil
		},
		"Installing",
		"install",
		console,
		true, // quiet (JSON)
	)

	require.Error(t, outcome.Err, "the operation still reports the failure via Err")
	for _, line := range console.Output() {
		assert.NotContains(t, line, "Some tools could not be",
			"quiet mode must not emit the failure warning to the console")
	}
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
		false,
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

func (m *cmdMockDetector) DetectSkillAgents(
	ctx context.Context, t *tool.ToolDefinition,
) ([]tool.InstalledSkillAgent, error) {
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
	availableSkillAgents func(ctx context.Context, t *tool.ToolDefinition) (commands []string, names []string)
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

func (m *cmdMockInstaller) AvailableSkillAgents(
	ctx context.Context,
	t *tool.ToolDefinition,
) (commands []string, names []string) {
	if m.availableSkillAgents != nil {
		return m.availableSkillAgents(ctx, t)
	}
	return nil, nil
}

// mockAvailableSkillAgents returns commands unchanged plus the display name for
// each, derived from the tool's SkillAgents (falling back to the command when
// no agent matches). It mirrors installer.AvailableSkillAgents so the mock
// yields the same (commands, names) shape from a plain list of commands.
func mockAvailableSkillAgents(td *tool.ToolDefinition, commands []string) ([]string, []string) {
	if len(commands) == 0 {
		return nil, nil
	}
	names := make([]string, len(commands))
	for i, c := range commands {
		names[i] = c
		for _, h := range td.SkillAgents {
			if h.Command == c {
				names[i] = h.DisplayName
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

// TestToolInstallAction_JsonFormat_NoBannerLeak verifies finding #1: in JSON
// mode the install command must not emit the MessageTitle banner (which the
// format-aware console would serialize as a `{"type":"consoleMessage"}` object
// ahead of the results array, breaking pure-JSON parsing). list/check already
// avoid this; install/upgrade/uninstall must match.
func TestToolInstallAction_JsonFormat_NoBannerLeak(t *testing.T) {
	tracing.ResetUsageAttributesForTest()

	installer := &cmdMockInstaller{
		install: func(_ context.Context, td *tool.ToolDefinition, _ ...tool.InstallOption) (*tool.InstallResult, error) {
			return &tool.InstallResult{Tool: td, Success: true, InstalledVersion: "2.0.0"}, nil
		},
	}
	manager := tool.NewManager(&cmdMockDetector{}, installer, nil)

	console := mockinput.NewMockConsole()
	var buf bytes.Buffer
	action := newToolInstallAction(
		[]string{"az-cli"},
		&toolInstallFlags{},
		manager,
		console,
		&output.JsonFormatter{},
		&buf,
	)

	_, err := action.Run(t.Context())
	require.NoError(t, err)

	for _, line := range console.Output() {
		assert.NotContains(t, line, "Install Azure development tools",
			"the title banner must be suppressed in JSON mode")
	}

	var items []toolInstallResultItem
	require.NoError(t, json.Unmarshal(buf.Bytes(), &items),
		"JSON output must be a single valid JSON document with no banner leak")
}

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
// resolveAgentOptions — --agent / --all-agents flag handling
// ---------------------------------------------------------------------------

func TestResolveAgentOptions(t *testing.T) {
	skill := &tool.ToolDefinition{
		Id:       "azure-skills",
		Name:     "Azure Skills",
		Category: tool.ToolCategorySkill,
		SkillAgents: []tool.SkillAgent{
			{DisplayName: "GitHub Copilot CLI", Command: "copilot"},
			{DisplayName: "Claude Code CLI", Command: "claude"},
		},
	}
	nonSkill := &tool.ToolDefinition{
		Id:       "azure-mcp-server",
		Category: tool.ToolCategoryServer,
	}

	newAction := func(args []string, flags *toolInstallFlags, present []string) *toolInstallAction {
		installer := &cmdMockInstaller{
			availableSkillAgents: func(_ context.Context, td *tool.ToolDefinition) ([]string, []string) {
				return mockAvailableSkillAgents(td, present)
			},
		}
		manager := tool.NewManager(&cmdMockDetector{}, installer, nil)
		return newToolInstallAction(
			args, flags, manager,
			mockinput.NewMockConsole(), &output.NoneFormatter{}, io.Discard,
		).(*toolInstallAction)
	}

	ctx := context.Background()

	t.Run("AgentWithoutSkillTool", func(t *testing.T) {
		a := newAction(nil, &toolInstallFlags{agents: []string{"copilot"}}, nil)
		_, err := a.resolveAgentOptions(ctx, []*tool.ToolDefinition{nonSkill})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "only applies to skill tools")
	})

	t.Run("AgentAllCannotMixWithSpecificAgents", func(t *testing.T) {
		a := newAction(nil, &toolInstallFlags{agents: []string{"all", "copilot"}}, []string{"copilot", "claude"})
		_, err := a.resolveAgentOptions(ctx, []*tool.ToolDefinition{skill})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be combined with specific agents")
	})

	t.Run("ExplicitAgentsReturnsOptions", func(t *testing.T) {
		a := newAction(nil, &toolInstallFlags{agents: []string{"claude"}}, []string{"copilot", "claude"})
		opts, err := a.resolveAgentOptions(ctx, []*tool.ToolDefinition{skill})
		require.NoError(t, err)
		assert.Len(t, opts, 1)
	})

	t.Run("AgentAllReturnsDeferredOption", func(t *testing.T) {
		a := newAction(nil, &toolInstallFlags{agents: []string{"all"}}, []string{"copilot", "claude"})
		opts, err := a.resolveAgentOptions(ctx, []*tool.ToolDefinition{skill})
		require.NoError(t, err)
		assert.Len(t, opts, 1)
	})

	t.Run("AgentAllDefersEvenWhenNoneDetected", func(t *testing.T) {
		// --agent all resolves at install time, so it returns an option
		// even when no agent is on PATH yet (the installer surfaces the
		// no-agent guidance later).
		a := newAction(nil, &toolInstallFlags{agents: []string{"all"}}, nil)
		opts, err := a.resolveAgentOptions(ctx, []*tool.ToolDefinition{skill})
		require.NoError(t, err)
		assert.Len(t, opts, 1)
	})

	t.Run("ExplicitlyNamedMultipleAgentsAsksToChoose", func(t *testing.T) {
		// `azd tool install azure-skills` (skill named in args) with
		// several agents present in a non-interactive terminal must surface
		// the guidance error asking the user to choose.
		a := newAction([]string{"azure-skills"}, &toolInstallFlags{}, []string{"copilot", "claude"})
		_, err := a.resolveAgentOptions(ctx, []*tool.ToolDefinition{skill})
		require.Error(t, err)
		var sug *internal.ErrorWithSuggestion
		require.ErrorAs(t, err, &sug)
		assert.Contains(t, sug.Message, "GitHub Copilot CLI, Claude Code CLI")
		assert.Contains(t, sug.Suggestion, "--agent all")
	})

	t.Run("ExplicitlyNamedMultipleAgentsInteractivePrompts", func(t *testing.T) {
		// In an interactive terminal the user is prompted to pick the
		// agents instead of erroring out. The picker shows friendly Agent
		// display names, and the user's selection maps back to the command.
		a := newAction([]string{"azure-skills"}, &toolInstallFlags{}, []string{"copilot", "claude"})
		mockConsole := a.console.(*mockinput.MockConsole)
		mockConsole.SetTerminal(true)
		var prompted []string
		mockConsole.WhenMultiSelect(func(options input.ConsoleOptions) bool {
			prompted = options.Options
			return true
		}).Respond([]string{"Claude Code CLI"})

		opts, err := a.resolveAgentOptions(ctx, []*tool.ToolDefinition{skill})
		require.NoError(t, err)
		assert.Len(t, opts, 1)
		// The picker offers friendly display names, not command identities.
		assert.Equal(t, []string{"GitHub Copilot CLI", "Claude Code CLI"}, prompted)
	})

	t.Run("ExplicitlyNamedMultipleAgentsPromptErrorPropagates", func(t *testing.T) {
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

		_, err := a.resolveAgentOptions(ctx, []*tool.ToolDefinition{skill})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "prompt boom")
	})

	t.Run("ExplicitlyNamedMultipleAgentsEmptySelectionFallsBackToError", func(t *testing.T) {
		// Selecting nothing in the picker falls back to the guidance
		// error telling the user to re-run with --agent.
		a := newAction([]string{"azure-skills"}, &toolInstallFlags{}, []string{"copilot", "claude"})
		mockConsole := a.console.(*mockinput.MockConsole)
		mockConsole.SetTerminal(true)
		mockConsole.WhenMultiSelect(func(options input.ConsoleOptions) bool {
			return true
		}).Respond([]string{})

		_, err := a.resolveAgentOptions(ctx, []*tool.ToolDefinition{skill})
		require.Error(t, err)
		var sug *internal.ErrorWithSuggestion
		require.ErrorAs(t, err, &sug)
		assert.Contains(t, sug.Suggestion, "--agent all")
	})

	t.Run("ExplicitUnavailableAgentInteractivePrompts", func(t *testing.T) {
		// `--agent gemini` names an agent that isn't supported/available.
		// In an interactive terminal we prompt over the agents that ARE
		// on PATH instead of hard-failing.
		a := newAction(
			[]string{"azure-skills"},
			&toolInstallFlags{agents: []string{"gemini"}},
			[]string{"copilot", "claude"},
		)
		mockConsole := a.console.(*mockinput.MockConsole)
		mockConsole.SetTerminal(true)
		var prompted []string
		mockConsole.WhenMultiSelect(func(options input.ConsoleOptions) bool {
			prompted = options.Options
			return true
		}).Respond([]string{"GitHub Copilot CLI"})

		opts, err := a.resolveAgentOptions(ctx, []*tool.ToolDefinition{skill})
		require.NoError(t, err)
		assert.Len(t, opts, 1)
		// The picker offered the available agents (friendly display names),
		// not the bogus request.
		assert.Equal(t, []string{"GitHub Copilot CLI", "Claude Code CLI"}, prompted)
	})

	t.Run("ExplicitUnavailableAgentNonInteractivePassesThrough", func(t *testing.T) {
		// Without a TTY we cannot prompt, so the request is passed
		// through unchanged for the installer to validate and reject.
		a := newAction(
			[]string{"azure-skills"},
			&toolInstallFlags{agents: []string{"gemini"}},
			[]string{"copilot", "claude"},
		)
		mockConsole := a.console.(*mockinput.MockConsole)
		prompted := false
		mockConsole.WhenMultiSelect(func(options input.ConsoleOptions) bool {
			prompted = true
			return true
		}).Respond([]string{"copilot"})

		opts, err := a.resolveAgentOptions(ctx, []*tool.ToolDefinition{skill})
		require.NoError(t, err)
		assert.Len(t, opts, 1)
		assert.False(t, prompted, "must not prompt without a terminal")
	})

	t.Run("ExplicitUnavailableAgentNoneOnPathDefersToGuidance", func(t *testing.T) {
		// `--agent gemini` with no supported agent on PATH: skip the picker
		// and target every available agent so the installer surfaces its
		// install-a-CLI-agent guidance.
		a := newAction(
			[]string{"azure-skills"},
			&toolInstallFlags{agents: []string{"gemini"}},
			nil,
		)
		mockConsole := a.console.(*mockinput.MockConsole)
		mockConsole.SetTerminal(true)
		prompted := false
		mockConsole.WhenMultiSelect(func(options input.ConsoleOptions) bool {
			prompted = true
			return true
		}).Respond([]string{"copilot"})

		opts, err := a.resolveAgentOptions(ctx, []*tool.ToolDefinition{skill})
		require.NoError(t, err)
		assert.Len(t, opts, 1)
		assert.False(t, prompted, "must not prompt when no agent is available")
	})

	t.Run("ExplicitUnavailableAgentMultiSelectErrorPropagates", func(t *testing.T) {
		// A failing picker during the unavailable-agent fallback surfaces
		// the error.
		a := newAction(
			[]string{"azure-skills"},
			&toolInstallFlags{agents: []string{"gemini"}},
			[]string{"copilot", "claude"},
		)
		mockConsole := a.console.(*mockinput.MockConsole)
		mockConsole.SetTerminal(true)
		mockConsole.WhenMultiSelect(func(options input.ConsoleOptions) bool {
			return true
		}).RespondFn(func(_ input.ConsoleOptions) (any, error) {
			return []string(nil), errors.New("picker boom")
		})

		_, err := a.resolveAgentOptions(ctx, []*tool.ToolDefinition{skill})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "picker boom")
	})

	t.Run("ExplicitUnavailableAgentEmptySelectionPassesThrough", func(t *testing.T) {
		// Selecting nothing leaves the original request intact so the
		// installer surfaces its validation error for the bad agent.
		a := newAction(
			[]string{"azure-skills"},
			&toolInstallFlags{agents: []string{"gemini"}},
			[]string{"copilot", "claude"},
		)
		mockConsole := a.console.(*mockinput.MockConsole)
		mockConsole.SetTerminal(true)
		mockConsole.WhenMultiSelect(func(options input.ConsoleOptions) bool {
			return true
		}).Respond([]string{})

		opts, err := a.resolveAgentOptions(ctx, []*tool.ToolDefinition{skill})
		require.NoError(t, err)
		assert.Len(t, opts, 1)
	})

	t.Run("ExplicitAvailableAgentSkipsPrompt", func(t *testing.T) {
		// A valid, available --agent is used directly without prompting,
		// even in an interactive terminal.
		a := newAction(
			[]string{"azure-skills"},
			&toolInstallFlags{agents: []string{"copilot"}},
			[]string{"copilot", "claude"},
		)
		mockConsole := a.console.(*mockinput.MockConsole)
		mockConsole.SetTerminal(true)
		prompted := false
		mockConsole.WhenMultiSelect(func(options input.ConsoleOptions) bool {
			prompted = true
			return true
		}).Respond([]string{"claude"})

		opts, err := a.resolveAgentOptions(ctx, []*tool.ToolDefinition{skill})
		require.NoError(t, err)
		assert.Len(t, opts, 1)
		assert.False(t, prompted, "available agent must not trigger a prompt")
	})

	t.Run("ExplicitlyNamedSingleAgentReturnsNil", func(t *testing.T) {
		a := newAction([]string{"azure-skills"}, &toolInstallFlags{}, []string{"copilot"})
		opts, err := a.resolveAgentOptions(ctx, []*tool.ToolDefinition{skill})
		require.NoError(t, err)
		assert.Nil(t, opts)
	})

	t.Run("BatchInstallsAllAvailableAgents", func(t *testing.T) {
		// A skill pulled in by --all / the interactive picker (not named
		// in args) installs through every available agent instead of
		// aborting on ambiguity.
		a := newAction(nil, &toolInstallFlags{all: true}, []string{"copilot", "claude"})
		opts, err := a.resolveAgentOptions(ctx, []*tool.ToolDefinition{skill})
		require.NoError(t, err)
		assert.Len(t, opts, 1)
	})

	t.Run("NonSkillNoFlagReturnsNil", func(t *testing.T) {
		a := newAction(nil, &toolInstallFlags{}, nil)
		opts, err := a.resolveAgentOptions(ctx, []*tool.ToolDefinition{nonSkill})
		require.NoError(t, err)
		assert.Nil(t, opts)
	})
}

func TestResolveAgentOptions_Upgrade(t *testing.T) {
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
			availableSkillAgents: func(_ context.Context, td *tool.ToolDefinition) ([]string, []string) {
				return mockAvailableSkillAgents(td, present)
			},
		}
		manager := tool.NewManager(&cmdMockDetector{}, installer, nil)
		return newToolUpgradeAction(
			nil, flags, manager,
			mockinput.NewMockConsole(), &output.NoneFormatter{}, io.Discard,
		).(*toolUpgradeAction)
	}

	t.Run("AgentWithoutSkillTool", func(t *testing.T) {
		a := newAction(&toolUpgradeFlags{agents: []string{"copilot"}}, nil)
		_, err := a.resolveAgentOptions([]*tool.ToolDefinition{nonSkill})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "only applies to skill tools")
	})

	t.Run("AgentAllCannotMixWithSpecificAgents", func(t *testing.T) {
		a := newAction(&toolUpgradeFlags{agents: []string{"all", "copilot"}}, []string{"copilot", "claude"})
		_, err := a.resolveAgentOptions([]*tool.ToolDefinition{skill})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be combined with specific agents")
	})

	t.Run("ExplicitAgentsReturnsOptions", func(t *testing.T) {
		a := newAction(&toolUpgradeFlags{agents: []string{"claude"}}, []string{"copilot", "claude"})
		opts, err := a.resolveAgentOptions([]*tool.ToolDefinition{skill})
		require.NoError(t, err)
		assert.Len(t, opts, 1)
	})

	t.Run("AgentAllIteratesDetectedAgents", func(t *testing.T) {
		a := newAction(&toolUpgradeFlags{agents: []string{"all"}}, []string{"copilot", "claude"})
		opts, err := a.resolveAgentOptions([]*tool.ToolDefinition{skill})
		require.NoError(t, err)
		assert.Len(t, opts, 1)
	})

	t.Run("AgentAllDefersEvenWhenNoneDetected", func(t *testing.T) {
		// --agent all resolves at install time, so it returns an option
		// even when no agent is on PATH yet.
		a := newAction(&toolUpgradeFlags{agents: []string{"all"}}, nil)
		opts, err := a.resolveAgentOptions([]*tool.ToolDefinition{skill})
		require.NoError(t, err)
		assert.Len(t, opts, 1)
	})

	// Unlike install, upgrade with no --agent never errors on multiple
	// agents: the installer upgrades the agent the skill is installed
	// through, so no explicit choice is required.
	t.Run("MultipleAgentsNoFlagReturnsNil", func(t *testing.T) {
		a := newAction(&toolUpgradeFlags{}, []string{"copilot", "claude"})
		opts, err := a.resolveAgentOptions([]*tool.ToolDefinition{skill})
		require.NoError(t, err)
		assert.Nil(t, opts)
	})
}

func TestResolveAgentOptions_Uninstall(t *testing.T) {
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
		opts, err := a.resolveAgentOptions([]*tool.ToolDefinition{skill})
		require.NoError(t, err)
		assert.Nil(t, opts, "no --agent removes from every installed agent")
	})

	t.Run("AgentWithoutSkillTool", func(t *testing.T) {
		a := newAction(&toolUninstallFlags{agents: []string{"copilot"}})
		_, err := a.resolveAgentOptions([]*tool.ToolDefinition{nonSkill})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "only applies to skill tools")
	})

	t.Run("ExplicitAgentReturnsOptions", func(t *testing.T) {
		a := newAction(&toolUninstallFlags{agents: []string{"claude"}})
		opts, err := a.resolveAgentOptions([]*tool.ToolDefinition{skill})
		require.NoError(t, err)
		assert.Len(t, opts, 1)
	})

	t.Run("AgentAllReturnsOptions", func(t *testing.T) {
		a := newAction(&toolUninstallFlags{agents: []string{"all"}})
		opts, err := a.resolveAgentOptions([]*tool.ToolDefinition{skill})
		require.NoError(t, err)
		assert.Len(t, opts, 1)
	})

	t.Run("AgentAllCannotMixWithSpecificAgents", func(t *testing.T) {
		a := newAction(&toolUninstallFlags{agents: []string{"all", "copilot"}})
		_, err := a.resolveAgentOptions([]*tool.ToolDefinition{skill})
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

// TestToolUpgradeAction_All_JsonFormat_EmitsCleanJson exercises the reviewer's
// exact trigger — `azd tool upgrade --all --output json` — and verifies the
// writer receives valid JSON. In JSON mode the detection spinner is
// bypassed (detectAllTools) so no control bytes can corrupt the stream.
func TestToolUpgradeAction_All_JsonFormat_EmitsCleanJson(t *testing.T) {
	tracing.ResetUsageAttributesForTest()

	detector := &cmdMockDetector{
		detectAll: func(_ context.Context, tools []*tool.ToolDefinition) ([]*tool.ToolStatus, error) {
			statuses := make([]*tool.ToolStatus, len(tools))
			for i, td := range tools {
				statuses[i] = &tool.ToolStatus{Tool: td, Installed: i < 1, InstalledVersion: "1.0.0"}
			}
			return statuses, nil
		},
	}
	installer := &cmdMockInstaller{
		upgrade: func(_ context.Context, td *tool.ToolDefinition, _ ...tool.InstallOption) (*tool.InstallResult, error) {
			return &tool.InstallResult{Tool: td, Success: true, InstalledVersion: "2.0.0"}, nil
		},
	}
	manager := tool.NewManager(detector, installer, nil)

	var buf bytes.Buffer
	action := newToolUpgradeAction(
		nil,
		&toolUpgradeFlags{all: true},
		manager,
		mockinput.NewMockConsole(),
		&output.JsonFormatter{},
		&buf,
	)

	_, err := action.Run(t.Context())
	require.NoError(t, err)

	var items []toolInstallResultItem
	require.NoError(t, json.Unmarshal(buf.Bytes(), &items),
		"upgrade --all --output json must emit valid JSON")
	require.NotEmpty(t, items, "at least one installed tool must be reported")
}

// TestToolUpgradeAction_All_JsonFormat_EmptyEmitsArray verifies that when there
// is nothing to upgrade, `azd tool upgrade --all --output json` still emits an
// empty result array ([]) rather than a consoleMessage object, so automation
// sees one stable shape.
func TestToolUpgradeAction_All_JsonFormat_EmptyEmitsArray(t *testing.T) {
	tracing.ResetUsageAttributesForTest()

	detector := &cmdMockDetector{
		detectAll: func(_ context.Context, tools []*tool.ToolDefinition) ([]*tool.ToolStatus, error) {
			// Nothing installed → nothing to upgrade.
			statuses := make([]*tool.ToolStatus, len(tools))
			for i, td := range tools {
				statuses[i] = &tool.ToolStatus{Tool: td, Installed: false}
			}
			return statuses, nil
		},
	}
	manager := tool.NewManager(detector, &cmdMockInstaller{}, nil)

	var buf bytes.Buffer
	action := newToolUpgradeAction(
		nil,
		&toolUpgradeFlags{all: true},
		manager,
		mockinput.NewMockConsole(),
		&output.JsonFormatter{},
		&buf,
	)

	_, err := action.Run(t.Context())
	require.NoError(t, err)

	assert.Equal(t, "[]", strings.TrimSpace(buf.String()),
		"empty upgrade must emit an empty JSON array, not a consoleMessage object")

	var items []toolInstallResultItem
	require.NoError(t, json.Unmarshal(buf.Bytes(), &items))
	assert.Empty(t, items)
}

// TestToolUpgradeAction_IDsWithAll_Errors verifies that `azd tool upgrade foo
// --all` is rejected rather than silently ignoring foo and upgrading everything.
func TestToolUpgradeAction_IDsWithAll_Errors(t *testing.T) {
	tracing.ResetUsageAttributesForTest()

	var upgraded bool
	installer := &cmdMockInstaller{
		upgrade: func(_ context.Context, td *tool.ToolDefinition, _ ...tool.InstallOption) (*tool.InstallResult, error) {
			upgraded = true
			return &tool.InstallResult{Tool: td, Success: true}, nil
		},
	}
	manager := tool.NewManager(&cmdMockDetector{}, installer, nil)

	action := newToolUpgradeAction(
		[]string{"foo"},
		&toolUpgradeFlags{all: true},
		manager,
		mockinput.NewMockConsole(),
		&output.NoneFormatter{},
		io.Discard,
	)

	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.ErrorIs(t, err, internal.ErrInvalidFlagCombination)
	assert.False(t, upgraded, "no tool must be upgraded when the flag combination is invalid")
}

// TestToolUpgradeAction_NoPrompt_WithoutTarget_Errors verifies that
// `azd tool upgrade` with --no-prompt (or a non-interactive terminal) and no
// tool IDs and no --all fails with guidance instead of implicitly upgrading
// every installed tool — consistent with install/uninstall and azd's
// --no-prompt contract.
func TestToolUpgradeAction_NoPrompt_WithoutTarget_Errors(t *testing.T) {
	tracing.ResetUsageAttributesForTest()

	detector := &cmdMockDetector{
		detectAll: func(_ context.Context, tools []*tool.ToolDefinition) ([]*tool.ToolStatus, error) {
			statuses := make([]*tool.ToolStatus, len(tools))
			for i, td := range tools {
				statuses[i] = &tool.ToolStatus{Tool: td, Installed: i < 2}
			}
			return statuses, nil
		},
	}
	installer := &cmdMockInstaller{
		upgrade: func(_ context.Context, td *tool.ToolDefinition, _ ...tool.InstallOption) (*tool.InstallResult, error) {
			t.Errorf("upgrade must not run without an explicit target; got %s", td.Id)
			return &tool.InstallResult{Tool: td, Success: true}, nil
		},
	}
	manager := tool.NewManager(detector, installer, nil)

	console := mockinput.NewMockConsole()
	console.SetTerminal(true)     // a TTY ...
	console.SetNoPromptMode(true) // ... but --no-prompt

	action := newToolUpgradeAction(
		nil, &toolUpgradeFlags{}, manager, console, &output.NoneFormatter{}, io.Discard,
	)

	_, err := action.Run(t.Context())
	require.Error(t, err)
	var ews *internal.ErrorWithSuggestion
	require.ErrorAs(t, err, &ews)
	assert.Contains(t, ews.Suggestion, "--all",
		"the guidance must tell the user to pass tool IDs or --all")
}

// TestToolUpgradeAction_JsonOnTTY_WithoutTarget_Errors verifies that
// `azd tool upgrade --output json` on an interactive terminal (no --no-prompt)
// requires an explicit target rather than opening the no-argument picker, whose
// output would corrupt the JSON result written to the same stdout.
func TestToolUpgradeAction_JsonOnTTY_WithoutTarget_Errors(t *testing.T) {
	tracing.ResetUsageAttributesForTest()

	detector := &cmdMockDetector{
		detectAll: func(_ context.Context, tools []*tool.ToolDefinition) ([]*tool.ToolStatus, error) {
			statuses := make([]*tool.ToolStatus, len(tools))
			for i, td := range tools {
				statuses[i] = &tool.ToolStatus{Tool: td, Installed: i < 2}
			}
			return statuses, nil
		},
	}
	installer := &cmdMockInstaller{
		upgrade: func(_ context.Context, td *tool.ToolDefinition, _ ...tool.InstallOption) (*tool.InstallResult, error) {
			t.Errorf("upgrade must not run without an explicit target; got %s", td.Id)
			return &tool.InstallResult{Tool: td, Success: true}, nil
		},
	}
	manager := tool.NewManager(detector, installer, nil)

	console := mockinput.NewMockConsole()
	console.SetTerminal(true) // interactive TTY, but NOT --no-prompt

	action := newToolUpgradeAction(
		nil, &toolUpgradeFlags{}, manager, console, &output.JsonFormatter{}, io.Discard,
	)

	_, err := action.Run(t.Context())
	require.Error(t, err, "JSON mode must require an explicit target, not open a picker")
	var ews *internal.ErrorWithSuggestion
	require.ErrorAs(t, err, &ews)
}
func TestToolUpgradeAction_AllFlag_NoPrompt(t *testing.T) {
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
	console.SetTerminal(true)
	console.SetNoPromptMode(true)

	action := newToolUpgradeAction(
		nil,
		&toolUpgradeFlags{all: true},
		manager,
		console,
		&output.NoneFormatter{},
		io.Discard,
	)

	_, err := action.Run(t.Context())
	require.NoError(t, err)

	require.Len(t, installedIDs, 2)
	assert.ElementsMatch(t, installedIDs, upgradedIDs,
		"--all must upgrade every installed tool without prompting")
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

// TestSkillAgentDisplayName verifies an installed agent's command identity is
// mapped to the agent's display name from the tool manifest (e.g. "copilot"
// -> "GitHub Copilot CLI"), falling back to the command when unmatched.
func TestSkillAgentDisplayName(t *testing.T) {
	td := &tool.ToolDefinition{
		SkillAgents: []tool.SkillAgent{
			{DisplayName: "GitHub Copilot CLI", Command: "copilot"},
			{DisplayName: "Claude Code CLI", Command: "claude"},
		},
	}
	assert.Equal(t, "GitHub Copilot CLI", skillAgentDisplayName(td, "copilot"))
	assert.Equal(t, "Claude Code CLI", skillAgentDisplayName(td, "claude"))
	// An unknown command falls back to itself.
	assert.Equal(t, "gemini", skillAgentDisplayName(td, "gemini"))
}

// TestToolInstallAction_resolveUnavailableAgentPrompt_CaseInsensitive verifies
// that an explicit --agent value is matched against the available agents
// case-insensitively (like findSkillAgent). "--agent Copilot" must match the
// available "copilot" command and NOT be reported unavailable or open a
// prompt.
func TestToolInstallAction_resolveUnavailableAgentPrompt_CaseInsensitive(t *testing.T) {
	installer := &cmdMockInstaller{
		availableSkillAgents: func(_ context.Context, _ *tool.ToolDefinition) ([]string, []string) {
			return []string{"copilot"}, []string{"GitHub Copilot CLI"}
		},
	}
	manager := tool.NewManager(&cmdMockDetector{}, installer, nil)

	console := mockinput.NewMockConsole()
	console.SetTerminal(true) // interactive, so the unavailable-agent path is reachable

	action := newToolInstallAction(
		nil,
		&toolInstallFlags{agents: []string{"Copilot"}},
		manager, console, &output.NoneFormatter{}, io.Discard,
	).(*toolInstallAction)

	skill := &tool.ToolDefinition{Id: "azure-skills", Category: tool.ToolCategorySkill}
	opts, handled, err := action.resolveUnavailableAgentPrompt(t.Context(), skill)
	require.NoError(t, err)
	assert.False(t, handled,
		"--agent Copilot must match available 'copilot' case-insensitively, not prompt")
	assert.Nil(t, opts)
}

// TestToolInstallAction_resolveToolIds_NoPromptWithoutTarget_Errors verifies
// that --no-prompt (or a non-interactive terminal) with no tool IDs and no
// --all fails with guidance instead of implicitly installing the recommended
// set — consistent with upgrade/uninstall and azd's --no-prompt contract.
func TestToolInstallAction_resolveToolIds_NoPromptWithoutTarget_Errors(t *testing.T) {
	detector := &cmdMockDetector{
		detectAll: func(_ context.Context, _ []*tool.ToolDefinition) ([]*tool.ToolStatus, error) {
			return []*tool.ToolStatus{
				{Tool: &tool.ToolDefinition{Id: "rec", Priority: tool.ToolPriorityRecommended}},
				{Tool: &tool.ToolDefinition{Id: "opt", Priority: tool.ToolPriorityOptional}},
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
	require.Error(t, err)
	assert.Nil(t, ids)
	var ews *internal.ErrorWithSuggestion
	require.ErrorAs(t, err, &ews)
	assert.Contains(t, ews.Suggestion, "--all",
		"the guidance must tell the user to pass tool IDs or --all")
}

// TestToolInstallAction_resolveToolIds_JsonOnTTY_Errors verifies that
// `--output json` on an interactive terminal (without --no-prompt) still
// requires an explicit target rather than opening a multi-select, whose prompt
// bytes would corrupt the JSON array written to the same stdout.
func TestToolInstallAction_resolveToolIds_JsonOnTTY_Errors(t *testing.T) {
	detector := &cmdMockDetector{
		detectAll: func(_ context.Context, _ []*tool.ToolDefinition) ([]*tool.ToolStatus, error) {
			return []*tool.ToolStatus{
				{Tool: &tool.ToolDefinition{Id: "rec", Priority: tool.ToolPriorityRecommended}},
			}, nil
		},
	}
	manager := tool.NewManager(detector, &cmdMockInstaller{}, nil)

	console := mockinput.NewMockConsole()
	console.SetTerminal(true) // interactive TTY, but NOT --no-prompt

	action := newToolInstallAction(
		nil, &toolInstallFlags{}, manager, console, &output.JsonFormatter{}, io.Discard,
	).(*toolInstallAction)

	ids, err := action.resolveToolIds(t.Context())
	require.Error(t, err, "JSON mode must require an explicit target, not open a picker")
	assert.Nil(t, ids)
	var ews *internal.ErrorWithSuggestion
	require.ErrorAs(t, err, &ews)
}

// not-yet-installed tools when --all is given, without prompting.
func TestToolInstallAction_resolveToolIds_AllFlag(t *testing.T) {
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

	action := newToolInstallAction(
		nil, &toolInstallFlags{all: true}, manager, mockinput.NewMockConsole(),
		&output.NoneFormatter{}, io.Discard,
	).(*toolInstallAction)

	ids, err := action.resolveToolIds(t.Context())
	require.NoError(t, err)
	assert.Equal(t, []string{"rec"}, ids,
		"--all installs recommended, not-yet-installed tools")
}

// TestToolInstallAction_resolveToolIds_IDsWithAll_Errors verifies that passing
// both tool IDs and --all is rejected rather than silently ignoring the IDs.
func TestToolInstallAction_resolveToolIds_IDsWithAll_Errors(t *testing.T) {
	manager := tool.NewManager(&cmdMockDetector{}, &cmdMockInstaller{}, nil)

	action := newToolInstallAction(
		[]string{"foo"}, &toolInstallFlags{all: true}, manager, mockinput.NewMockConsole(),
		&output.NoneFormatter{}, io.Discard,
	).(*toolInstallAction)

	ids, err := action.resolveToolIds(t.Context())
	require.Error(t, err)
	assert.Nil(t, ids)
	assert.ErrorIs(t, err, internal.ErrInvalidFlagCombination)
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

// TestToolUninstallAction_resolveToolIds_JsonOnTTY_Errors verifies that
// `--output json` on an interactive terminal still requires an explicit target
// rather than opening the uninstall picker (whose output would corrupt JSON).
func TestToolUninstallAction_resolveToolIds_JsonOnTTY_Errors(t *testing.T) {
	detector := &cmdMockDetector{
		detectAll: func(_ context.Context, _ []*tool.ToolDefinition) ([]*tool.ToolStatus, error) {
			return []*tool.ToolStatus{
				{Tool: &tool.ToolDefinition{Id: "a"}, Installed: true},
				{Tool: &tool.ToolDefinition{Id: "b"}, Installed: true},
			}, nil
		},
	}
	manager := tool.NewManager(detector, &cmdMockInstaller{}, nil)

	console := mockinput.NewMockConsole()
	console.SetTerminal(true) // interactive TTY, but NOT --no-prompt

	action := newToolUninstallAction(
		nil, &toolUninstallFlags{}, manager, console, &output.JsonFormatter{}, io.Discard,
	).(*toolUninstallAction)

	ids, err := action.resolveToolIds(t.Context())
	require.Error(t, err, "JSON mode must require an explicit target, not open a picker")
	assert.Nil(t, ids)
	var ews *internal.ErrorWithSuggestion
	require.ErrorAs(t, err, &ews)
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

// TestToolUninstallAction_resolveToolIds_IDsWithAll_Errors verifies that passing
// both tool IDs and --all is rejected rather than silently ignoring one of them.
func TestToolUninstallAction_resolveToolIds_IDsWithAll_Errors(t *testing.T) {
	manager := tool.NewManager(&cmdMockDetector{}, &cmdMockInstaller{}, nil)

	action := newToolUninstallAction(
		[]string{"a"}, &toolUninstallFlags{all: true}, manager, mockinput.NewMockConsole(),
		&output.NoneFormatter{}, io.Discard,
	).(*toolUninstallAction)

	ids, err := action.resolveToolIds(t.Context())
	require.Error(t, err)
	assert.Nil(t, ids)
	assert.ErrorIs(t, err, internal.ErrInvalidFlagCombination)
}

// TestToolUpgradeAction_MultiAgentSkill_UpgradedNotUpToDate reproduces the
// multi-agent skill case: the aggregate InstalledVersion (first agent) is
// unchanged, but the installer set AlreadyUpToDate=false because another agent
// WAS upgraded. The header must read "upgraded", not "already up to date" —
// version comparison must not run for skills.
func TestToolUpgradeAction_MultiAgentSkill_UpgradedNotUpToDate(t *testing.T) {
	tracing.ResetUsageAttributesForTest()

	detector := &cmdMockDetector{
		detectTool: func(_ context.Context, td *tool.ToolDefinition) (*tool.ToolStatus, error) {
			// First agent is current before the upgrade.
			return &tool.ToolStatus{Tool: td, Installed: true, InstalledVersion: "1.1.87"}, nil
		},
	}
	installer := &cmdMockInstaller{
		upgrade: func(_ context.Context, td *tool.ToolDefinition, _ ...tool.InstallOption) (*tool.InstallResult, error) {
			// Aggregate version unchanged (first agent current), but a different
			// agent was upgraded, so AlreadyUpToDate is false.
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
		"a multi-agent skill with an upgraded agent must not read as already up to date")
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

// TestToolListAction_JsonFormat_SkillPerAgentRows locks the machine-readable
// contract for `azd tool list --output json`: a skill installed on multiple
// agents expands into one row per agent, each carrying the original tool name,
// the command-valued agent, and that agent's installed version.
func TestToolListAction_JsonFormat_SkillPerAgentRows(t *testing.T) {
	t.Parallel()

	skill := &tool.ToolDefinition{
		Id:       "azure-skills",
		Name:     "Azure Skills",
		Category: tool.ToolCategorySkill,
		Priority: tool.ToolPriorityRecommended,
		SkillAgents: []tool.SkillAgent{
			{DisplayName: "GitHub Copilot CLI", Command: "copilot"},
			{DisplayName: "Claude Code CLI", Command: "claude"},
		},
	}
	detector := &cmdMockDetector{
		detectAll: func(_ context.Context, _ []*tool.ToolDefinition) ([]*tool.ToolStatus, error) {
			return []*tool.ToolStatus{{
				Tool:      skill,
				Installed: true,
				SkillAgents: []tool.InstalledSkillAgent{
					{Agent: "copilot", Version: "1.0.0"},
					{Agent: "claude", Version: "2.0.0"},
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
	require.Len(t, byAgent, 2, "a two-agent skill must produce two agent rows")
	assert.Equal(t, "Azure Skills", byAgent["copilot"].Name)
	assert.Equal(t, "1.0.0", byAgent["copilot"].Version)
	assert.Equal(t, "Azure Skills", byAgent["claude"].Name)
	assert.Equal(t, "2.0.0", byAgent["claude"].Version)
}

// TestToolCheckAction_JsonFormat_SkillPerAgentRows locks the machine-readable
// contract for `azd tool check --output json`: each skill agent is a row with
// the command-valued agent, its installed version, the tool's latest version,
// and a per-agent update flag (so a stale agent reports an update while a current
// one does not).
func TestToolCheckAction_JsonFormat_SkillPerAgentRows(t *testing.T) {
	tracing.ResetUsageAttributesForTest()

	detector := &cmdMockDetector{
		detectAll: func(_ context.Context, _ []*tool.ToolDefinition) ([]*tool.ToolStatus, error) {
			return []*tool.ToolStatus{{
				Tool:      &tool.ToolDefinition{Id: "azure-skills"},
				Installed: true,
				SkillAgents: []tool.InstalledSkillAgent{
					{Agent: "copilot", Version: "1.0.0"}, // behind latest
					{Agent: "claude", Version: "2.0.0"},  // current
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
	require.Len(t, byAgent, 2, "a two-agent skill must produce two agent rows")

	assert.Equal(t, "1.0.0", byAgent["copilot"].InstalledVersion)
	assert.Equal(t, "2.0.0", byAgent["copilot"].LatestVersion)
	assert.True(t, byAgent["copilot"].UpdateAvailable, "a stale agent must report an update")

	assert.Equal(t, "2.0.0", byAgent["claude"].InstalledVersion)
	assert.Equal(t, "2.0.0", byAgent["claude"].LatestVersion)
	assert.False(t, byAgent["claude"].UpdateAvailable, "a current agent must not report an update")
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
