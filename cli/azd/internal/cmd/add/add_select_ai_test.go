// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package add

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/input"
)

func TestSelectFromMap_MultipleOptions(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.WhenSelect(func(input.ConsoleOptions) bool { return true }).
		RespondFn(func(opts input.ConsoleOptions) (any, error) {
			// options are sorted, return index 0
			return 0, nil
		})
	m := map[string]int{"a": 1, "b": 2, "c": 3}
	key, val, err := selectFromMap(t.Context(), c, "q", m, nil)
	require.NoError(t, err)
	assert.Equal(t, "a", key)
	assert.Equal(t, 1, val)
}

func TestSelectFromMap_MultipleOptions_WithDefault(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.WhenSelect(func(input.ConsoleOptions) bool { return true }).
		RespondFn(func(opts input.ConsoleOptions) (any, error) {
			assert.Equal(t, "b", opts.DefaultValue)
			return 1, nil
		})
	m := map[string]int{"a": 1, "b": 2, "c": 3}
	def := "b"
	key, _, err := selectFromMap(t.Context(), c, "q", m, &def)
	require.NoError(t, err)
	assert.Equal(t, "b", key)
}

func TestSelectFromMap_SelectError(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.WhenSelect(func(input.ConsoleOptions) bool { return true }).
		RespondFn(func(input.ConsoleOptions) (any, error) { return 0, assertErr() })
	m := map[string]int{"a": 1, "b": 2}
	_, _, err := selectFromMap(t.Context(), c, "q", m, nil)
	require.Error(t, err)
}

func TestSelectFromSkus_Multiple(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.WhenSelect(func(input.ConsoleOptions) bool { return true }).Respond(1)
	skus := []ModelSku{
		{Name: "Standard"},
		{Name: "Premium"},
	}
	got, err := selectFromSkus(t.Context(), c, "q", skus)
	require.NoError(t, err)
	assert.Equal(t, "Premium", got.Name)
}

func TestSelectFromSkus_MultipleError(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.WhenSelect(func(input.ConsoleOptions) bool { return true }).
		RespondFn(func(input.ConsoleOptions) (any, error) { return 0, assertErr() })
	skus := []ModelSku{{Name: "Standard"}, {Name: "Premium"}}
	_, err := selectFromSkus(t.Context(), c, "q", skus)
	require.Error(t, err)
}
