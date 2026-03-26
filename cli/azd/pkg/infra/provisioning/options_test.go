// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDestroyOptions(t *testing.T) {
	tests := []struct {
		name  string
		force bool
		purge bool
	}{
		{"both false", false, false},
		{"force only", true, false},
		{"purge only", false, true},
		{"both true", true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := NewDestroyOptions(tt.force, tt.purge)
			assert.Equal(t, tt.force, opts.Force())
			assert.Equal(t, tt.purge, opts.Purge())
		})
	}
}

func TestNewStateOptions(t *testing.T) {
	t.Run("stores hint", func(t *testing.T) {
		opts := NewStateOptions("my-hint")
		assert.Equal(t, "my-hint", opts.Hint())
	})

	t.Run("empty hint", func(t *testing.T) {
		opts := NewStateOptions("")
		assert.Equal(t, "", opts.Hint())
	})
}

func TestNewActionOptions(t *testing.T) {
	t.Run("interactive with nil formatter", func(t *testing.T) {
		opts := NewActionOptions(nil, true)
		// Formatter() should return a NoneFormatter
		assert.NotNil(t, opts.Formatter())
		assert.Equal(
			t, output.NoneFormat, opts.Formatter().Kind(),
		)
		// IsInteractive returns true when no format set
		assert.True(t, opts.IsInteractive())
	})

	t.Run("non-interactive", func(t *testing.T) {
		opts := NewActionOptions(nil, false)
		assert.False(t, opts.IsInteractive())
	})

	t.Run("interactive with json formatter", func(t *testing.T) {
		formatter := &output.JsonFormatter{}
		opts := NewActionOptions(formatter, true)
		assert.Equal(
			t, output.JsonFormat, opts.Formatter().Kind(),
		)
		// IsInteractive returns false when a format is set
		assert.False(t, opts.IsInteractive())
	})
}

func TestOptionsGetLayers(t *testing.T) {
	t.Run("no layers returns self", func(t *testing.T) {
		opts := &Options{
			Provider: Bicep,
			Path:     "infra",
		}
		layers := opts.GetLayers()
		require.Len(t, layers, 1)
		assert.Equal(t, Bicep, layers[0].Provider)
		assert.Equal(t, "infra", layers[0].Path)
	})

	t.Run("with layers returns layers", func(t *testing.T) {
		opts := &Options{
			Layers: []Options{
				{Name: "layer1", Path: "infra/1"},
				{Name: "layer2", Path: "infra/2"},
			},
		}
		layers := opts.GetLayers()
		require.Len(t, layers, 2)
		assert.Equal(t, "layer1", layers[0].Name)
		assert.Equal(t, "layer2", layers[1].Name)
	})
}

func TestOptionsGetLayer(t *testing.T) {
	t.Run("empty name with no layers returns self",
		func(t *testing.T) {
			opts := &Options{
				Provider: Bicep,
				Path:     "infra",
			}
			layer, err := opts.GetLayer("")
			require.NoError(t, err)
			assert.Equal(t, Bicep, layer.Provider)
		})

	t.Run("empty name with layers returns error",
		func(t *testing.T) {
			opts := &Options{
				Layers: []Options{
					{Name: "layer1", Path: "infra/1"},
				},
			}
			_, err := opts.GetLayer("")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "not found")
		})

	t.Run("named layer found", func(t *testing.T) {
		opts := &Options{
			Layers: []Options{
				{Name: "a", Path: "infra/a"},
				{Name: "b", Path: "infra/b"},
			},
		}
		layer, err := opts.GetLayer("b")
		require.NoError(t, err)
		assert.Equal(t, "b", layer.Name)
		assert.Equal(t, "infra/b", layer.Path)
	})

	t.Run("named layer not found", func(t *testing.T) {
		opts := &Options{
			Layers: []Options{
				{Name: "a", Path: "infra/a"},
			},
		}
		_, err := opts.GetLayer("missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing")
		assert.Contains(t, err.Error(), "available layers: a")
	})

	t.Run("no layers defined returns error for non-empty name",
		func(t *testing.T) {
			opts := &Options{}
			_, err := opts.GetLayer("something")
			require.Error(t, err)
			assert.Contains(
				t, err.Error(), "no layers defined",
			)
		})
}

func TestOptionsValidate(t *testing.T) {
	t.Run("empty options is valid", func(t *testing.T) {
		opts := &Options{}
		require.NoError(t, opts.Validate())
	})

	t.Run("layers without incompatible fields is valid",
		func(t *testing.T) {
			opts := &Options{
				Layers: []Options{
					{Name: "l1", Path: "infra/l1"},
				},
			}
			require.NoError(t, opts.Validate())
		})

	t.Run("layers with path set at top level is invalid",
		func(t *testing.T) {
			opts := &Options{
				Path: "infra",
				Layers: []Options{
					{Name: "l1", Path: "infra/l1"},
				},
			}
			err := opts.Validate()
			require.Error(t, err)
			assert.Contains(
				t, err.Error(),
				"properties on 'infra' cannot be declared",
			)
		})

	t.Run("layers with name set at top level is invalid",
		func(t *testing.T) {
			opts := &Options{
				Name: "top-name",
				Layers: []Options{
					{Name: "l1", Path: "infra/l1"},
				},
			}
			err := opts.Validate()
			require.Error(t, err)
		})

	t.Run("layers with module set at top level is invalid",
		func(t *testing.T) {
			opts := &Options{
				Module: "main",
				Layers: []Options{
					{Name: "l1", Path: "infra/l1"},
				},
			}
			err := opts.Validate()
			require.Error(t, err)
		})

	t.Run(
		"layers with DeploymentStacks set at top level is invalid",
		func(t *testing.T) {
			opts := &Options{
				DeploymentStacks: map[string]any{"k": "v"},
				Layers: []Options{
					{Name: "l1", Path: "infra/l1"},
				},
			}
			err := opts.Validate()
			require.Error(t, err)
		})

	t.Run("layer without name is invalid",
		func(t *testing.T) {
			opts := &Options{
				Layers: []Options{
					{Path: "infra/l1"},
				},
			}
			err := opts.Validate()
			require.Error(t, err)
			assert.Contains(
				t, err.Error(), "name must be specified",
			)
		})

	t.Run("layer without path is invalid",
		func(t *testing.T) {
			opts := &Options{
				Layers: []Options{
					{Name: "l1"},
				},
			}
			err := opts.Validate()
			require.Error(t, err)
			assert.Contains(
				t, err.Error(), "path must be specified",
			)
		})

	t.Run("multiple valid layers", func(t *testing.T) {
		opts := &Options{
			Layers: []Options{
				{Name: "l1", Path: "infra/l1"},
				{Name: "l2", Path: "infra/l2"},
			},
		}
		require.NoError(t, opts.Validate())
	})
}
