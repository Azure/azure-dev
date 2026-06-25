// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package add

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

func TestSelectStorageDataTypes_ReturnsBlobs(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.multiSelectFn = func(opts input.ConsoleOptions) ([]string, error) {
		return []string{StorageDataTypeBlob}, nil
	}
	got, err := selectStorageDataTypes(t.Context(), c)
	require.NoError(t, err)
	assert.Equal(t, []string{StorageDataTypeBlob}, got)
}

func TestSelectStorageDataTypes_RetriesOnEmpty(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	calls := 0
	c.multiSelectFn = func(opts input.ConsoleOptions) ([]string, error) {
		calls++
		if calls == 1 {
			return []string{}, nil
		}
		return []string{StorageDataTypeBlob}, nil
	}
	got, err := selectStorageDataTypes(t.Context(), c)
	require.NoError(t, err)
	assert.Equal(t, []string{StorageDataTypeBlob}, got)
	assert.Equal(t, 2, calls)
}

func TestSelectStorageDataTypes_Error(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.multiSelectFn = func(opts input.ConsoleOptions) ([]string, error) {
		return nil, assertErr()
	}
	_, err := selectStorageDataTypes(t.Context(), c)
	require.Error(t, err)
}

func TestFillBlobDetails_SuccessAndRetry(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	responses := []string{"bad container!", "my-blobs"}
	i := 0
	c.WhenPrompt(func(input.ConsoleOptions) bool { return true }).
		RespondFn(func(opts input.ConsoleOptions) (any, error) {
			v := responses[i]
			i++
			return v, nil
		})
	props := &project.StorageProps{}
	err := fillBlobDetails(t.Context(), c, props)
	require.NoError(t, err)
	assert.Equal(t, []string{"my-blobs"}, props.Containers)
}

func TestFillBlobDetails_PromptError(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.WhenPrompt(func(input.ConsoleOptions) bool { return true }).
		RespondFn(func(input.ConsoleOptions) (any, error) { return "", assertErr() })
	props := &project.StorageProps{}
	err := fillBlobDetails(t.Context(), c, props)
	require.Error(t, err)
}

func TestFillStorageDetails_FullFlow(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.multiSelectFn = func(opts input.ConsoleOptions) ([]string, error) {
		return []string{StorageDataTypeBlob}, nil
	}
	c.WhenPrompt(func(input.ConsoleOptions) bool { return true }).Respond("my-container")
	r := &project.ResourceConfig{
		Type:  project.ResourceTypeStorage,
		Props: project.StorageProps{},
	}
	opts := PromptOptions{PrjConfig: &project.ProjectConfig{
		Resources: map[string]*project.ResourceConfig{},
	}}
	got, err := fillStorageDetails(t.Context(), r, c, opts)
	require.NoError(t, err)
	assert.Equal(t, "storage", got.Name)
	props, ok := got.Props.(project.StorageProps)
	require.True(t, ok)
	assert.Equal(t, []string{"my-container"}, props.Containers)
}
