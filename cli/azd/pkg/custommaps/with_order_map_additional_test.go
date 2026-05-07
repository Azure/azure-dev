// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package custommaps

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWithOrder_OrderedValues(t *testing.T) {
	type Item struct {
		Name string `json:"name"`
	}
	m := &WithOrder[Item]{}
	data := `{
		"first":  {"name": "alpha"},
		"second": {"name": "bravo"},
		"third":  {"name": "charlie"}
	}`
	err := json.Unmarshal([]byte(data), m)
	require.NoError(t, err)

	values := m.OrderedValues()
	require.Len(t, values, 3)
	require.Equal(t, "alpha", values[0].Name)
	require.Equal(t, "bravo", values[1].Name)
	require.Equal(t, "charlie", values[2].Name)
}

func TestWithOrder_Get_ExistingKey(t *testing.T) {
	type Item struct {
		Value int `json:"value"`
	}
	m := &WithOrder[Item]{}
	data := `{"a": {"value": 1}, "b": {"value": 2}}`
	err := json.Unmarshal([]byte(data), m)
	require.NoError(t, err)

	val, ok := m.Get("a")
	require.True(t, ok)
	require.NotNil(t, val)
	require.Equal(t, 1, val.Value)

	val, ok = m.Get("b")
	require.True(t, ok)
	require.Equal(t, 2, val.Value)
}

func TestWithOrder_Get_MissingKey(t *testing.T) {
	m := &WithOrder[struct{}]{}
	err := json.Unmarshal([]byte(`{"x": {}}`), m)
	require.NoError(t, err)

	val, ok := m.Get("missing")
	require.False(t, ok)
	require.Nil(t, val)
}

func TestWithOrder_EmptyObject(t *testing.T) {
	m := &WithOrder[struct{}]{}
	err := json.Unmarshal([]byte(`{}`), m)
	require.NoError(t, err)

	require.Empty(t, m.OrderedKeys())
	require.Empty(t, m.OrderedValues())
}

func TestWithOrder_SingleEntry(t *testing.T) {
	type Item struct {
		ID string `json:"id"`
	}
	m := &WithOrder[Item]{}
	err := json.Unmarshal([]byte(`{"only": {"id": "one"}}`), m)
	require.NoError(t, err)

	keys := m.OrderedKeys()
	require.Len(t, keys, 1)
	require.Equal(t, "only", keys[0])

	values := m.OrderedValues()
	require.Len(t, values, 1)
	require.Equal(t, "one", values[0].ID)
}

func TestWithOrder_OrderPreserved(t *testing.T) {
	m := &WithOrder[struct{}]{}
	data := `{"z": {}, "a": {}, "m": {}, "b": {}}`
	err := json.Unmarshal([]byte(data), m)
	require.NoError(t, err)

	keys := m.OrderedKeys()
	require.Equal(t, []string{"z", "a", "m", "b"}, keys)
}

func TestWithOrder_InvalidJSON(t *testing.T) {
	m := &WithOrder[struct{}]{}
	err := json.Unmarshal([]byte(`not-json`), m)
	require.Error(t, err)
}

func TestWithOrder_ArrayInsteadOfObject(t *testing.T) {
	m := &WithOrder[struct{}]{}
	err := json.Unmarshal([]byte(`[1,2,3]`), m)
	require.Error(t, err)
}

func TestWithOrder_NestedValues(t *testing.T) {
	type Nested struct {
		Items []string `json:"items"`
	}
	m := &WithOrder[Nested]{}
	data := `{
		"group1": {"items": ["a", "b"]},
		"group2": {"items": ["c"]}
	}`
	err := json.Unmarshal([]byte(data), m)
	require.NoError(t, err)

	g1, ok := m.Get("group1")
	require.True(t, ok)
	require.Equal(t, []string{"a", "b"}, g1.Items)

	g2, ok := m.Get("group2")
	require.True(t, ok)
	require.Equal(t, []string{"c"}, g2.Items)
}

func TestWithOrder_ValuesMatchKeys(t *testing.T) {
	type Item struct {
		Name string `json:"name"`
	}
	m := &WithOrder[Item]{}
	data := `{
		"x": {"name": "X"},
		"y": {"name": "Y"},
		"z": {"name": "Z"}
	}`
	err := json.Unmarshal([]byte(data), m)
	require.NoError(t, err)

	keys := m.OrderedKeys()
	values := m.OrderedValues()
	require.Len(t, keys, len(values))

	for i, key := range keys {
		got, ok := m.Get(key)
		require.True(t, ok)
		require.Equal(t, got, values[i])
	}
}

func TestWithOrder_NullValues(t *testing.T) {
	type Item struct {
		Name string `json:"name"`
	}
	m := &WithOrder[Item]{}
	data := `{"a": null, "b": {"name": "B"}}`
	err := json.Unmarshal([]byte(data), m)
	require.NoError(t, err)

	keys := m.OrderedKeys()
	require.Equal(t, []string{"a", "b"}, keys)

	aVal, ok := m.Get("a")
	require.True(t, ok)
	require.Nil(t, aVal)

	bVal, ok := m.Get("b")
	require.True(t, ok)
	require.Equal(t, "B", bVal.Name)
}
