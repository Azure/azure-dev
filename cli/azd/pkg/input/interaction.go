// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package input

import "sync"

// TerminalRenderer is a component that periodically redraws content in the terminal
// (for example, a live progress table). Registered renderers must yield the terminal
// while an interactive prompt is displayed so the prompt is not overwritten.
type TerminalRenderer interface {
	// PauseRendering stops all terminal output from the renderer and clears any
	// dynamic content it owns. It must not return until in-flight output completes.
	PauseRendering()
	// ResumeRendering re-enables terminal output from the renderer.
	ResumeRendering()
}

// interactionCoordinator gives interactive prompts exclusive ownership of the terminal
// by pausing every registered TerminalRenderer for the duration of the interaction.
// The terminal is a process-wide resource, so the coordinator is process-wide as well.
type interactionCoordinator struct {
	mu        sync.Mutex
	renderers map[*rendererRegistration]struct{}
	// active is the number of in-progress interactions. Renderers are paused while it is non-zero.
	active int
}

type rendererRegistration struct {
	renderer TerminalRenderer
}

var defaultInteractionCoordinator = newInteractionCoordinator()

func newInteractionCoordinator() *interactionCoordinator {
	return &interactionCoordinator{renderers: map[*rendererRegistration]struct{}{}}
}

// RegisterTerminalRenderer registers a renderer that is paused while interactive prompts
// are displayed. If an interaction is already in progress, the renderer is paused immediately.
// The returned function removes the registration; it is safe to call more than once.
func RegisterTerminalRenderer(renderer TerminalRenderer) (unregister func()) {
	return defaultInteractionCoordinator.register(renderer)
}

// BeginInteraction marks the start of an interactive prompt and pauses all registered
// terminal renderers. The returned function ends the interaction and resumes the renderers
// once no other interactions are in progress; it is safe to call more than once.
func BeginInteraction() (end func()) {
	return defaultInteractionCoordinator.begin()
}

func (c *interactionCoordinator) register(renderer TerminalRenderer) func() {
	reg := &rendererRegistration{renderer: renderer}

	c.mu.Lock()
	c.renderers[reg] = struct{}{}
	if c.active > 0 {
		renderer.PauseRendering()
	}
	c.mu.Unlock()

	return sync.OnceFunc(func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		delete(c.renderers, reg)
	})
}

func (c *interactionCoordinator) begin() func() {
	c.mu.Lock()
	c.active++
	if c.active == 1 {
		for reg := range c.renderers {
			reg.renderer.PauseRendering()
		}
	}
	c.mu.Unlock()

	return sync.OnceFunc(func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.active--
		if c.active == 0 {
			for reg := range c.renderers {
				reg.renderer.ResumeRendering()
			}
		}
	})
}
