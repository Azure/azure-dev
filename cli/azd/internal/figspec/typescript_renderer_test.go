// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package figspec

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEscapeString(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"plain", "hello world", "hello world"},
		{"backslash", `a\b`, `a\\b`},
		{"single_quote", "it's", `it\'s`},
		{"newline", "a\nb", `a\nb`},
		{"carriage_return", "a\rb", `a\rb`},
		{"tab", "a\tb", `a\tb`},
		{"combined", "it's a\nnew\\line", `it\'s a\nnew\\line`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, escapeString(tt.in))
		})
	}
}

func TestQuoteString(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"simple", "hello", "'hello'"},
		{"with_quote", "it's", `'it\'s'`},
		{"empty", "", "''"},
		// After escaping, \n is literal characters `\n`, so no real newline remains
		{"already_escaped_newline", "line1\nline2", `'line1\nline2'`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, quoteString(tt.in))
		})
	}
}

func TestIndentString(t *testing.T) {
	tests := []struct {
		name  string
		s     string
		level int
		want  string
	}{
		{"zero_indent", "hello", 0, "hello"},
		{"one_tab", "hello", 1, "\thello"},
		{"two_tabs", "hello", 2, "\t\thello"},
		{"multiline", "a\nb", 1, "\ta\n\tb"},
		{"empty_lines_preserved", "a\n\nb", 1, "\ta\n\n\tb"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, indentString(tt.s, tt.level))
		})
	}
}

func TestRenderBoolField(t *testing.T) {
	tests := []struct {
		name      string
		fieldName string
		value     bool
		indent    string
		want      string
	}{
		{"false_returns_empty", "hidden", false, "\t\t", ""},
		{"true_returns_field", "hidden", true, "\t\t", "\t\t\thidden: true,"},
		{"is_persistent", "isPersistent", true, "\t", "\t\tisPersistent: true,"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, renderBoolField(tt.fieldName, tt.value, tt.indent))
		})
	}
}

func TestRenderNameField(t *testing.T) {
	tests := []struct {
		name   string
		names  []string
		indent string
	}{
		{"single_name", []string{"init"}, "\t\t"},
		{"multiple_names", []string{"init", "i"}, "\t\t"},
		{"three_names", []string{"list", "ls", "l"}, "\t"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := renderNameField(tt.names, tt.indent)
			require.Contains(t, result, "name:")
			for _, n := range tt.names {
				require.Contains(t, result, "'"+n+"'")
			}
		})
	}
}

func TestRenderArgs(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		require.Equal(t, "", renderArgs(nil, 2))
		require.Equal(t, "", renderArgs([]Arg{}, 2))
	})

	t.Run("single_basic", func(t *testing.T) {
		result := renderArgs([]Arg{{Name: "env"}}, 2)
		require.Contains(t, result, "name: 'env'")
	})

	t.Run("optional", func(t *testing.T) {
		result := renderArgs([]Arg{{Name: "env", IsOptional: true}}, 2)
		require.Contains(t, result, "isOptional: true")
	})

	t.Run("with_description", func(t *testing.T) {
		result := renderArgs([]Arg{{Name: "env", Description: "The environment"}}, 2)
		require.Contains(t, result, "description:")
		require.Contains(t, result, "The environment")
	})

	t.Run("with_generator", func(t *testing.T) {
		result := renderArgs([]Arg{{Name: "env", Generator: "azdGenerators.listEnvironments"}}, 2)
		require.Contains(t, result, "generators: azdGenerators.listEnvironments")
	})

	t.Run("with_template", func(t *testing.T) {
		result := renderArgs([]Arg{{Name: "path", Template: "filepaths"}}, 2)
		require.Contains(t, result, "template: 'filepaths'")
	})

	t.Run("few_suggestions_inline", func(t *testing.T) {
		result := renderArgs([]Arg{{Name: "fmt", Suggestions: []string{"json", "table"}}}, 2)
		require.Contains(t, result, "suggestions: ['json', 'table']")
	})

	t.Run("many_suggestions_multiline", func(t *testing.T) {
		args := []Arg{{Name: "hook", Suggestions: []string{
			"prebuild", "postbuild", "predeploy", "postdeploy",
		}}}
		result := renderArgs(args, 2)
		require.Contains(t, result, "suggestions: [")
		require.Contains(t, result, "'prebuild',")
	})
}

func TestRenderOptions(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		require.Equal(t, "", renderOptions(nil, 2, false))
		require.Equal(t, "", renderOptions([]Option{}, 2, false))
	})

	t.Run("filters_persistent_in_subcommand", func(t *testing.T) {
		opts := []Option{
			{Name: []string{"--local"}, Description: "Local flag", IsPersistent: false},
			{Name: []string{"--global"}, Description: "Global flag", IsPersistent: true},
		}
		// inSubcommand=true should filter persistent
		result := renderOptions(opts, 2, true)
		require.Contains(t, result, "'--local'")
		require.NotContains(t, result, "'--global'")
	})

	t.Run("includes_persistent_at_root", func(t *testing.T) {
		opts := []Option{
			{Name: []string{"--global"}, Description: "Global flag", IsPersistent: true},
		}
		result := renderOptions(opts, 2, false)
		require.Contains(t, result, "'--global'")
	})

	t.Run("all_persistent_in_subcommand_returns_empty", func(t *testing.T) {
		opts := []Option{
			{Name: []string{"--global"}, Description: "Global flag", IsPersistent: true},
		}
		result := renderOptions(opts, 2, true)
		require.Equal(t, "", result)
	})
}

func TestRenderSubcommands(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		require.Equal(t, "", renderSubcommands(nil, 2))
		require.Equal(t, "", renderSubcommands([]Subcommand{}, 2))
	})

	t.Run("multiple", func(t *testing.T) {
		subs := []Subcommand{
			{Name: []string{"init"}, Description: "Init"},
			{Name: []string{"up"}, Description: "Deploy"},
		}
		result := renderSubcommands(subs, 1)
		require.Contains(t, result, "'init'")
		require.Contains(t, result, "'up'")
	})
}

func TestToTypeScript_Empty(t *testing.T) {
	spec := &Spec{
		Name:        "test",
		Description: "Test CLI",
	}
	ts, err := spec.ToTypeScript()
	require.NoError(t, err)
	require.Contains(t, ts, "name: 'test'")
	require.True(t, strings.HasSuffix(ts, "\n"))
}
