// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

// newCacheTestCli creates a Cli with a mocked command runner and the bicep cache enabled.
// The returned mock context can be used to set up command expectations.
func newCacheTestCli(t *testing.T) (*Cli, *mocks.MockContext) {
	t.Helper()

	mockContext := mocks.NewMockContext(t.Context())

	cli := newCliWithTransporter(
		mockContext.Console, mockContext.CommandRunner, mockContext.HttpClient,
	)

	// Pre-initialise the install check so tests never attempt to find or download a real
	// bicep binary. After this call, ensureInstalledOnce is a no-op that returns nil.
	_ = cli.installInit.Do(func() error {
		cli.path = "bicep"
		return nil
	})

	return cli, mockContext
}

func TestBuildCache_HitReturnsCachedResult(t *testing.T) {
	// Not parallel — tests share a single Cli instance with internal state.

	dir := t.TempDir()
	bicepFile := filepath.Join(dir, "main.bicep")
	require.NoError(t, os.WriteFile(bicepFile, []byte("param location string"), 0600))

	cli, mockContext := newCacheTestCli(t)

	// Set up the command runner to return a compiled template.
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return len(args.Args) >= 3 && args.Args[0] == "build" && args.Args[2] == "--stdout"
	}).Respond(exec.NewRunResult(0, `{"$schema":"arm-template"}`, ""))

	ctx := *mockContext.Context

	// First call: cache miss — should invoke bicep build.
	result1, err := cli.Build(ctx, bicepFile)
	require.NoError(t, err)
	require.Equal(t, `{"$schema":"arm-template"}`, result1.Compiled)

	// Second call with same file content: cache hit — should NOT invoke bicep build again.
	// Override the command runner to fail, proving the cache is used.
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return len(args.Args) >= 2 && args.Args[0] == "build"
	}).Respond(exec.NewRunResult(1, "", "should not be called"))

	result2, err := cli.Build(ctx, bicepFile)
	require.NoError(t, err)
	require.Equal(t, result1.Compiled, result2.Compiled)
	require.Equal(t, result1.LintErr, result2.LintErr)
}

func TestBuildCache_MissTriggersBuild(t *testing.T) {
	// Not parallel — tests share a single Cli instance with internal state.

	dir := t.TempDir()
	bicepFile := filepath.Join(dir, "main.bicep")
	require.NoError(t, os.WriteFile(bicepFile, []byte("param name string"), 0600))

	cli, mockContext := newCacheTestCli(t)

	buildCalled := false
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return len(args.Args) >= 2 && args.Args[0] == "build"
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		buildCalled = true
		return exec.NewRunResult(0, `{"compiled":true}`, ""), nil
	})

	result, err := cli.Build(*mockContext.Context, bicepFile)
	require.NoError(t, err)
	require.True(t, buildCalled, "expected bicep build to be invoked on cache miss")
	require.Equal(t, `{"compiled":true}`, result.Compiled)
}

func TestBuildCache_DifferentContentGetsDifferentKey(t *testing.T) {
	// Not parallel — tests share a single Cli instance with internal state.

	dir := t.TempDir()
	bicepFileA := filepath.Join(dir, "a.bicep")
	bicepFileB := filepath.Join(dir, "b.bicep")
	require.NoError(t, os.WriteFile(bicepFileA, []byte("param a string"), 0600))
	require.NoError(t, os.WriteFile(bicepFileB, []byte("param b string"), 0600))

	cli, _ := newCacheTestCli(t)

	keyA, err := cli.buildCacheKey(bicepFileA)
	require.NoError(t, err)

	keyB, err := cli.buildCacheKey(bicepFileB)
	require.NoError(t, err)

	require.NotEqual(t, keyA, keyB, "different file content must produce different cache keys")
}

func TestBuildCache_InvalidationOnFileChange(t *testing.T) {
	// Not parallel — tests share a single Cli instance with internal state.

	dir := t.TempDir()
	bicepFile := filepath.Join(dir, "main.bicep")
	require.NoError(t, os.WriteFile(bicepFile, []byte("param v1 string"), 0600))

	cli, mockContext := newCacheTestCli(t)

	callCount := 0
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return len(args.Args) >= 2 && args.Args[0] == "build"
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		callCount++
		return exec.NewRunResult(0, fmt.Sprintf(`{"version":%d}`, callCount), ""), nil
	})

	ctx := *mockContext.Context

	// First build with v1 content.
	result1, err := cli.Build(ctx, bicepFile)
	require.NoError(t, err)
	require.Equal(t, 1, callCount)

	// Mutate the file content.
	require.NoError(t, os.WriteFile(bicepFile, []byte("param v2 string"), 0600))

	// Second build with v2 content — cache key changes, so bicep build must be invoked again.
	result2, err := cli.Build(ctx, bicepFile)
	require.NoError(t, err)
	require.Equal(t, 2, callCount, "expected second bicep build invocation after file change")
	require.NotEqual(t, result1.Compiled, result2.Compiled)
}

func TestBuildCacheKey_WithModuleImports(t *testing.T) {
	cli, _ := newCacheTestCli(t)

	t.Run("cache key changes when module file changes", func(t *testing.T) {
		dir := t.TempDir()
		modDir := filepath.Join(dir, "modules")
		require.NoError(t, os.MkdirAll(modDir, 0700))

		moduleFile := filepath.Join(modDir, "network.bicep")
		require.NoError(t, os.WriteFile(moduleFile, []byte("param vnetName string"), 0600))

		mainBicep := filepath.Join(dir, "main.bicep")
		require.NoError(t, os.WriteFile(
			mainBicep,
			[]byte("module net './modules/network.bicep' = {\n  name: 'net'\n}"),
			0600))

		key1, err := cli.buildCacheKey(mainBicep)
		require.NoError(t, err)
		require.NotEmpty(t, key1)

		// Change module content — cache key must change.
		require.NoError(t, os.WriteFile(moduleFile, []byte("param vnetName string\nparam subnetCidr string"), 0600))

		key2, err := cli.buildCacheKey(mainBicep)
		require.NoError(t, err)
		require.NotEqual(t, key1, key2, "cache key must change when module content changes")
	})

	t.Run("cache miss when module file missing", func(t *testing.T) {
		dir := t.TempDir()
		mainBicep := filepath.Join(dir, "main.bicep")
		require.NoError(t, os.WriteFile(mainBicep, []byte("module db './db.bicep' = {\n  name: 'db'\n}"), 0600))

		key, err := cli.buildCacheKey(mainBicep)
		require.Error(t, err, "unresolvable module should cause an error (cache miss)")
		require.Empty(t, key)
	})

	t.Run("registry modules ignored", func(t *testing.T) {
		dir := t.TempDir()
		mainBicep := filepath.Join(dir, "main.bicep")
		content := "module registry 'br:mcr.microsoft.com/bicep/avm:1.0' = {\n  name: 'reg'\n}\n" +
			"module tspec 'ts:sub/rg/spec:v1' = {\n  name: 'ts'\n}"
		require.NoError(t, os.WriteFile(mainBicep, []byte(content), 0600))

		key, err := cli.buildCacheKey(mainBicep)
		require.NoError(t, err, "registry modules should be skipped, not cause errors")
		require.NotEmpty(t, key)
	})

	t.Run("recursive module resolution", func(t *testing.T) {
		dir := t.TempDir()
		modDir := filepath.Join(dir, "modules")
		require.NoError(t, os.MkdirAll(modDir, 0700))

		// main.bicep → modules/app.bicep → modules/db.bicep
		dbFile := filepath.Join(modDir, "db.bicep")
		require.NoError(t, os.WriteFile(dbFile, []byte("param dbName string"), 0600))

		appFile := filepath.Join(modDir, "app.bicep")
		require.NoError(t, os.WriteFile(appFile, []byte("module database './db.bicep' = {\n  name: 'database'\n}"), 0600))

		mainBicep := filepath.Join(dir, "main.bicep")
		require.NoError(t, os.WriteFile(mainBicep, []byte("module app './modules/app.bicep' = {\n  name: 'app'\n}"), 0600))

		key1, err := cli.buildCacheKey(mainBicep)
		require.NoError(t, err)

		// Change the deeply nested module — key must change.
		require.NoError(t, os.WriteFile(dbFile, []byte("param dbName string\nparam sku string"), 0600))

		key2, err := cli.buildCacheKey(mainBicep)
		require.NoError(t, err)
		require.NotEqual(t, key1, key2, "cache key must change when deeply nested module changes")
	})

	t.Run("unchanged modules produce same key", func(t *testing.T) {
		dir := t.TempDir()
		modDir := filepath.Join(dir, "modules")
		require.NoError(t, os.MkdirAll(modDir, 0700))

		require.NoError(t, os.WriteFile(filepath.Join(modDir, "net.bicep"), []byte("param x string"), 0600))

		mainBicep := filepath.Join(dir, "main.bicep")
		require.NoError(t, os.WriteFile(mainBicep, []byte("module net './modules/net.bicep' = {\n  name: 'net'\n}"), 0600))

		key1, err := cli.buildCacheKey(mainBicep)
		require.NoError(t, err)

		key2, err := cli.buildCacheKey(mainBicep)
		require.NoError(t, err)
		require.Equal(t, key1, key2, "identical content must produce identical key")
	})
}

func TestBuildCacheKey_IncludesBicepparamContent(t *testing.T) {
	// Not parallel — tests share a single Cli instance with internal state.

	dir := t.TempDir()
	bicepFile := filepath.Join(dir, "main.bicep")
	paramFile := filepath.Join(dir, "main.bicepparam")

	require.NoError(t, os.WriteFile(bicepFile, []byte("param loc string"), 0600))

	cli, _ := newCacheTestCli(t)

	// Key without .bicepparam
	keyWithout, err := cli.buildCacheKey(bicepFile)
	require.NoError(t, err)

	// Add a .bicepparam file
	require.NoError(t, os.WriteFile(paramFile, []byte("using './main.bicep'\nparam loc = 'eastus'"), 0600))

	// Key with .bicepparam — must differ
	keyWith, err := cli.buildCacheKey(bicepFile)
	require.NoError(t, err)

	require.NotEqual(t, keyWithout, keyWith, "adding a .bicepparam file must change the cache key")
}

func TestBuildCacheKey_CommentedModuleIgnored(t *testing.T) {
	cli, _ := newCacheTestCli(t)
	dir := t.TempDir()

	mainBicep := filepath.Join(dir, "main.bicep")
	content := "// module old './nonexistent.bicep' = {\n//   name: 'old'\n// }\n" +
		"param location string\n"
	require.NoError(t, os.WriteFile(mainBicep, []byte(content), 0600))

	key, err := cli.buildCacheKey(mainBicep)
	require.NoError(t, err, "commented-out module should not cause cache miss")
	require.NotEmpty(t, key)
}
