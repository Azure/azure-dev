// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tracing

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
)

const testUsageKey = attribute.Key("agent.deploy.mode")

func testEligibleEvents() map[string]struct{} {
	return map[string]struct{}{
		"cmd.deploy": {},
		"cmd.up":     {},
	}
}

// closeValues closes the scope and returns the string-slice value for the
// telemetry key, or nil when the key was not recorded.
func closeValues(t *testing.T, scope CommandUsageScope) []string {
	t.Helper()

	attrs, err := CloseCommandUsageScope(scope)
	require.NoError(t, err)

	for _, attr := range attrs {
		if attr.Key == testUsageKey {
			return attr.Value.AsStringSlice()
		}
	}

	return nil
}

func TestCommandUsage_NoActiveScope(t *testing.T) {
	ResetCommandUsageForTest()
	t.Cleanup(ResetCommandUsageForTest)

	ok := TryAppendCommandUsageUnique(testEligibleEvents(), testUsageKey, "code")
	require.False(t, ok)
}

func TestCommandUsage_EligibleDeployScope(t *testing.T) {
	ResetCommandUsageForTest()
	t.Cleanup(ResetCommandUsageForTest)

	scope := BeginCommandUsageScope("cmd.deploy")
	require.True(t, TryAppendCommandUsageUnique(testEligibleEvents(), testUsageKey, "code"))
	require.Equal(t, []string{"code"}, closeValues(t, scope))
}

func TestCommandUsage_EligibleUpScope(t *testing.T) {
	ResetCommandUsageForTest()
	t.Cleanup(ResetCommandUsageForTest)

	scope := BeginCommandUsageScope("cmd.up")
	require.True(t, TryAppendCommandUsageUnique(testEligibleEvents(), testUsageKey, "container"))
	require.Equal(t, []string{"container"}, closeValues(t, scope))
}

func TestCommandUsage_DuplicateValueCollapses(t *testing.T) {
	ResetCommandUsageForTest()
	t.Cleanup(ResetCommandUsageForTest)

	scope := BeginCommandUsageScope("cmd.deploy")
	require.True(t, TryAppendCommandUsageUnique(testEligibleEvents(), testUsageKey, "code"))
	require.True(t, TryAppendCommandUsageUnique(testEligibleEvents(), testUsageKey, "code"))
	require.Equal(t, []string{"code"}, closeValues(t, scope))
}

func TestCommandUsage_TwoValuesSortedUnique(t *testing.T) {
	ResetCommandUsageForTest()
	t.Cleanup(ResetCommandUsageForTest)

	scope := BeginCommandUsageScope("cmd.up")
	require.True(t, TryAppendCommandUsageUnique(testEligibleEvents(), testUsageKey, "container"))
	require.True(t, TryAppendCommandUsageUnique(testEligibleEvents(), testUsageKey, "code"))
	// Sorted regardless of insertion order.
	require.Equal(t, []string{"code", "container"}, closeValues(t, scope))
}

func TestCommandUsage_ConcurrentValues(t *testing.T) {
	ResetCommandUsageForTest()
	t.Cleanup(ResetCommandUsageForTest)

	scope := BeginCommandUsageScope("cmd.up")

	modes := []string{"code", "container", "byo_image"}
	var wg sync.WaitGroup
	for i := range 50 {
		mode := modes[i%len(modes)]
		wg.Go(func() {
			TryAppendCommandUsageUnique(testEligibleEvents(), testUsageKey, mode)
		})
	}
	wg.Wait()

	require.ElementsMatch(t, modes, closeValues(t, scope))
}

func TestCommandUsage_IneligiblePackageScope(t *testing.T) {
	ResetCommandUsageForTest()
	t.Cleanup(ResetCommandUsageForTest)

	scope := BeginCommandUsageScope("cmd.package")
	require.False(t, TryAppendCommandUsageUnique(testEligibleEvents(), testUsageKey, "code"))
	require.Nil(t, closeValues(t, scope))
}

func TestCommandUsage_NestedUpThenPackageNoFallback(t *testing.T) {
	ResetCommandUsageForTest()
	t.Cleanup(ResetCommandUsageForTest)

	up := BeginCommandUsageScope("cmd.up")
	pkg := BeginCommandUsageScope("cmd.package")

	// A report while the ineligible child is on top must not fall back to up.
	require.False(t, TryAppendCommandUsageUnique(testEligibleEvents(), testUsageKey, "code"))

	require.Nil(t, closeValues(t, pkg))
	require.Nil(t, closeValues(t, up))
}

func TestCommandUsage_NestedUpThenDeployOwnsValue(t *testing.T) {
	ResetCommandUsageForTest()
	t.Cleanup(ResetCommandUsageForTest)

	up := BeginCommandUsageScope("cmd.up")
	deploy := BeginCommandUsageScope("cmd.deploy")

	require.True(t, TryAppendCommandUsageUnique(testEligibleEvents(), testUsageKey, "byo_image"))

	// The value lands only on the child deploy, never the parent up.
	require.Equal(t, []string{"byo_image"}, closeValues(t, deploy))
	require.Nil(t, closeValues(t, up))
}

func TestCommandUsage_ChildCloseRestoresParentAsCurrent(t *testing.T) {
	ResetCommandUsageForTest()
	t.Cleanup(ResetCommandUsageForTest)

	up := BeginCommandUsageScope("cmd.up")
	deploy := BeginCommandUsageScope("cmd.deploy")
	_, err := CloseCommandUsageScope(deploy)
	require.NoError(t, err)

	// After the child closes, up is current again and eligible.
	require.True(t, TryAppendCommandUsageUnique(testEligibleEvents(), testUsageKey, "code"))
	require.Equal(t, []string{"code"}, closeValues(t, up))
}

func TestCommandUsage_CloseAfterActionErrorRemovesScope(t *testing.T) {
	ResetCommandUsageForTest()
	t.Cleanup(ResetCommandUsageForTest)

	// Simulate a command action that errored: the deferred close still runs.
	scope := BeginCommandUsageScope("cmd.deploy")
	require.True(t, TryAppendCommandUsageUnique(testEligibleEvents(), testUsageKey, "code"))
	_, err := CloseCommandUsageScope(scope)
	require.NoError(t, err)

	// The next command sees no active scope.
	require.False(t, TryAppendCommandUsageUnique(testEligibleEvents(), testUsageKey, "code"))
}

func TestCommandUsage_RepeatedCloseErrors(t *testing.T) {
	ResetCommandUsageForTest()
	t.Cleanup(ResetCommandUsageForTest)

	scope := BeginCommandUsageScope("cmd.deploy")
	_, err := CloseCommandUsageScope(scope)
	require.NoError(t, err)

	_, err = CloseCommandUsageScope(scope)
	require.Error(t, err)
}

func TestCommandUsage_OutOfOrderCloseErrorsAndPreservesState(t *testing.T) {
	ResetCommandUsageForTest()
	t.Cleanup(ResetCommandUsageForTest)

	up := BeginCommandUsageScope("cmd.up")
	deploy := BeginCommandUsageScope("cmd.deploy")

	// Closing the parent while the child is on top is rejected.
	_, err := CloseCommandUsageScope(up)
	require.Error(t, err)

	// State is intact: the child is still current and closes cleanly, then up.
	_, err = CloseCommandUsageScope(deploy)
	require.NoError(t, err)
	_, err = CloseCommandUsageScope(up)
	require.NoError(t, err)
}

func TestCommandUsage_NextCommandHasNoLeakedValue(t *testing.T) {
	ResetCommandUsageForTest()
	t.Cleanup(ResetCommandUsageForTest)

	first := BeginCommandUsageScope("cmd.deploy")
	require.True(t, TryAppendCommandUsageUnique(testEligibleEvents(), testUsageKey, "code"))
	require.Equal(t, []string{"code"}, closeValues(t, first))

	// A subsequent unrelated command starts empty.
	second := BeginCommandUsageScope("cmd.deploy")
	require.Nil(t, closeValues(t, second))
}

func TestCommandUsage_CloseWithNoScopeErrors(t *testing.T) {
	ResetCommandUsageForTest()
	t.Cleanup(ResetCommandUsageForTest)

	_, err := CloseCommandUsageScope(CommandUsageScope{id: 999})
	require.Error(t, err)
}
