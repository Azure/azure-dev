// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ext

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/azure/azure-dev/cli/azd/internal"
)

type Event string

type EventHandlerFn[T any] func(ctx context.Context, args T) error

var (
	ErrInvalidEvent = errors.New("invalid event name for the current type")
)

// handlerRegistration pairs a handler with a unique ID and its registration context
// for reliable identity tracking and stale handler detection.
type handlerRegistration[T any] struct {
	id      uint64
	ctx     context.Context
	handler EventHandlerFn[T]
}

type EventDispatcher[T any] struct {
	mu         sync.RWMutex
	handlers   map[Event][]handlerRegistration[T]
	eventNames map[Event]struct{}
	nextID     uint64
}

func NewEventDispatcher[T any](validEventNames ...Event) *EventDispatcher[T] {
	eventNames := map[Event]struct{}{}
	for _, name := range validEventNames {
		eventNames[name] = struct{}{}
		eventNames[Event("pre"+name)] = struct{}{}
		eventNames[Event("post"+name)] = struct{}{}
	}

	return &EventDispatcher[T]{
		handlers:   map[Event][]handlerRegistration[T]{},
		eventNames: eventNames,
	}
}

// AddHandler adds an event handler for the specified event name.
// The handler is automatically removed when ctx is cancelled.
func (ed *EventDispatcher[T]) AddHandler(ctx context.Context, name Event, handler EventHandlerFn[T]) error {
	if err := ed.validateEvent(name); err != nil {
		return err
	}

	ed.mu.Lock()
	defer ed.mu.Unlock()

	ed.nextID++
	id := ed.nextID

	ed.handlers[name] = append(ed.handlers[name], handlerRegistration[T]{
		id:      id,
		ctx:     ctx,
		handler: handler,
	})

	// Only start cleanup goroutine when the context is cancellable.
	// For non-cancellable contexts (e.g., context.Background()), the handler
	// persists for the lifetime of the dispatcher.
	if ctx.Done() != nil {
		go func(ctx context.Context, name Event, id uint64) {
			<-ctx.Done()
			ed.removeByID(name, id)
		}(ctx, name, id)
	}

	return nil
}

// removeByID removes a handler registration by its unique ID.
func (ed *EventDispatcher[T]) removeByID(name Event, id uint64) {
	ed.mu.Lock()
	defer ed.mu.Unlock()

	entries := ed.handlers[name]
	for i, entry := range entries {
		if entry.id == id {
			ed.handlers[name] = append(entries[:i], entries[i+1:]...)
			return
		}
	}
}

// Raises the specified event and calls any registered event handlers
func (ed *EventDispatcher[T]) RaiseEvent(ctx context.Context, name Event, eventArgs T) error {
	if err := ed.validateEvent(name); err != nil {
		return err
	}

	ed.mu.RLock()
	entries := make([]handlerRegistration[T], len(ed.handlers[name]))
	copy(entries, ed.handlers[name])
	ed.mu.RUnlock()

	handlerErrors := []error{}

	// TODO: Opportunity to dispatch these event handlers in parallel if needed
	for _, entry := range entries {
		// Skip handlers whose registration context has been cancelled.
		// This prevents stale handlers from firing during the window between
		// context cancellation and async cleanup goroutine execution
		// (e.g., between workflow steps in azd up).
		if entry.ctx.Err() != nil {
			continue
		}

		err := entry.handler(ctx, eventArgs)
		if err != nil {
			handlerErrors = append(handlerErrors, err)
		}
	}

	// Build final error string if their are any failures
	if len(handlerErrors) > 0 {
		// For multiple errors, join them as before
		lines := make([]string, len(handlerErrors))
		// If any of the errors have a suggestion, collect them
		var suggestions []string
		for i, err := range handlerErrors {
			lines[i] = err.Error()
			if errWithSuggestion, ok := errors.AsType[*internal.ErrorWithSuggestion](err); ok &&
				errWithSuggestion.Suggestion != "" {
				suggestions = append(suggestions, errWithSuggestion.Suggestion)
			}
		}
		combinedErr := errors.New(strings.Join(lines, ","))
		if len(suggestions) > 0 {
			return &internal.ErrorWithSuggestion{
				Err:        combinedErr,
				Suggestion: strings.Join(suggestions, "\n"),
			}
		}
		return combinedErr
	}

	return nil
}

// Invokes an action and raises an event before and after the action
func (ed *EventDispatcher[T]) Invoke(ctx context.Context, name Event, eventArgs T, action InvokeFn) error {
	if err := ed.validateEvent(name); err != nil {
		return err
	}

	preEventName := Event(fmt.Sprintf("pre%s", name))
	postEventName := Event(fmt.Sprintf("post%s", name))

	if err := ed.RaiseEvent(ctx, preEventName, eventArgs); err != nil {
		return fmt.Errorf("failed invoking event handlers for 'pre%s', %w", name, err)
	}

	if err := action(); err != nil {
		return err
	}

	if err := ed.RaiseEvent(ctx, postEventName, eventArgs); err != nil {
		return fmt.Errorf("failed invoking event handlers for 'post%s', %w", name, err)
	}

	return nil
}

func (ed *EventDispatcher[T]) validateEvent(name Event) error {
	// If not events have been defined assumed any event name is valid
	if len(ed.eventNames) == 0 {
		return nil
	}

	if _, has := ed.eventNames[name]; !has {
		return fmt.Errorf("%s: %w", name, ErrInvalidEvent)
	}

	return nil
}
