// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package config

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCloneValue(t *testing.T) {
	type namedMap map[string][]int
	type namedSlice []map[string]string
	type namedPointer *int
	type section struct {
		Values []string
		Count  *int
		When   time.Time
	}

	tests := []struct {
		name   string
		value  any
		mutate func(any)
	}{
		{
			name:  "nested maps and slices",
			value: map[string]any{"values": []any{map[string]any{"count": 1}}},
			mutate: func(value any) {
				value.(map[string]any)["values"].([]any)[0].(map[string]any)["count"] = 2
			},
		},
		{
			name:  "typed map",
			value: namedMap{"values": {1, 2}},
			mutate: func(value any) {
				value.(namedMap)["values"][0] = 3
			},
		},
		{
			name:  "typed slice",
			value: namedSlice{{"key": "original"}},
			mutate: func(value any) {
				value.(namedSlice)[0]["key"] = "changed"
			},
		},
		{
			name:  "array",
			value: [1][]string{{"original"}},
			mutate: func(value any) {
				value.([1][]string)[0][0] = "changed"
			},
		},
		{
			name:  "named pointer",
			value: namedPointer(new(1)),
			mutate: func(value any) {
				*value.(namedPointer) = 2
			},
		},
		{
			name:  "struct pointer",
			value: &section{Values: []string{"original"}, Count: new(1), When: time.Now()},
			mutate: func(value any) {
				value.(*section).Values[0] = "changed"
				*value.(*section).Count = 2
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expected, err := CloneValue(tt.value)
			require.NoError(t, err)
			cloned, err := CloneValue(tt.value)
			require.NoError(t, err)
			require.IsType(t, tt.value, cloned)
			require.Equal(t, tt.value, cloned)
			tt.mutate(cloned)
			require.Equal(t, expected, tt.value)
			require.NotEqual(t, tt.value, cloned)
		})
	}
}

func TestCloneValueNilAndScalars(t *testing.T) {
	for _, value := range []any{
		nil, map[string]any(nil), []string(nil), (*int)(nil),
		map[string]string{}, []int{},
		1, int64(2), uint32(3), float32(4.5), "string", true,
	} {
		cloned, err := CloneValue(value)
		require.NoError(t, err)
		require.Equal(t, value, cloned)
	}
	cloned, err := Clone(nil)
	require.NoError(t, err)
	require.True(t, cloned.IsEmpty())
}

func TestCloneRawConfig(t *testing.T) {
	type rawConfig struct{ Config }
	original := rawConfig{NewConfig(map[string]any{"nested": map[string]string{"key": "original"}})}
	cloned, err := Clone(original)
	require.NoError(t, err)
	require.IsType(t, &config{}, cloned)
	require.Equal(t, original.Raw(), cloned.Raw())
	require.IsType(t, map[string]string{}, cloned.Raw()["nested"])
	cloned.Raw()["nested"].(map[string]string)["key"] = "changed"
	require.Equal(t, map[string]string{"key": "original"}, original.Raw()["nested"])
}

func TestClonePreservesConfigAndVault(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", filepath.Join(dir, "user"))
	original := NewEmptyConfig()
	require.NoError(t, original.Set("nested", map[string][]int{"values": {1, 2}}))
	require.NoError(t, original.SetSecret("secrets.password", "unsaved-secret"))

	cloned, err := Clone(original)
	require.NoError(t, err)
	require.IsType(t, &config{}, cloned)
	require.Equal(t, original.Raw(), cloned.Raw())
	require.Equal(t, original.ResolvedRaw(), cloned.ResolvedRaw())
	require.IsType(t, map[string][]int{}, cloned.Raw()["nested"])
	cloned.Raw()["nested"].(map[string][]int)["values"][0] = 9
	require.Equal(t, map[string][]int{"values": {1, 2}}, original.Raw()["nested"])

	require.IsType(t, &config{}, original)
	originalConfig := original.(*config)
	clonedConfig := cloned.(*config)
	require.Equal(t, originalConfig.vaultId, clonedConfig.vaultId)
	require.NotSame(t, originalConfig.vault, clonedConfig.vault)
	require.NoError(t, cloned.SetSecret("secrets.new", "new-secret"))
	_, exists := original.Get("secrets.new")
	require.False(t, exists)
	require.NotEqual(t, originalConfig.vault.Raw(), clonedConfig.vault.Raw())

	manager := NewFileConfigManager(NewManager())
	path := filepath.Join(dir, "config.json")
	require.NoError(t, manager.Save(cloned, path))
	loaded, err := manager.Load(path)
	require.NoError(t, err)
	password, exists := loaded.GetString("secrets.password")
	require.True(t, exists)
	require.Equal(t, "unsaved-secret", password)
	password, exists = loaded.GetString("secrets.new")
	require.True(t, exists)
	require.Equal(t, "new-secret", password)
	require.Equal(t, cloned.Raw()["secrets"], loaded.Raw()["secrets"])

	loadedClone, err := Clone(loaded)
	require.NoError(t, err)
	require.NoError(t, loadedClone.SetSecret("secrets.password", "replacement"))
	password, exists = loaded.GetString("secrets.password")
	require.True(t, exists)
	require.Equal(t, "unsaved-secret", password)
	require.NoError(t, manager.Save(loadedClone, path))
	reloaded, err := manager.Load(path)
	require.NoError(t, err)
	password, exists = reloaded.GetString("secrets.password")
	require.True(t, exists)
	require.Equal(t, "replacement", password)
}

func TestCloneRejectsUnsupportedValues(t *testing.T) {
	type opaque struct{ values []string }
	type link struct{ Next *link }
	cyclicMap := map[string]any{}
	cyclicMap["self"] = cyclicMap
	cyclicSlice := make([]any, 1)
	cyclicSlice[0] = cyclicSlice
	cyclicPointer := &link{}
	cyclicPointer.Next = cyclicPointer

	tests := []struct {
		name  string
		value any
	}{
		{"channel", make(chan int)},
		{"function", func() {}},
		{"unexported mutable field", opaque{values: []string{"value"}}},
		{"mutable map key", map[*int]string{new(1): "value"}},
		{"cyclic map", cyclicMap},
		{"cyclic slice", cyclicSlice},
		{"cyclic pointer", cyclicPointer},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := CloneValue(tt.value)
			require.Error(t, err)
			cloned, err := Clone(NewConfig(map[string]any{"nested": []any{tt.value}}))
			require.Error(t, err)
			require.Nil(t, cloned)
		})
	}
}

func TestCloneValueShorterSubsliceWithSameStartAddressIsNotCycle(t *testing.T) {
	values := make([]any, 1)
	values[0] = values[:0]

	cloned, err := CloneValue(values)
	require.NoError(t, err)
	require.Len(t, cloned, 1)
	subSlice, ok := cloned[0].([]any)
	require.True(t, ok)
	require.NotNil(t, subSlice)
	require.Empty(t, subSlice)
}

func TestCloneRejectsVaultStateLoss(t *testing.T) {
	type rawConfig struct{ Config }
	original := rawConfig{NewEmptyConfig()}
	require.NoError(t, original.SetSecret("password", "secret"))
	cloned, err := Clone(original)
	require.ErrorContains(t, err, "preserve vault state")
	require.Nil(t, cloned)
}

func TestCloneRejectsUnsupportedVault(t *testing.T) {
	original := &config{
		data:  map[string]any{},
		vault: NewConfig(map[string]any{"unsupported": make(chan int)}),
	}
	cloned, err := Clone(original)
	require.ErrorContains(t, err, "cloning configuration vault")
	require.Nil(t, cloned)
}

func TestClonePreservesRepeatedReferences(t *testing.T) {
	shared := []string{"original"}
	value := map[string]any{"first": shared, "second": shared}
	cloned, err := CloneValue(value)
	require.NoError(t, err)
	require.Equal(t, value, cloned)
	require.IsType(t, []string{}, cloned["first"])
	cloned["first"].([]string)[0] = "changed"
	require.Equal(t, []string{"original"}, cloned["second"])
	require.Equal(t, []string{"original"}, shared)
}
