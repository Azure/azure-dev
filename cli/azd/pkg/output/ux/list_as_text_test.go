// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_ListAsText(t *testing.T) {
	t.Run("single", func(t *testing.T) {
		expected := "foo"
		result := ItemsAsText([]string{expected})
		require.Equal(t, expected, result)
	})

	t.Run("double", func(t *testing.T) {
		expected := "foo and bar"
		result := ItemsAsText([]string{"foo", "bar"})
		require.Equal(t, expected, result)
	})

	t.Run("triple", func(t *testing.T) {
		expected := "foo, bar and axe"
		result := ItemsAsText([]string{"foo", "bar", "axe"})
		require.Equal(t, expected, result)
	})

	t.Run("long", func(t *testing.T) {
		expected := "foo, bar, axe, x, y and z"
		result := ItemsAsText([]string{"foo", "bar", "axe", "x", "y", "z"})
		require.Equal(t, expected, result)
	})
}
