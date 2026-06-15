// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package input

import (
	"errors"
	"fmt"
	"testing"

	surveyterm "github.com/AlecAivazis/survey/v2/terminal"
	uxlib "github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/stretchr/testify/require"
)

func TestMapUxCancel(t *testing.T) {
	t.Run("nil passes through", func(t *testing.T) {
		require.NoError(t, mapUxCancel(nil))
	})

	t.Run("ErrCancelled maps to InterruptErr", func(t *testing.T) {
		require.ErrorIs(t, mapUxCancel(uxlib.ErrCancelled), surveyterm.InterruptErr)
	})

	t.Run("wrapped ErrCancelled maps to InterruptErr", func(t *testing.T) {
		wrapped := fmt.Errorf("reading input: %w", uxlib.ErrCancelled)
		require.ErrorIs(t, mapUxCancel(wrapped), surveyterm.InterruptErr)
	})

	t.Run("other errors pass through unchanged", func(t *testing.T) {
		other := errors.New("boom")
		require.Equal(t, other, mapUxCancel(other))
	})
}

func TestOptionLabel(t *testing.T) {
	t.Run("no details returns plain option", func(t *testing.T) {
		options := ConsoleOptions{Options: []string{"alpha", "beta"}}
		require.Equal(t, "alpha", optionLabel(options, 0))
		require.Equal(t, "beta", optionLabel(options, 1))
	})

	t.Run("detail is appended", func(t *testing.T) {
		options := ConsoleOptions{
			Options:       []string{"alpha"},
			OptionDetails: []string{"the first"},
		}
		require.Contains(t, optionLabel(options, 0), "alpha")
		require.Contains(t, optionLabel(options, 0), "the first")
	})

	t.Run("empty detail is ignored", func(t *testing.T) {
		options := ConsoleOptions{
			Options:       []string{"alpha", "beta"},
			OptionDetails: []string{"", "second"},
		}
		require.Equal(t, "alpha", optionLabel(options, 0))
		require.Contains(t, optionLabel(options, 1), "second")
	})

	t.Run("details shorter than options does not panic", func(t *testing.T) {
		options := ConsoleOptions{
			Options:       []string{"alpha", "beta"},
			OptionDetails: []string{"only first"},
		}
		require.Contains(t, optionLabel(options, 0), "only first")
		require.Equal(t, "beta", optionLabel(options, 1))
	})
}

func TestSelectChoices(t *testing.T) {
	t.Run("maps options to choices with default index 0", func(t *testing.T) {
		options := ConsoleOptions{Options: []string{"alpha", "beta", "gamma"}}
		choices, selectedIndex := selectChoices(options)

		require.Len(t, choices, 3)
		require.Equal(t, "alpha", choices[0].Value)
		require.Equal(t, "alpha", choices[0].Label)
		require.Equal(t, "gamma", choices[2].Value)
		require.Equal(t, 0, selectedIndex)
	})

	t.Run("default value selects matching index", func(t *testing.T) {
		options := ConsoleOptions{
			Options:      []string{"alpha", "beta", "gamma"},
			DefaultValue: "gamma",
		}
		_, selectedIndex := selectChoices(options)
		require.Equal(t, 2, selectedIndex)
	})

	t.Run("default value not in options falls back to 0", func(t *testing.T) {
		options := ConsoleOptions{
			Options:      []string{"alpha", "beta"},
			DefaultValue: "missing",
		}
		_, selectedIndex := selectChoices(options)
		require.Equal(t, 0, selectedIndex)
	})

	t.Run("non-string default falls back to 0", func(t *testing.T) {
		options := ConsoleOptions{
			Options:      []string{"alpha", "beta"},
			DefaultValue: 42,
		}
		_, selectedIndex := selectChoices(options)
		require.Equal(t, 0, selectedIndex)
	})

	t.Run("labels include option details", func(t *testing.T) {
		options := ConsoleOptions{
			Options:       []string{"alpha"},
			OptionDetails: []string{"detail"},
		}
		choices, _ := selectChoices(options)
		require.Equal(t, "alpha", choices[0].Value)
		require.Contains(t, choices[0].Label, "detail")
	})
}

func TestMultiSelectChoices(t *testing.T) {
	t.Run("maps options with none selected by default", func(t *testing.T) {
		options := ConsoleOptions{Options: []string{"alpha", "beta"}}
		choices := multiSelectChoices(options)

		require.Len(t, choices, 2)
		require.Equal(t, "alpha", choices[0].Value)
		require.False(t, choices[0].Selected)
		require.False(t, choices[1].Selected)
	})

	t.Run("default values mark choices selected", func(t *testing.T) {
		options := ConsoleOptions{
			Options:      []string{"alpha", "beta", "gamma"},
			DefaultValue: []string{"alpha", "gamma"},
		}
		choices := multiSelectChoices(options)

		require.True(t, choices[0].Selected)
		require.False(t, choices[1].Selected)
		require.True(t, choices[2].Selected)
	})

	t.Run("non-slice default selects nothing", func(t *testing.T) {
		options := ConsoleOptions{
			Options:      []string{"alpha", "beta"},
			DefaultValue: "alpha",
		}
		choices := multiSelectChoices(options)
		require.False(t, choices[0].Selected)
		require.False(t, choices[1].Selected)
	})

	t.Run("labels include option details", func(t *testing.T) {
		options := ConsoleOptions{
			Options:       []string{"alpha"},
			OptionDetails: []string{"detail"},
		}
		choices := multiSelectChoices(options)
		require.Contains(t, choices[0].Label, "detail")
	})
}

func TestMultiSelectValues(t *testing.T) {
	t.Run("maps choices back to values", func(t *testing.T) {
		selected := []*uxlib.MultiSelectChoice{
			{Value: "alpha"},
			{Value: "gamma"},
		}
		require.Equal(t, []string{"alpha", "gamma"}, multiSelectValues(selected))
	})

	t.Run("empty selection returns empty slice", func(t *testing.T) {
		require.Empty(t, multiSelectValues(nil))
		require.Empty(t, multiSelectValues([]*uxlib.MultiSelectChoice{}))
	})
}
