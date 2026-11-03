// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

func TestPromptTextInput(t *testing.T) {
	for _, kind := range []PromptType{PromptTypeString, PromptTypePassword, PromptTypeDirectory} {
		t.Run(string(kind), func(t *testing.T) {
			m := newPromptModel(&PromptRequest{
				Type: kind, Options: PromptOptions{DefaultValue: "initial"},
			})
			require.Equal(t, 50, m.textInput.Width())
			require.True(t, m.textInput.VirtualCursor())
			m, _ = m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
			require.Equal(t, "initialx", m.textInput.Value())
			if kind == PromptTypePassword {
				require.NotContains(t, m.View(), "initialx")
			}
			m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			require.True(t, m.submitted)
			require.Equal(t, PromptStatusSuccess, m.GetResponse().Status)
			require.Equal(t, "initialx", m.GetResponse().Value)
		})
	}
}

func TestPromptSelectFilteringAndScrolling(t *testing.T) {
	m := newPromptModel(&PromptRequest{
		Type: PromptTypeSelect,
		Options: PromptOptions{
			Choices: []PromptChoice{{Value: "alpha"}, {Value: "beta"}, {Value: "gamma"}, {Value: "delta"}},
		},
	})
	for range 3 {
		m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	require.Equal(t, 3, m.selectedIndex)
	require.Equal(t, 1, m.scrollOffset)
	m, _ = m.Update(tea.KeyPressMsg{Code: 'b', Text: "b"})
	require.Equal(t, []int{1}, m.filteredIndices)
	require.Zero(t, m.selectedIndex)
	require.Zero(t, m.scrollOffset)
	m, _ = m.Update(tea.KeyReleaseMsg{Code: 'b', Text: "b"})
	require.Equal(t, "b", m.filterText)
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, "beta", m.GetResponse().Value)
}

func TestPromptMultiSelectKeepsOriginalIndices(t *testing.T) {
	m := newPromptModel(&PromptRequest{
		Type: PromptTypeMultiSelect,
		Options: PromptOptions{
			Choices: []PromptChoice{{Value: "alpha"}, {Value: "beta"}}, DefaultValue: []any{"alpha"},
		},
	})
	m, _ = m.Update(tea.KeyPressMsg{Code: 'b', Text: "b"})
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m, _ = m.Update(tea.KeyReleaseMsg{Code: tea.KeyTab})
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	require.Equal(t, []int{0, 1}, m.filteredIndices)
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, []string{"alpha", "beta"}, m.GetResponse().Value)
}

func TestPromptConfirmationAndCancellation(t *testing.T) {
	for _, tt := range []struct {
		key      tea.KeyPressMsg
		expected string
	}{
		{key: tea.KeyPressMsg{Code: 'y', Text: "Y", Mod: tea.ModShift}, expected: "true"},
		{key: tea.KeyPressMsg{Code: 'n', Text: "N", Mod: tea.ModShift}, expected: "false"},
	} {
		m := newPromptModel(&PromptRequest{Type: PromptTypeConfirm})
		m, _ = m.Update(tt.key)
		require.True(t, m.submitted)
		require.Equal(t, tt.expected, m.GetResponse().Value)
	}
	m := newPromptModel(&PromptRequest{Type: PromptTypeString})
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.True(t, m.submitted)
	require.Equal(t, PromptStatusCancelled, m.GetResponse().Status)
}
