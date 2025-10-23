// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ext

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/stretchr/testify/require"
)

func Test_Valid_Event_Names(t *testing.T) {
	handler := func(ctx context.Context, args testEventArgs) error {
		return nil
	}

	ed := NewEventDispatcher[testEventArgs](testEvent)
	err := ed.AddHandler(context.Background(), testEvent, handler)
	require.NoError(t, err)

	err = ed.RemoveHandler(context.Background(), testEvent, handler)
	require.NoError(t, err)

	err = ed.Invoke(context.Background(), testEvent, testEventArgs{}, func() error {
		return nil
	})

	require.NoError(t, err)
}

func Test_Invalid_Event_Names(t *testing.T) {
	handler := func(ctx context.Context, args testEventArgs) error {
		return nil
	}

	invalidEventName := Event("invalid")
	ed := NewEventDispatcher[testEventArgs](testEvent)

	tests := map[string]func() error{
		"AddHandler": func() error {
			return ed.AddHandler(context.Background(), invalidEventName, handler)
		},
		"RemoveHandler": func() error {
			return ed.RemoveHandler(context.Background(), invalidEventName, handler)
		},
		"Invoke": func() error {
			return ed.Invoke(context.Background(), invalidEventName, testEventArgs{}, func() error {
				return nil
			})
		},
	}

	for testName, fn := range tests {
		t.Run(testName, func(t *testing.T) {
			err := fn()
			require.ErrorIs(t, err, ErrInvalidEvent)
		})
	}
}

func Test_Multiple_Errors_With_Suggestions(t *testing.T) {
	handler1 := func(ctx context.Context, args testEventArgs) error {
		return &internal.ErrorWithSuggestion{
			Err:        errors.New("Error1 for test"),
			Suggestion: "Suggestion1",
		}
	}
	handler2 := func(ctx context.Context, args testEventArgs) error {
		return &internal.ErrorWithSuggestion{
			Err:        errors.New("Error2 for test"),
			Suggestion: "Suggestion2",
		}
	}

	ed := NewEventDispatcher[testEventArgs](testEvent)
	err := ed.AddHandler(context.Background(), testEvent, handler1)
	require.NoError(t, err)
	err = ed.AddHandler(context.Background(), testEvent, handler2)
	require.NoError(t, err)

	err = ed.RaiseEvent(context.Background(), testEvent, testEventArgs{})
	require.Error(t, err)

	var errWithSuggestion *internal.ErrorWithSuggestion
	ok := errors.As(err, &errWithSuggestion)
	require.True(t, ok)

	require.Contains(t, errWithSuggestion.Error(), "Error1 for test")
	require.Contains(t, errWithSuggestion.Error(), "Error2 for test")

	suggestion := errWithSuggestion.Suggestion
	require.Contains(t, suggestion, "Suggestion1")
	require.Contains(t, suggestion, "Suggestion2")
}

func Test_Automatic_Handler_Removal_On_Context_Done(t *testing.T) {
	callCount := 0
	handler := func(ctx context.Context, args testEventArgs) error {
		callCount++
		return nil
	}

	ed := NewEventDispatcher[testEventArgs](testEvent)

	// Create a cancellable context
	ctx, cancel := context.WithCancel(context.Background())

	// Add handler with the cancellable context
	err := ed.AddHandler(ctx, testEvent, handler)
	require.NoError(t, err)

	// Verify handler is registered by calling it
	err = ed.RaiseEvent(context.Background(), testEvent, testEventArgs{})
	require.NoError(t, err)
	require.Equal(t, 1, callCount, "Handler should be called initially")

	// Cancel the context to trigger automatic removal
	cancel()

	// Give the cleanup goroutine time to process the cancellation
	time.Sleep(50 * time.Millisecond)

	// Reset call count to verify handler was actually removed
	callCount = 0

	// Attempt to raise the event again - handler should not be called
	err = ed.RaiseEvent(context.Background(), testEvent, testEventArgs{})
	require.NoError(t, err)
	require.Equal(t, 0, callCount, "Handler should not be called after context cancellation")

	// Verify the handler was actually removed from the internal handlers map
	ed.mu.RLock()
	remainingHandlers := len(ed.handlers[testEvent])
	ed.mu.RUnlock()
	require.Equal(t, 0, remainingHandlers, "Handler should be removed from internal handlers map")
}

func Test_Multiple_Handlers_Selective_Removal_On_Context_Done(t *testing.T) {
	callCount1 := 0
	handler1 := func(ctx context.Context, args testEventArgs) error {
		callCount1++
		return nil
	}

	callCount2 := 0
	handler2 := func(ctx context.Context, args testEventArgs) error {
		callCount2++
		return nil
	}

	callCount3 := 0
	handler3 := func(ctx context.Context, args testEventArgs) error {
		callCount3++
		return nil
	}

	ed := NewEventDispatcher[testEventArgs](testEvent)

	// Create different contexts
	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())
	ctx3 := context.Background() // Non-cancellable context
	defer cancel2()              // Ensure cleanup

	// Add handlers with different contexts
	err := ed.AddHandler(ctx1, testEvent, handler1)
	require.NoError(t, err)

	err = ed.AddHandler(ctx2, testEvent, handler2)
	require.NoError(t, err)

	err = ed.AddHandler(ctx3, testEvent, handler3)
	require.NoError(t, err)

	// All handlers should be called initially
	err = ed.RaiseEvent(context.Background(), testEvent, testEventArgs{})
	require.NoError(t, err)
	require.Equal(t, 1, callCount1, "Handler1 should be called initially")
	require.Equal(t, 1, callCount2, "Handler2 should be called initially")
	require.Equal(t, 1, callCount3, "Handler3 should be called initially")

	// Cancel only ctx1
	cancel1()

	// Give cleanup time to process
	time.Sleep(50 * time.Millisecond)

	// Reset counters
	callCount1 = 0
	callCount2 = 0
	callCount3 = 0

	// Only handler1 should be removed, handler2 and handler3 should still be called
	err = ed.RaiseEvent(context.Background(), testEvent, testEventArgs{})
	require.NoError(t, err)
	require.Equal(t, 0, callCount1, "Handler1 should not be called after its context cancellation")
	require.Equal(t, 1, callCount2, "Handler2 should still be called")
	require.Equal(t, 1, callCount3, "Handler3 should still be called")

	// Verify handler count in internal map
	ed.mu.RLock()
	remainingHandlers := len(ed.handlers[testEvent])
	ed.mu.RUnlock()
	require.Equal(t, 2, remainingHandlers, "Should have 2 handlers remaining after selective removal")
}

func Test_Already_Cancelled_Context_Handler_Cleanup(t *testing.T) {
	callCount := 0
	handler := func(ctx context.Context, args testEventArgs) error {
		callCount++
		return nil
	}

	ed := NewEventDispatcher[testEventArgs](testEvent)

	// Create and immediately cancel a context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel before adding handler

	// Add handler with already cancelled context
	err := ed.AddHandler(ctx, testEvent, handler)
	require.NoError(t, err)

	// Give cleanup goroutine time to detect the already-cancelled context
	time.Sleep(50 * time.Millisecond)

	// Handler should not be called because context was already cancelled
	err = ed.RaiseEvent(context.Background(), testEvent, testEventArgs{})
	require.NoError(t, err)
	require.Equal(t, 0, callCount, "Handler should not be called with already cancelled context")

	// Verify handler was removed from internal map
	ed.mu.RLock()
	remainingHandlers := len(ed.handlers[testEvent])
	ed.mu.RUnlock()
	require.Equal(t, 0, remainingHandlers, "Handler should be automatically removed for already cancelled context")
}

type testEventArgs struct{}

const testEvent Event = "test"
