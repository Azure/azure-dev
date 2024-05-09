package ext

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type Event string

type EventHandlerFn[T any] func(ctx context.Context, args T) error

var (
	ErrInvalidEvent = errors.New("invalid event name for the current type")
)

type EventDispatcher[T any] struct {
	handlers   map[Event][]EventHandlerFn[T]
	eventNames map[Event]struct{}
}

func NewEventDispatcher[T any](validEventNames ...Event) *EventDispatcher[T] {
	eventNames := map[Event]struct{}{}
	for _, name := range validEventNames {
		eventNames[name] = struct{}{}
		eventNames[Event("pre"+name)] = struct{}{}
		eventNames[Event("post"+name)] = struct{}{}
	}

	return &EventDispatcher[T]{
		handlers:   map[Event][]EventHandlerFn[T]{},
		eventNames: eventNames,
	}
}

// Adds an event handler for the specified event name
func (ed *EventDispatcher[T]) AddHandler(name Event, handler EventHandlerFn[T]) error {
	if err := ed.validateEvent(name); err != nil {
		return err
	}

	events := ed.handlers[name]
	events = append(events, handler)
	ed.handlers[name] = events

	return nil
}

// Removes the event handler for the specified event name
func (ed *EventDispatcher[T]) RemoveHandler(name Event, handler EventHandlerFn[T]) error {
	if err := ed.validateEvent(name); err != nil {
		return err
	}

	newHandler := fmt.Sprintf("%v", handler)
	events := ed.handlers[name]
	for i, ref := range events {
		existingHandler := fmt.Sprintf("%v", ref)

		if newHandler == existingHandler {
			ed.handlers[name] = append(events[:i], events[i+1:]...)
			return nil
		}
	}

	return fmt.Errorf("specified handler was not found in %s event registrations", name)
}

// Raises the specified event and calls any registered event handlers
func (ed *EventDispatcher[T]) RaiseEvent(ctx context.Context, name Event, eventArgs T) error {
	if err := ed.validateEvent(name); err != nil {
		return err
	}

	handlerErrors := []error{}
	handlers := ed.handlers[name]

	// TODO: Opportunity to dispatch these event handlers in parallel if needed
	for _, handler := range handlers {
		err := handler(ctx, eventArgs)
		if err != nil {
			handlerErrors = append(handlerErrors, err)
		}
	}

	// Build final error string if their are any failures
	if len(handlerErrors) > 0 {
		lines := make([]string, len(handlerErrors))
		for i, err := range handlerErrors {
			lines[i] = err.Error()
		}

		return errors.New(strings.Join(lines, ","))
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
