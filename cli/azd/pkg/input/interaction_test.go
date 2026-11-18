// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package input

import (
	"sync/atomic"
	"testing"

	"github.com/AlecAivazis/survey/v2"
	"github.com/stretchr/testify/require"
)

type fakeTerminalRenderer struct {
	pauses  atomic.Int32
	resumes atomic.Int32
}

func (r *fakeTerminalRenderer) PauseRendering()  { r.pauses.Add(1) }
func (r *fakeTerminalRenderer) ResumeRendering() { r.resumes.Add(1) }
func (r *fakeTerminalRenderer) paused() bool     { return r.pauses.Load() > r.resumes.Load() }

func TestInteractionCoordinator_OverlappingInteractions(t *testing.T) {
	c := newInteractionCoordinator()
	r := &fakeTerminalRenderer{}
	unregister := c.register(r)
	defer unregister()

	endFirst := c.begin()
	endSecond := c.begin()
	require.Equal(t, int32(1), r.pauses.Load(), "renderer should pause once for overlapping interactions")

	endFirst()
	endFirst() // ending twice must not end the other interaction
	require.True(t, r.paused(), "renderer must stay paused while another interaction is active")

	endSecond()
	require.Equal(t, int32(1), r.resumes.Load())
	require.False(t, r.paused())
}

func TestInteractionCoordinator_RegisterDuringInteraction(t *testing.T) {
	c := newInteractionCoordinator()
	end := c.begin()

	r := &fakeTerminalRenderer{}
	unregister := c.register(r)
	require.True(t, r.paused(), "renderer registered during an interaction must start paused")

	end()
	require.False(t, r.paused())

	unregister()
	c.begin()()
	require.Equal(t, int32(1), r.pauses.Load(), "unregistered renderer must not be paused again")
}

func TestAskerConsole_PromptPausesTerminalRenderers(t *testing.T) {
	console, _ := newTestConsole(t, false, nil)
	r := &fakeTerminalRenderer{}
	unregister := RegisterTerminalRenderer(r)
	defer unregister()

	var pausedDuringPrompt bool
	console.asker = func(_ survey.Prompt, response any) error {
		pausedDuringPrompt = r.paused()
		*(response.(*int)) = 1
		return nil
	}

	selected, err := console.Select(t.Context(), ConsoleOptions{Message: "Pick", Options: []string{"a", "b"}})
	require.NoError(t, err)
	require.Equal(t, 1, selected)
	require.True(t, pausedDuringPrompt, "renderer must be paused while the prompt is displayed")
	require.False(t, r.paused(), "renderer must resume after the prompt completes")
}
