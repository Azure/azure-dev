// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package figspec

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRenderOption(t *testing.T) {
	t.Run("basic_option", func(t *testing.T) {
		opt := &Option{
			Name:        []string{"--verbose", "-v"},
			Description: "Enable verbose output",
		}
		result := renderOption(opt, 2)
		require.Contains(t, result, "'--verbose'")
		require.Contains(t, result, "'-v'")
		require.Contains(t, result, "description:")
	})

	t.Run("all_bool_fields", func(t *testing.T) {
		opt := &Option{
			Name:         []string{"--force"},
			Description:  "Force it",
			IsPersistent: true,
			IsRepeatable: true,
			IsRequired:   true,
			IsDangerous:  true,
			Hidden:       true,
		}
		result := renderOption(opt, 1)
		require.Contains(t, result, "isPersistent: true")
		require.Contains(t, result, "isRepeatable: true")
		require.Contains(t, result, "isRequired: true")
		require.Contains(t, result, "isDangerous: true")
		require.Contains(t, result, "hidden: true")
	})

	t.Run("with_args", func(t *testing.T) {
		opt := &Option{
			Name:        []string{"--output"},
			Description: "Output format",
			Args:        []Arg{{Name: "format"}},
		}
		result := renderOption(opt, 2)
		require.Contains(t, result, "args: [")
		require.Contains(t, result, "name: 'format'")
	})
}

func TestRenderSubcommand(t *testing.T) {
	t.Run("nil_returns_empty", func(t *testing.T) {
		require.Equal(t, "", renderSubcommand(nil, 2))
	})

	t.Run("basic", func(t *testing.T) {
		sub := &Subcommand{
			Name:        []string{"init"},
			Description: "Initialize a new app",
		}
		result := renderSubcommand(sub, 1)
		require.Contains(t, result, "'init'")
		require.Contains(t, result, "Initialize a new app")
	})

	t.Run("hidden", func(t *testing.T) {
		sub := &Subcommand{
			Name:        []string{"debug"},
			Description: "Debug",
			Hidden:      true,
		}
		result := renderSubcommand(sub, 1)
		require.Contains(t, result, "hidden: true")
	})

	t.Run("with_nested_subcommands", func(t *testing.T) {
		sub := &Subcommand{
			Name:        []string{"env"},
			Description: "Manage environments",
			Subcommands: []Subcommand{
				{Name: []string{"list"}, Description: "List envs"},
			},
		}
		result := renderSubcommand(sub, 1)
		require.Contains(t, result, "subcommands: [")
		require.Contains(t, result, "'list'")
	})

	t.Run("with_options", func(t *testing.T) {
		sub := &Subcommand{
			Name:        []string{"deploy"},
			Description: "Deploy",
			Options: []Option{
				{Name: []string{"--all"}, Description: "Deploy all"},
			},
		}
		result := renderSubcommand(sub, 1)
		require.Contains(t, result, "options: [")
		require.Contains(t, result, "'--all'")
	})

	t.Run("single_arg_inline", func(t *testing.T) {
		sub := &Subcommand{
			Name:        []string{"get"},
			Description: "Get value",
			Args:        []Arg{{Name: "key"}},
		}
		result := renderSubcommand(sub, 1)
		require.Contains(t, result, "args:")
		require.Contains(t, result, "'key'")
		// Single arg should NOT have array brackets around the value
		require.NotContains(t, result, "args: [")
	})

	t.Run("multiple_args_array", func(t *testing.T) {
		sub := &Subcommand{
			Name:        []string{"set"},
			Description: "Set value",
			Args:        []Arg{{Name: "key"}, {Name: "value"}},
		}
		result := renderSubcommand(sub, 1)
		require.Contains(t, result, "args: [")
		require.Contains(t, result, "'key'")
		require.Contains(t, result, "'value'")
	})
}

func TestToTypeScript(t *testing.T) {
	spec := &Spec{
		Name:        "azd",
		Description: "Azure Developer CLI",
		Subcommands: []Subcommand{
			{
				Name:        []string{"init"},
				Description: "Initialize app",
				Options: []Option{
					{
						Name:        []string{"--template", "-t"},
						Description: "Template name",
						Args:        []Arg{{Name: "template"}},
					},
				},
			},
		},
		Options: []Option{
			{
				Name:         []string{"--debug", "-d"},
				Description:  "Enable debug",
				IsPersistent: true,
			},
		},
	}

	ts, err := spec.ToTypeScript()
	require.NoError(t, err)
	require.Contains(t, ts, "name: 'azd'")
	require.Contains(t, ts, "const completionSpec: Fig.Spec")
	require.Contains(t, ts, "export default completionSpec")
	require.Contains(t, ts, "'init'")
	require.Contains(t, ts, "'--template'")
	require.Contains(t, ts, "'--debug'")
	require.True(t, strings.HasSuffix(ts, "\n"))
}
