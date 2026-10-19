// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

func updateDeploymentModel(t *testing.T, m deploymentModel, msg tea.Msg) deploymentModel {
	t.Helper()
	next, _ := m.Update(msg)
	result, ok := next.(deploymentModel)
	require.True(t, ok)
	return result
}

func TestDeploymentViewsKeepAlternateScreen(t *testing.T) {
	for _, tt := range []struct {
		name    string
		content string
		prepare func(*deploymentModel)
	}{
		{name: "progress", content: "Azure Developer CLI - ConcurX", prepare: func(*deploymentModel) {}},
		{name: "logs", content: "Deployment Logs", prepare: func(m *deploymentModel) { m.viewMode = viewLogs }},
		{name: "summary", content: "Deployment Summary", prepare: func(m *deploymentModel) { m.quitting = true }},
		{name: "error", content: "broken", prepare: func(m *deploymentModel) { m.err = errors.New("broken") }},
		{name: "prompt", content: "Choose a value", prepare: func(m *deploymentModel) {
			m.viewMode = viewPrompt
			prompt := newPromptModel(&PromptRequest{Type: PromptTypeString,
				Options: PromptOptions{Message: "Choose a value"}})
			m.activePrompt = &prompt
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := newDeploymentModel([]string{"api"}, nil, nil)
			tt.prepare(&m)
			view := m.View()
			require.True(t, view.AltScreen)
			require.Contains(t, view.Content, tt.content)
		})
	}
}

func TestDeploymentLogViewport(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "provision.log")
	require.NoError(t, os.WriteFile(logPath, []byte(strings.Repeat("log line\n", 100)), 0600))
	m := newDeploymentModel([]string{"api"}, nil, nil)
	m.provisionLogPath = logPath
	m = updateDeploymentModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 40})
	m = updateDeploymentModel(t, m, tea.KeyPressMsg{Code: 'l', Text: "L", Mod: tea.ModShift})
	require.Equal(t, viewLogs, m.viewMode)
	require.Equal(t, 96, m.viewport.Width())
	require.Equal(t, 33, m.viewport.Height())
	require.True(t, m.viewport.AtBottom())
	m = updateDeploymentModel(t, m, tea.KeyPressMsg{Code: 'i', Text: "I", Mod: tea.ModShift})
	require.True(t, m.viewport.AtTop())
	m = updateDeploymentModel(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
	require.Equal(t, 1, m.selectedTab)
	m = updateDeploymentModel(t, m, tea.KeyPressMsg{Code: tea.KeyLeft})
	require.Zero(t, m.selectedTab)
	m = updateDeploymentModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 30})
	require.Equal(t, 76, m.viewport.Width())
	require.Equal(t, 23, m.viewport.Height())
	m = updateDeploymentModel(t, m, tea.KeyPressMsg{Code: 'a', Text: "A", Mod: tea.ModShift})
	require.True(t, m.autoRefresh)
	m = updateDeploymentModel(t, m, tea.KeyPressMsg{Code: 'b', Text: "B", Mod: tea.ModShift})
	require.Equal(t, viewDeployment, m.viewMode)
}

func TestDeploymentCancellationUsesKeyPressOnly(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	m := newDeploymentModel(nil, cancel, nil)
	m = updateDeploymentModel(t, m, tea.KeyReleaseMsg{Code: 'c', Mod: tea.ModCtrl})
	require.NoError(t, ctx.Err())
	require.False(t, m.cancelled)
	m = updateDeploymentModel(t, m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	require.True(t, m.cancelled)
}

func TestDeploymentProgramUsesInjectedOutput(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var output bytes.Buffer
	program := tea.NewProgram(newDeploymentModel(nil, nil, nil),
		tea.WithContext(ctx), tea.WithInput(nil), tea.WithOutput(&output),
		tea.WithWindowSize(100, 30), tea.WithoutSignalHandler())
	sent := make(chan struct{})
	go func() {
		defer close(sent)
		program.Send(tea.KeyPressMsg{Code: 'q', Text: "q"})
	}()
	result, err := program.Run()
	<-sent
	require.NoError(t, err)
	m, ok := result.(deploymentModel)
	require.True(t, ok)
	require.True(t, m.cancelled)
	require.NotEmpty(t, output.String())
}
