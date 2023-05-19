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
		result := AndListAsText([]string{expected})
		require.Equal(t, expected, result)
	})

	t.Run("double", func(t *testing.T) {
		expected := "foo and bar"
		result := AndListAsText([]string{"foo", "bar"})
		require.Equal(t, expected, result)
	})

	t.Run("triple", func(t *testing.T) {
		expected := "foo, bar and axe"
		result := AndListAsText([]string{"foo", "bar", "axe"})
		require.Equal(t, expected, result)
	})

	t.Run("long", func(t *testing.T) {
		expected := "foo, bar, axe, x, y and z"
		result := AndListAsText([]string{"foo", "bar", "axe", "x", "y", "z"})
		require.Equal(t, expected, result)
	})

	//only one test is added for OrListAsText as the function
	//calls the same listAsText like AndListAstTest
	t.Run("longOr", func(t *testing.T) {
		expected := "foo, bar, axe, x, y or z"
		result := OrListAsText([]string{"foo", "bar", "axe", "x", "y", "z"})
		require.Equal(t, expected, result)
	})
}
