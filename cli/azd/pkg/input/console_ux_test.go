// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package input

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	surveyterm "github.com/AlecAivazis/survey/v2/terminal"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	uxlib "github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/stretchr/testify/require"
)

// newTestAskerConsole builds a non-terminal AskerConsole suitable for exercising
// the ux helper plumbing (spinner pausing, result bookkeeping) without a TTY.
func newTestAskerConsole(t *testing.T) *AskerConsole {
	t.Helper()

	formatter, err := output.NewFormatter(string(output.NoneFormat))
	require.NoError(t, err)

	buf := &bytes.Buffer{}
	c := NewConsole(
		false,
		false,
		Writers{Output: buf},
		ConsoleHandles{Stderr: io.Discard, Stdin: strings.NewReader(""), Stdout: buf},
		formatter,
		nil,
	)

	asker, ok := c.(*AskerConsole)
	require.True(t, ok)
	return asker
}

// fakeUxComponent is a test double for the ux prompt components used by runComponent.
type fakeUxComponent[T any] struct {
	result T
	err    error
}

func (f fakeUxComponent[T]) Ask(ctx context.Context) (T, error) {
	return f.result, f.err
}

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

func TestNewPromptOptions(t *testing.T) {
	buf := &bytes.Buffer{}
	opts := newPromptOptions(buf, ConsoleOptions{
		Message:      "message",
		Help:         "help",
		DefaultValue: "default",
		IsPassword:   true,
	})

	require.Equal(t, buf, opts.Writer)
	require.Equal(t, "message", opts.Message)
	require.Equal(t, "help", opts.HelpMessage)
	require.Equal(t, "default", opts.DefaultValue)
	require.True(t, opts.Secret)

	t.Run("non-string default is ignored", func(t *testing.T) {
		opts := newPromptOptions(buf, ConsoleOptions{DefaultValue: 42})
		require.Empty(t, opts.DefaultValue)
	})
}

func TestNewSelectOptions(t *testing.T) {
	buf := &bytes.Buffer{}
	opts := newSelectOptions(buf, ConsoleOptions{
		Message:      "message",
		Options:      []string{"alpha", "beta", "gamma"},
		DefaultValue: "gamma",
	})

	require.Equal(t, buf, opts.Writer)
	require.Equal(t, "message", opts.Message)
	require.Len(t, opts.Choices, 3)
	require.NotNil(t, opts.SelectedIndex)
	require.Equal(t, 2, *opts.SelectedIndex)
}

func TestNewConfirmOptions(t *testing.T) {
	buf := &bytes.Buffer{}

	t.Run("uses bool default", func(t *testing.T) {
		opts := newConfirmOptions(buf, ConsoleOptions{Message: "message", DefaultValue: true})
		require.Equal(t, buf, opts.Writer)
		require.Equal(t, "message", opts.Message)
		require.NotNil(t, opts.DefaultValue)
		require.True(t, *opts.DefaultValue)
	})

	t.Run("defaults to false", func(t *testing.T) {
		opts := newConfirmOptions(buf, ConsoleOptions{})
		require.NotNil(t, opts.DefaultValue)
		require.False(t, *opts.DefaultValue)
	})
}

func TestNewMultiSelectOptions(t *testing.T) {
	buf := &bytes.Buffer{}
	opts := newMultiSelectOptions(buf, ConsoleOptions{
		Message:      "message",
		Options:      []string{"alpha", "beta"},
		DefaultValue: []string{"beta"},
	})

	require.Equal(t, buf, opts.Writer)
	require.Equal(t, "message", opts.Message)
	require.Len(t, opts.Choices, 2)
	require.False(t, opts.Choices[0].Selected)
	require.True(t, opts.Choices[1].Selected)
	require.NotNil(t, opts.AllowEmptySelection)
	require.True(t, *opts.AllowEmptySelection)
}

func TestSelectResult(t *testing.T) {
	t.Run("error passes through", func(t *testing.T) {
		boom := errors.New("boom")
		_, err := selectResult(nil, boom)
		require.ErrorIs(t, err, boom)
	})

	t.Run("nil result is an interrupt", func(t *testing.T) {
		got, err := selectResult(nil, nil)
		require.ErrorIs(t, err, surveyterm.InterruptErr)
		require.Equal(t, -1, got)
	})

	t.Run("returns dereferenced index", func(t *testing.T) {
		got, err := selectResult(new(2), nil)
		require.NoError(t, err)
		require.Equal(t, 2, got)
	})
}

func TestConfirmResult(t *testing.T) {
	t.Run("error passes through", func(t *testing.T) {
		boom := errors.New("boom")
		_, err := confirmResult(nil, boom)
		require.ErrorIs(t, err, boom)
	})

	t.Run("nil result is an interrupt", func(t *testing.T) {
		got, err := confirmResult(nil, nil)
		require.ErrorIs(t, err, surveyterm.InterruptErr)
		require.False(t, got)
	})

	t.Run("returns dereferenced value", func(t *testing.T) {
		got, err := confirmResult(new(true), nil)
		require.NoError(t, err)
		require.True(t, got)
	})
}

func TestMultiSelectResult(t *testing.T) {
	t.Run("error passes through", func(t *testing.T) {
		boom := errors.New("boom")
		_, err := multiSelectResult(nil, boom)
		require.ErrorIs(t, err, boom)
	})

	t.Run("maps selected values", func(t *testing.T) {
		got, err := multiSelectResult([]*uxlib.MultiSelectChoice{{Value: "alpha"}, {Value: "beta"}}, nil)
		require.NoError(t, err)
		require.Equal(t, []string{"alpha", "beta"}, got)
	})
}

func TestRunComponent(t *testing.T) {
	c := newTestAskerConsole(t)

	t.Run("returns result on success", func(t *testing.T) {
		got, err := runComponent[string](t.Context(), c, fakeUxComponent[string]{result: "hello"})
		require.NoError(t, err)
		require.Equal(t, "hello", got)
	})

	t.Run("maps cancellation to interrupt", func(t *testing.T) {
		_, err := runComponent[string](t.Context(), c, fakeUxComponent[string]{err: uxlib.ErrCancelled})
		require.ErrorIs(t, err, surveyterm.InterruptErr)
	})

	t.Run("passes through other errors", func(t *testing.T) {
		boom := errors.New("boom")
		_, err := runComponent[string](t.Context(), c, fakeUxComponent[string]{err: boom})
		require.ErrorIs(t, err, boom)
	})

	t.Run("supports pointer result types", func(t *testing.T) {
		got, err := runComponent[*int](t.Context(), c, fakeUxComponent[*int]{result: new(7)})
		require.NoError(t, err)
		require.NotNil(t, got)
		require.Equal(t, 7, *got)
	})
}
