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

func Test_Duplicate_Handler_Detection(t *testing.T) {
	callCount := 0
	handler := func(ctx context.Context, args testEventArgs) error {
		callCount++
		return nil
	}

	ed := NewEventDispatcher[testEventArgs](testEvent)

	// First registration should succeed
	err := ed.AddHandler(context.Background(), testEvent, handler)
	require.NoError(t, err)

	// Second registration of same handler should log warning and skip
	err = ed.AddHandler(context.Background(), testEvent, handler)
	require.NoError(t, err)

	// Verify handler was not added again by checking the handler count
	ed.mu.RLock()
	handlerCount := len(ed.handlers[testEvent])
	ed.mu.RUnlock()
	require.Equal(t, 1, handlerCount, "Should have only 1 handler, not 2")

	// Verify handler is only called once when event is raised
	err = ed.RaiseEvent(context.Background(), testEvent, testEventArgs{})
	require.NoError(t, err)
	require.Equal(t, 1, callCount, "Handler should be called only once, not twice")
}

func Test_Duplicate_Handler_Detection_Different_Events(t *testing.T) {
	callCount := 0
	handler := func(ctx context.Context, args testEventArgs) error {
		callCount++
		return nil
	}

	// Create dispatcher with multiple valid events
	event1 := Event("event1")
	event2 := Event("event2")
	ed := NewEventDispatcher[testEventArgs](event1, event2)

	// Register same handler for different events - should succeed both times
	err := ed.AddHandler(context.Background(), event1, handler)
	require.NoError(t, err)

	err = ed.AddHandler(context.Background(), event2, handler)
	require.NoError(t, err)

	// Verify both events have the handler registered
	ed.mu.RLock()
	handler1Count := len(ed.handlers[event1])
	handler2Count := len(ed.handlers[event2])
	ed.mu.RUnlock()

	require.Equal(t, 1, handler1Count, "Event1 should have 1 handler")
	require.Equal(t, 1, handler2Count, "Event2 should have 1 handler")

	// Both events should trigger the handler
	err = ed.RaiseEvent(context.Background(), event1, testEventArgs{})
	require.NoError(t, err)
	require.Equal(t, 1, callCount, "Handler should be called once for event1")

	err = ed.RaiseEvent(context.Background(), event2, testEventArgs{})
	require.NoError(t, err)
	require.Equal(t, 2, callCount, "Handler should be called again for event2")
}

func Test_Duplicate_Handler_Detection_Different_Handlers(t *testing.T) {
	// Different handlers for the same event should not trigger duplicate detection
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

	ed := NewEventDispatcher[testEventArgs](testEvent)

	// Register two different handlers for the same event
	err := ed.AddHandler(context.Background(), testEvent, handler1)
	require.NoError(t, err)

	err = ed.AddHandler(context.Background(), testEvent, handler2)
	require.NoError(t, err)

	// Verify both handlers are registered
	ed.mu.RLock()
	handlerCount := len(ed.handlers[testEvent])
	ed.mu.RUnlock()
	require.Equal(t, 2, handlerCount, "Should have 2 different handlers")

	// Both handlers should be called when event is raised
	err = ed.RaiseEvent(context.Background(), testEvent, testEventArgs{})
	require.NoError(t, err)
	require.Equal(t, 1, callCount1, "Handler1 should be called")
	require.Equal(t, 1, callCount2, "Handler2 should be called")
}

func Test_Duplicate_Handler_Detection_Multiple_Duplicates(t *testing.T) {
	callCount := 0
	handler := func(ctx context.Context, args testEventArgs) error {
		callCount++
		return nil
	}

	ed := NewEventDispatcher[testEventArgs](testEvent)

	// First registration
	err := ed.AddHandler(context.Background(), testEvent, handler)
	require.NoError(t, err)

	// Multiple duplicate registrations
	for i := 0; i < 3; i++ {
		err = ed.AddHandler(context.Background(), testEvent, handler)
		require.NoError(t, err)
	}

	// Verify only one handler is registered
	ed.mu.RLock()
	handlerCount := len(ed.handlers[testEvent])
	ed.mu.RUnlock()
	require.Equal(t, 1, handlerCount, "Should still have only 1 handler after multiple duplicate attempts")

	// Handler should only be called once
	err = ed.RaiseEvent(context.Background(), testEvent, testEventArgs{})
	require.NoError(t, err)
	require.Equal(t, 1, callCount, "Handler should be called only once despite multiple registration attempts")
}

// Test_Workflow_Context_Handler_Cleanup validates that event handlers are properly cleaned up
// when using a context hierarchy similar to azd workflows:
// - Root context uses context.WithoutCancel (for singletons - never cancelled)
// - Step contexts are cancellable children (cancelled after each workflow step)
// - Handlers registered with step contexts should be cleaned up when step is cancelled
// This test validates the fix for https://github.com/Azure/azure-dev/issues/6530
func Test_Workflow_Context_Handler_Cleanup(t *testing.T) {
	t.Run("HandlersCleanedUpWhenStepContextCancelled", func(t *testing.T) {
		ed := NewEventDispatcher[testEventArgs](testEvent)

		// Simulate root context that uses WithoutCancel (like singletons)
		rootCtx := context.Background()
		nonCancellableRoot := context.WithoutCancel(rootCtx)

		// Simulate workflow step 1 - create cancellable child context
		step1Ctx, cancelStep1 := context.WithCancel(nonCancellableRoot)

		// Register handler with step 1 context (like actions do)
		step1CallCount := 0
		step1Handler := func(ctx context.Context, args testEventArgs) error {
			step1CallCount++
			return nil
		}
		err := ed.AddHandler(step1Ctx, testEvent, step1Handler)
		require.NoError(t, err)

		// Verify handler is registered
		ed.mu.RLock()
		require.Equal(t, 1, len(ed.handlers[testEvent]), "Step 1 handler should be registered")
		ed.mu.RUnlock()

		// Raise event with step context - handler should be called
		err = ed.RaiseEvent(step1Ctx, testEvent, testEventArgs{})
		require.NoError(t, err)
		require.Equal(t, 1, step1CallCount, "Step 1 handler should be called")

		// Cancel step 1 context (simulating workflow step completion)
		cancelStep1()

		// Give cleanup goroutine time to process
		time.Sleep(50 * time.Millisecond)

		// Verify handler was removed
		ed.mu.RLock()
		require.Equal(t, 0, len(ed.handlers[testEvent]), "Step 1 handler should be cleaned up after cancel")
		ed.mu.RUnlock()

		// Raise event again - handler should NOT be called
		step1CallCount = 0
		err = ed.RaiseEvent(context.Background(), testEvent, testEventArgs{})
		require.NoError(t, err)
		require.Equal(t, 0, step1CallCount, "Step 1 handler should not be called after cleanup")
	})

	t.Run("MultipleStepsWithHandlerCleanup", func(t *testing.T) {
		ed := NewEventDispatcher[testEventArgs](testEvent)

		// Simulate root context with WithoutCancel
		rootCtx := context.Background()
		nonCancellableRoot := context.WithoutCancel(rootCtx)

		// Step 1: provision
		step1Ctx, cancelStep1 := context.WithCancel(nonCancellableRoot)
		step1CallCount := 0
		step1Handler := func(ctx context.Context, args testEventArgs) error {
			step1CallCount++
			return nil
		}
		err := ed.AddHandler(step1Ctx, testEvent, step1Handler)
		require.NoError(t, err)

		// Verify step 1 handler works
		err = ed.RaiseEvent(step1Ctx, testEvent, testEventArgs{})
		require.NoError(t, err)
		require.Equal(t, 1, step1CallCount)

		// Complete step 1
		cancelStep1()
		time.Sleep(50 * time.Millisecond)

		// Verify step 1 handler cleaned up
		ed.mu.RLock()
		require.Equal(t, 0, len(ed.handlers[testEvent]), "Step 1 handler should be cleaned up")
		ed.mu.RUnlock()

		// Step 2: deploy (fresh context, same root)
		step2Ctx, cancelStep2 := context.WithCancel(nonCancellableRoot)
		step2CallCount := 0
		step2Handler := func(ctx context.Context, args testEventArgs) error {
			step2CallCount++
			return nil
		}
		err = ed.AddHandler(step2Ctx, testEvent, step2Handler)
		require.NoError(t, err)

		// Verify step 2 handler works
		err = ed.RaiseEvent(step2Ctx, testEvent, testEventArgs{})
		require.NoError(t, err)
		require.Equal(t, 1, step2CallCount)
		require.Equal(t, 1, step1CallCount, "Step 1 handler should not be called again")

		// Complete step 2
		cancelStep2()
		time.Sleep(50 * time.Millisecond)

		// Verify step 2 handler cleaned up
		ed.mu.RLock()
		require.Equal(t, 0, len(ed.handlers[testEvent]), "Step 2 handler should be cleaned up")
		ed.mu.RUnlock()
	})

	t.Run("NonCancellableContextHandlerNotCleanedUp", func(t *testing.T) {
		ed := NewEventDispatcher[testEventArgs](testEvent)

		// Register handler with non-cancellable context (simulating incorrect usage)
		nonCancellableCtx := context.WithoutCancel(context.Background())
		callCount := 0
		handler := func(ctx context.Context, args testEventArgs) error {
			callCount++
			return nil
		}
		err := ed.AddHandler(nonCancellableCtx, testEvent, handler)
		require.NoError(t, err)

		// Handler should be registered
		ed.mu.RLock()
		require.Equal(t, 1, len(ed.handlers[testEvent]))
		ed.mu.RUnlock()

		// Wait a bit - handler should NOT be cleaned up since context is non-cancellable
		time.Sleep(50 * time.Millisecond)

		// Handler should still be registered
		ed.mu.RLock()
		require.Equal(t, 1, len(ed.handlers[testEvent]), "Handler with non-cancellable context should persist")
		ed.mu.RUnlock()

		// Handler should still be called
		err = ed.RaiseEvent(context.Background(), testEvent, testEventArgs{})
		require.NoError(t, err)
		require.Equal(t, 1, callCount)
	})
}

type testEventArgs struct{}

const testEvent Event = "test"
