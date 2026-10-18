// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package envkeys

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type mapStore map[string]string

func (m mapStore) Get(_ context.Context, key string) (string, bool, error) {
	value, present := m[key]
	return value, present, nil
}

func (m mapStore) Set(_ context.Context, key, value string) error {
	m[key] = value
	return nil
}

type testEntry struct {
	Name string `json:"name"`
}

func testJSONKey() JSON[[]testEntry] {
	return JSON[[]testEntry]{
		name: "TEST_JSON",
		validate: func(entries []testEntry) error {
			for i, entry := range entries {
				if entry.Name == "" {
					return fmt.Errorf("entry %d needs name", i)
				}
			}
			return nil
		},
	}
}

func TestStrictJSON(t *testing.T) {
	key := testJSONKey()
	for _, raw := range []string{
		`[{}]`,
		`[{"name":"a","extra":1}]`,
		`[] []`,
		`[] true`,
		`[] trailing`,
		`[] }`,
		``,
		`{`,
	} {
		t.Run(raw, func(t *testing.T) {
			_, present, err := key.Get(t.Context(), mapStore{key.Name(): raw})
			require.True(t, present)
			require.ErrorContains(t, err, key.Name())
		})
	}
	for _, raw := range []string{`[]`, `[{"name":"a"}]`, "[] \n\t"} {
		t.Run("valid/"+raw, func(t *testing.T) {
			_, present, err := key.Get(t.Context(), mapStore{key.Name(): raw})
			require.NoError(t, err)
			require.True(t, present)
		})
	}
	value, present, err := key.Get(t.Context(), mapStore{})
	require.NoError(t, err)
	require.False(t, present)
	require.Nil(t, value)
}

func TestJSONSet(t *testing.T) {
	key := testJSONKey()
	store := mapStore{}
	entries := []testEntry{{Name: "a"}}
	require.NoError(t, key.Set(t.Context(), store, entries))
	value, present, err := key.Get(t.Context(), store)
	require.NoError(t, err)
	require.True(t, present)
	require.Equal(t, entries, value)
	require.Error(t, key.Set(t.Context(), store, []testEntry{{}}))
	require.Equal(t, `[{"name":"a"}]`, store[key.Name()])

	unencodable := JSON[chan int]{name: "TEST_CHANNEL"}
	require.ErrorContains(t, unencodable.Set(t.Context(), store, make(chan int)), "encode TEST_CHANNEL")
}

func TestEnum(t *testing.T) {
	for _, mode := range []AcrMode{AcrCreate, AcrReuseConnect, AcrAlreadyConnected, AcrNone} {
		t.Run(string(mode), func(t *testing.T) {
			store := mapStore{}
			require.NoError(t, FoundryAcrMode.Set(t.Context(), store, mode))
			value, present, err := FoundryAcrMode.Get(t.Context(), store)
			require.NoError(t, err)
			require.True(t, present)
			require.Equal(t, mode, value)
		})
	}
	for _, raw := range []string{"reuse", "invalid", "CREATE"} {
		t.Run(raw, func(t *testing.T) {
			store := mapStore{FoundryAcrMode.Name(): raw}
			_, present, err := FoundryAcrMode.Get(t.Context(), store)
			require.True(t, present)
			require.ErrorContains(t, err, "unsupported value")
			require.Error(t, FoundryAcrMode.Set(t.Context(), store, AcrMode(raw)))
			require.Equal(t, raw, store[FoundryAcrMode.Name()])
		})
	}
	for _, store := range []mapStore{{}, {FoundryAcrMode.Name(): ""}} {
		value, present, err := FoundryAcrMode.Get(t.Context(), store)
		require.NoError(t, err)
		require.Empty(t, value)
		_, expectedPresent := store[FoundryAcrMode.Name()]
		require.Equal(t, expectedPresent, present)
	}
	require.Error(t, FoundryAcrMode.Set(t.Context(), mapStore{}, ""))
}

func TestInputAndOutput(t *testing.T) {
	store := mapStore{ContainerRegistryResourceID.Name(): "registry-id"}
	value, present, err := ContainerRegistryResourceID.Get(t.Context(), store)
	require.NoError(t, err)
	require.True(t, present)
	require.Equal(t, "registry-id", value)
	require.NoError(t, ContainerRegistryEndpoint.Set(t.Context(), store, "registry.azurecr.io"))
	value, present, err = ContainerRegistryEndpoint.Get(t.Context(), store)
	require.NoError(t, err)
	require.True(t, present)
	require.Equal(t, "registry.azurecr.io", value)

	type setter interface {
		Set(context.Context, Store, string) error
	}
	for _, key := range []Input{ContainerRegistryResourceID, AgentSkipAcr} {
		_, writable := any(key).(setter)
		require.False(t, writable, "%s must be read-only", key.Name())
	}
}

type environmentClient struct {
	azdext.EnvironmentServiceClient
	get func(context.Context, *azdext.GetEnvRequest) (*azdext.KeyValueResponse, error)
	set func(context.Context, *azdext.SetEnvRequest) (*azdext.EmptyResponse, error)
}

func (c environmentClient) GetValue(
	ctx context.Context, req *azdext.GetEnvRequest, _ ...grpc.CallOption,
) (*azdext.KeyValueResponse, error) {
	return c.get(ctx, req)
}

func (c environmentClient) SetValue(
	ctx context.Context, req *azdext.SetEnvRequest, _ ...grpc.CallOption,
) (*azdext.EmptyResponse, error) {
	return c.set(ctx, req)
}

func TestEnvServiceStore(t *testing.T) {
	ctx := t.Context()
	failure := errors.New("RPC failure")
	for _, test := range []struct {
		name  string
		value string
		err   error
	}{
		{name: "value", value: "endpoint"},
		{name: "empty is present"},
		{name: "error", err: failure},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := EnvServiceStore{
				EnvName: "dev",
				Client: environmentClient{
					get: func(gotCtx context.Context, req *azdext.GetEnvRequest) (*azdext.KeyValueResponse, error) {
						require.Equal(t, ctx, gotCtx)
						require.Equal(t, "dev", req.EnvName)
						require.Equal(t, ContainerRegistryEndpoint.Name(), req.Key)
						return &azdext.KeyValueResponse{Value: test.value}, test.err
					},
					set: func(gotCtx context.Context, req *azdext.SetEnvRequest) (*azdext.EmptyResponse, error) {
						require.Equal(t, ctx, gotCtx)
						require.Equal(t, "dev", req.EnvName)
						require.Equal(t, ContainerRegistryEndpoint.Name(), req.Key)
						require.Equal(t, test.value, req.Value)
						return &azdext.EmptyResponse{}, test.err
					},
				},
			}
			value, present, err := ContainerRegistryEndpoint.Get(ctx, store)
			require.Equal(t, test.value, value)
			require.Equal(t, test.err == nil, present)
			require.ErrorIs(t, err, test.err)
			require.ErrorIs(t, ContainerRegistryEndpoint.Set(ctx, store, test.value), test.err)
		})
	}
}
