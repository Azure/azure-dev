// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provider

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"azure.resources/internal/resources"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

type testExecutor struct {
	azdext.ProvisioningProvider
	options       *azdext.ProvisioningOptions
	initializeErr error
	ensureEnvErr  error
	ensured       bool
	closeErr      error
}

func (e *testExecutor) Initialize(_ context.Context, _ string, options *azdext.ProvisioningOptions) error {
	e.options = options
	return e.initializeErr
}

func (e *testExecutor) Close() error { return e.closeErr }

func (e *testExecutor) EnsureEnv(context.Context) error {
	if e.options == nil {
		return errors.New("environment setup requires initialization")
	}
	e.ensured = true
	return e.ensureEnvErr
}

func testOptions(t *testing.T, name string) *azdext.ProvisioningOptions {
	t.Helper()
	config, err := structpb.NewStruct(map[string]any{"cache": map[string]any{"type": "db.redis"}})
	require.NoError(t, err)
	return &azdext.ProvisioningOptions{
		Provider: "resources", Name: name, Config: config,
		IgnoreDeploymentState: true, VirtualEnv: map[string]string{"OUTPUT": "value"},
	}
}

func TestInitializePreservesOptionsAndOwnsFiles(t *testing.T) {
	executor := &testExecutor{}
	provider := &Provider{executionProvider: executor, generator: &resources.Generator{}}
	options := testOptions(t, "data")
	original := proto.Clone(options)
	require.NoError(t, provider.Initialize(t.Context(), t.TempDir(), options))
	t.Cleanup(func() { require.NoError(t, provider.Close()) })
	require.True(t, proto.Equal(original, options), "caller options must not be modified")
	require.True(t, executor.ensured, "initialization must resolve and save the Azure scope")
	expected := proto.Clone(options).(*azdext.ProvisioningOptions)
	expected.Path, expected.Module = executor.options.Path, "main"
	require.True(t, proto.Equal(expected, executor.options))
	require.True(t, filepath.IsAbs(executor.options.Path))
	require.FileExists(t, filepath.Join(executor.options.Path, "main.bicep"))
	require.NoError(t, provider.Close())
	_, err := os.Stat(executor.options.Path)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.NoError(t, provider.Close())
}

func TestInitializeFailureRemovesGeneratedFiles(t *testing.T) {
	expected := errors.New("compilation failed")
	executor := &testExecutor{initializeErr: expected}
	provider := &Provider{executionProvider: executor, generator: &resources.Generator{}}
	err := provider.Initialize(t.Context(), t.TempDir(), testOptions(t, "data"))
	require.ErrorIs(t, err, expected)
	_, err = os.Stat(executor.options.Path)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.Empty(t, provider.tempDir)
}

func TestCloseFailureRetainsFilesForRetry(t *testing.T) {
	executor := &testExecutor{}
	provider := &Provider{executionProvider: executor, generator: &resources.Generator{}}
	require.NoError(t, provider.Initialize(t.Context(), t.TempDir(), testOptions(t, "data")))
	t.Cleanup(func() { executor.closeErr = nil; require.NoError(t, provider.Close()) })
	executor.closeErr = errors.New("executor still busy")
	require.ErrorIs(t, provider.Close(), executor.closeErr)
	require.DirExists(t, provider.tempDir)
	executor.closeErr = nil
	require.NoError(t, provider.Close())
	require.NoDirExists(t, executor.options.Path)
}

func TestEnvironmentSetupFailureRemovesGeneratedFiles(t *testing.T) {
	expected := errors.New("interactive input required")
	executor := &testExecutor{ensureEnvErr: expected}
	provider := &Provider{executionProvider: executor, generator: &resources.Generator{}}
	require.ErrorIs(t, provider.Initialize(t.Context(), t.TempDir(), testOptions(t, "data")), expected)
	require.True(t, executor.ensured)
	require.Empty(t, provider.tempDir)
	require.NoDirExists(t, executor.options.Path)
}

func TestSameProviderInstancesAreIndependent(t *testing.T) {
	first := &Provider{executionProvider: &testExecutor{}, generator: &resources.Generator{}}
	second := &Provider{executionProvider: &testExecutor{}, generator: &resources.Generator{}}
	require.NoError(t, first.Initialize(t.Context(), t.TempDir(), testOptions(t, "data")))
	t.Cleanup(func() { require.NoError(t, first.Close()) })
	require.NoError(t, second.Initialize(t.Context(), t.TempDir(), testOptions(t, "api")))
	t.Cleanup(func() { require.NoError(t, second.Close()) })
	require.NotEqual(t, first.tempDir, second.tempDir)
	require.NoError(t, first.Close())
	require.FileExists(t, filepath.Join(second.tempDir, "main.bicep"))
}

func TestInitializeRejectsInvalidModelQuotaBeforeHostCalls(t *testing.T) {
	for _, sku := range []resources.AiServicesModelSku{
		{Capacity: 10},
		{UsageName: "chat-quota"},
		{UsageName: "chat-quota", Capacity: -1},
	} {
		config := resources.Config{"foundry": {
			Type:   resources.AIProject,
			Models: []resources.AiServicesModel{{Name: "chat", Sku: sku}},
		}}
		raw, err := config.Raw()
		require.NoError(t, err)
		value, err := structpb.NewStruct(raw)
		require.NoError(t, err)
		p := New(nil)
		err = p.Initialize(t.Context(), t.TempDir(), &azdext.ProvisioningOptions{Config: value})
		require.ErrorContains(t, err, "requires a quota usage name and positive capacity")
		require.Empty(t, p.tempDir)
	}
}
