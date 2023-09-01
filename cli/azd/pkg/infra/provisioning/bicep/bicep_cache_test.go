// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/stretchr/testify/require"
)

func TestBicepLocalCache(t *testing.T) {
	var lazyAzdContext lazy.Lazy[*azdcontext.AzdContext]
	lazyAzdContext.SetValue(azdcontext.NewAzdContextWithDirectory("foo"))

	var lazyEnv lazy.Lazy[*environment.Environment]
	lazyEnv.SetValue(environment.Ephemeral())

	var sample azure.ArmTemplate
	sampleTemplate, err := json.Marshal(sample)
	require.NoError(t, err)
	expectedResult := &BicepCache{
		Parameters: azure.ArmParameters{},
		Template:   sampleTemplate,
	}

	manager := &bicepCache{
		lazyAzdContext: &lazyAzdContext,
		lazyAzdEnv:     &lazyEnv,
		overrideReadFunc: func(context context.Context, arg any) ([]byte, error) {
			// should call file impl
			path, goodCast := arg.(string)
			require.True(t, goodCast)
			require.Equal(t, "foo/.azure/bicep.cache", path)
			return json.Marshal(expectedResult)
		},
	}

	ctx := context.Background()
	data := manager.Current(ctx)
	require.NotNil(t, data)
	require.Equal(t, data, expectedResult)
}

func TestBicepEqual(t *testing.T) {
	var lazyAzdContext lazy.Lazy[*azdcontext.AzdContext]
	lazyAzdContext.SetValue(azdcontext.NewAzdContextWithDirectory("foo"))

	var lazyEnv lazy.Lazy[*environment.Environment]
	lazyEnv.SetValue(environment.Ephemeral())

	var sample azure.ArmTemplate
	sampleTemplate, err := json.Marshal(sample)
	require.NoError(t, err)
	expectedResult := &BicepCache{
		Parameters: azure.ArmParameters{},
		Template:   sampleTemplate,
	}

	manager := &bicepCache{
		lazyAzdContext: &lazyAzdContext,
		lazyAzdEnv:     &lazyEnv,
		overrideReadFunc: func(context context.Context, arg any) ([]byte, error) {
			// should call file impl
			path, goodCast := arg.(string)
			require.True(t, goodCast)
			require.Equal(t, "foo/.azure/bicep.cache", path)
			return json.Marshal(expectedResult)
		},
	}

	ctx := context.Background()
	require.True(t, manager.Equal(ctx, expectedResult))
	require.False(t, manager.Equal(ctx, &BicepCache{
		Parameters: azure.ArmParameters{
			"foo": azure.ArmParameterValue{
				Value: "bar",
			},
		},
		Template: sampleTemplate,
	}))
}

func TestBicepCacheWriteLocal(t *testing.T) {
	var lazyAzdContext lazy.Lazy[*azdcontext.AzdContext]
	lazyAzdContext.SetValue(azdcontext.NewAzdContextWithDirectory("foo"))

	var lazyEnv lazy.Lazy[*environment.Environment]
	lazyEnv.SetValue(environment.Ephemeral())

	var sample azure.ArmTemplate
	sampleTemplate, err := json.Marshal(sample)
	require.NoError(t, err)
	expectedResult := &BicepCache{
		Parameters: azure.ArmParameters{},
		Template:   sampleTemplate,
	}

	manager := &bicepCache{
		lazyAzdContext: &lazyAzdContext,
		lazyAzdEnv:     &lazyEnv,
		overrideWriteFunc: func(context context.Context, arg any, cache []byte) error {
			path, goodCast := arg.(string)
			require.True(t, goodCast)
			require.Equal(t, "foo/.azure/bicep.cache", path)
			var reconstruct BicepCache
			err = json.Unmarshal(cache, &reconstruct)
			require.NoError(t, err)
			require.Equal(t, expectedResult, &reconstruct)
			return nil
		},
	}

	ctx := context.Background()
	err = manager.Cache(ctx, expectedResult)
	require.NoError(t, err)
}

func TestBicepRemoteAzure(t *testing.T) {
	var lazyAzdContext lazy.Lazy[*azdcontext.AzdContext]
	lazyAzdContext.SetValue(azdcontext.NewAzdContextWithDirectory("foo"))

	var lazyEnv lazy.Lazy[*environment.Environment]
	lazyEnv.SetValue(environment.EphemeralWithValues("foo", map[string]string{
		"AZURE_BICEP_CACHE_CONFIG": "azureBlob,container,connectionString",
	}))

	var sample azure.ArmTemplate
	sampleTemplate, err := json.Marshal(sample)
	require.NoError(t, err)
	expectedResult := &BicepCache{
		Parameters: azure.ArmParameters{},
		Template:   sampleTemplate,
	}

	manager := &bicepCache{
		lazyAzdContext: &lazyAzdContext,
		lazyAzdEnv:     &lazyEnv,
		overrideReadFunc: func(context context.Context, arg any) ([]byte, error) {
			// should call file impl
			azureStorageConfig, goodCast := arg.(*azBlobSource)
			require.True(t, goodCast)
			require.Equal(t, "container", azureStorageConfig.azContainerName)
			require.Equal(t, "connectionString", azureStorageConfig.azStorageConnectionString)
			return json.Marshal(expectedResult)
		},
	}

	ctx := context.Background()
	data := manager.Current(ctx)
	require.NotNil(t, data)
	require.Equal(t, data, expectedResult)
}

func TestBicepCacheWriteRemote(t *testing.T) {
	var lazyAzdContext lazy.Lazy[*azdcontext.AzdContext]
	lazyAzdContext.SetValue(azdcontext.NewAzdContextWithDirectory("foo"))

	var lazyEnv lazy.Lazy[*environment.Environment]
	lazyEnv.SetValue(environment.EphemeralWithValues("foo", map[string]string{
		"AZURE_BICEP_CACHE_CONFIG": "azureBlob,container,connectionString",
	}))

	var sample azure.ArmTemplate
	sampleTemplate, err := json.Marshal(sample)
	require.NoError(t, err)
	expectedResult := &BicepCache{
		Parameters: azure.ArmParameters{},
		Template:   sampleTemplate,
	}

	manager := &bicepCache{
		lazyAzdContext: &lazyAzdContext,
		lazyAzdEnv:     &lazyEnv,
		overrideWriteFunc: func(context context.Context, arg any, cache []byte) error {
			azureStorageConfig, goodCast := arg.(*azBlobSource)
			require.True(t, goodCast)
			require.Equal(t, "container", azureStorageConfig.azContainerName)
			require.Equal(t, "connectionString", azureStorageConfig.azStorageConnectionString)

			var reconstruct BicepCache
			err = json.Unmarshal(cache, &reconstruct)
			require.NoError(t, err)
			require.Equal(t, expectedResult, &reconstruct)
			return nil
		},
	}

	ctx := context.Background()
	err = manager.Cache(ctx, expectedResult)
	require.NoError(t, err)
}

func TestBicepCacheWriteRemoteFallbackLocal(t *testing.T) {
	var lazyAzdContext lazy.Lazy[*azdcontext.AzdContext]
	lazyAzdContext.SetValue(azdcontext.NewAzdContextWithDirectory("foo"))

	var lazyEnv lazy.Lazy[*environment.Environment]
	lazyEnv.SetValue(environment.EphemeralWithValues("foo", map[string]string{
		"AZURE_BICEP_CACHE_CONFIG": "azureBlob,incompleted",
	}))

	var sample azure.ArmTemplate
	sampleTemplate, err := json.Marshal(sample)
	require.NoError(t, err)
	expectedResult := &BicepCache{
		Parameters: azure.ArmParameters{},
		Template:   sampleTemplate,
	}

	manager := &bicepCache{
		lazyAzdContext: &lazyAzdContext,
		lazyAzdEnv:     &lazyEnv,
		overrideReadFunc: func(context context.Context, arg any) ([]byte, error) {
			// should call file impl
			path, goodCast := arg.(string)
			require.True(t, goodCast)
			require.Equal(t, "foo/.azure/bicep.cache", path)
			return json.Marshal(expectedResult)
		},
	}

	ctx := context.Background()
	err = manager.Cache(ctx, expectedResult)
	require.NoError(t, err)
}
