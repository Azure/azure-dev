// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStringPtr(t *testing.T) {
	t.Parallel()

	t.Run("initial_empty", func(t *testing.T) {
		t.Parallel()
		p := stringPtr{}
		assert.Equal(t, "", p.String())
		assert.Equal(t, "string", p.Type())
	})

	t.Run("set_value", func(t *testing.T) {
		t.Parallel()
		p := stringPtr{}
		err := p.Set("hello")
		require.NoError(t, err)
		assert.Equal(t, "hello", p.String())
	})

	t.Run("set_empty_string", func(t *testing.T) {
		t.Parallel()
		p := stringPtr{}
		err := p.Set("")
		require.NoError(t, err)
		assert.Equal(t, "", p.String())
		assert.NotNil(t, p.ptr)
	})
}

func TestBoolPtr(t *testing.T) {
	t.Parallel()

	t.Run("initial_false", func(t *testing.T) {
		t.Parallel()
		p := boolPtr{}
		assert.Equal(t, "false", p.String())
		assert.Equal(t, "", p.Type())
	})

	t.Run("set_true", func(t *testing.T) {
		t.Parallel()
		p := boolPtr{}
		err := p.Set("true")
		require.NoError(t, err)
		assert.Equal(t, "true", p.String())
	})
}

func TestUploadAction_Run_NilTelemetry(t *testing.T) {
	t.Parallel()

	action := newUploadAction(&internal.GlobalCommandOptions{})
	result, err := action.Run(t.Context())
	require.NoError(t, err)
	require.Nil(t, result)
}
