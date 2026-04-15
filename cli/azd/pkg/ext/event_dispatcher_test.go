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

	ctx, cancel := context.WithCancel(context.Background())
	err := ed.AddHandler(ctx, testEvent, handler)
	require.NoError(t, err)

	// Cancel context to remove handler
	cancel()

	require.Eventually(t, func() bool {
		ed.mu.RLock()
		defer ed.mu.RUnlock()
		return len(ed.handlers[testEvent]) == 0
	}, time.Second, 10*time.Millisecond, "Handler should be removed after context cancel")

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

	// Handler should not be called because context was already cancelled
	err = ed.RaiseEvent(context.Background(), testEvent, testEventArgs{})
	require.NoError(t, err)
	require.Equal(t, 0, callCount, "Handler should not be called with already cancelled context")

	// Verify handler was removed from internal map
	require.Eventually(t, func() bool {
		ed.mu.RLock()
		defer ed.mu.RUnlock()
		return len(ed.handlers[testEvent]) == 0
	}, time.Second, 10*time.Millisecond, "Handler should be automatically removed for already cancelled context")
}

func Test_Same_Handler_Registered_Twice(t *testing.T) {
	callCount := 0
	handler := func(ctx context.Context, args testEventArgs) error {
		callCount++
		return nil
	}

	ed := NewEventDispatcher[testEventArgs](testEvent)

	// Both registrations should succeed
	err := ed.AddHandler(context.Background(), testEvent, handler)
	require.NoError(t, err)

	err = ed.AddHandler(context.Background(), testEvent, handler)
	require.NoError(t, err)

	// Both registrations are kept — each AddHandler call is a distinct registration
	ed.mu.RLock()
	handlerCount := len(ed.handlers[testEvent])
	ed.mu.RUnlock()
	require.Equal(t, 2, handlerCount, "Both registrations should be kept")

	// Handler is called once per registration
	err = ed.RaiseEvent(context.Background(), testEvent, testEventArgs{})
	require.NoError(t, err)
	require.Equal(t, 2, callCount, "Handler should be called once per registration")
}

func Test_Same_Handler_Different_Events(t *testing.T) {
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

func Test_Different_Handlers_Same_Event(t *testing.T) {
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

func Test_Same_Handler_Multiple_Registrations(t *testing.T) {
	callCount := 0
	handler := func(ctx context.Context, args testEventArgs) error {
		callCount++
		return nil
	}

	ed := NewEventDispatcher[testEventArgs](testEvent)

	// First registration
	err := ed.AddHandler(context.Background(), testEvent, handler)
	require.NoError(t, err)

	// Multiple registrations of the same handler
	for range 3 {
		err = ed.AddHandler(context.Background(), testEvent, handler)
		require.NoError(t, err)
	}

	// All registrations are kept
	ed.mu.RLock()
	handlerCount := len(ed.handlers[testEvent])
	ed.mu.RUnlock()
	require.Equal(t, 4, handlerCount, "All registrations should be kept")

	// Handler is called once per registration
	err = ed.RaiseEvent(context.Background(), testEvent, testEventArgs{})
	require.NoError(t, err)
	require.Equal(t, 4, callCount, "Handler should be called once per registration")
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
		ed.mu.RLock()
		require.Equal(t, 1, len(ed.handlers[testEvent]), "Step 1 handler should be registered")
		ed.mu.RUnlock()

		// Raise event with step context - handler should be called
		err = ed.RaiseEvent(step1Ctx, testEvent, testEventArgs{})
		require.NoError(t, err)
		require.Equal(t, 1, step1CallCount, "Step 1 handler should be called")

		// Cancel step 1 context (simulating workflow step completion)
		cancelStep1()

		// Verify handler was removed
		require.Eventually(t, func() bool {
			ed.mu.RLock()
			defer ed.mu.RUnlock()
			return len(ed.handlers[testEvent]) == 0
		}, time.Second, 10*time.Millisecond, "Step 1 handler should be cleaned up after cancel")

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
		err = ed.RaiseEvent(step1Ctx, testEvent, testEventArgs{})
		require.NoError(t, err)
		require.Equal(t, 1, step1CallCount)

		// Complete step 1
		cancelStep1()

		// Verify step 1 handler cleaned up
		require.Eventually(t, func() bool {
			ed.mu.RLock()
			defer ed.mu.RUnlock()
			return len(ed.handlers[testEvent]) == 0
		}, time.Second, 10*time.Millisecond, "Step 1 handler should be cleaned up")

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

		// Verify step 2 handler cleaned up
		require.Eventually(t, func() bool {
			ed.mu.RLock()
			defer ed.mu.RUnlock()
			return len(ed.handlers[testEvent]) == 0
		}, time.Second, 10*time.Millisecond, "Step 2 handler should be cleaned up")
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

		// Handler should still be registered (non-cancellable context means no cleanup goroutine)
		ed.mu.RLock()
		require.Equal(t, 1, len(ed.handlers[testEvent]), "Handler with non-cancellable context should persist")
		ed.mu.RUnlock()

		// Handler should still be called
		err = ed.RaiseEvent(context.Background(), testEvent, testEventArgs{})
		require.NoError(t, err)
		require.Equal(t, 1, callCount)
	})
}

// Test_Closures_From_Same_Literal validates that closures created from the same function literal
// can all be registered for the same event (e.g., multiple extensions registering via
// createProjectEventHandler).
func Test_Closures_From_Same_Literal(t *testing.T) {
	type extensionInfo struct {
		name string
	}

	// createHandler simulates createProjectEventHandler: returns closures from the same literal
	createHandler := func(ext *extensionInfo, eventName string) EventHandlerFn[testEventArgs] {
		return func(ctx context.Context, args testEventArgs) error {
			_ = ext.name
			_ = eventName
			return nil
		}
	}

	ed := NewEventDispatcher[testEventArgs](testEvent)

	ext1 := &extensionInfo{name: "microsoft.azd.demo"}
	ext2 := &extensionInfo{name: "azure.ai.agents"}

	// Register handlers from two "extensions" for the same event
	h1 := createHandler(ext1, "preprovision")
	h2 := createHandler(ext2, "preprovision")

	err := ed.AddHandler(context.Background(), testEvent, h1)
	require.NoError(t, err)

	err = ed.AddHandler(context.Background(), testEvent, h2)
	require.NoError(t, err)
	ed.mu.RLock()
	handlerCount := len(ed.handlers[testEvent])
	ed.mu.RUnlock()
	require.Equal(t, 2, handlerCount, "Both extension handlers must be registered")
}

// Test_Context_Cleanup_With_Same_Literal_Closures validates that context-based cleanup
// removes only the correct handler when multiple closures from the same literal are registered.
func Test_Context_Cleanup_With_Same_Literal_Closures(t *testing.T) {
	createHandler := func(name string, counter *int) EventHandlerFn[testEventArgs] {
		return func(ctx context.Context, args testEventArgs) error {
			(*counter)++
			return nil
		}
	}

	ed := NewEventDispatcher[testEventArgs](testEvent)

	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2 := context.Background()

	counter1, counter2 := 0, 0
	h1 := createHandler("ext1", &counter1)
	h2 := createHandler("ext2", &counter2)

	err := ed.AddHandler(ctx1, testEvent, h1)
	require.NoError(t, err)
	err = ed.AddHandler(ctx2, testEvent, h2)
	require.NoError(t, err)

	// Both should fire
	err = ed.RaiseEvent(context.Background(), testEvent, testEventArgs{})
	require.NoError(t, err)
	require.Equal(t, 1, counter1)
	require.Equal(t, 1, counter2)

	// Cancel ext1's context — only ext1's handler should be removed
	cancel1()

	require.Eventually(t, func() bool {
		counter1, counter2 = 0, 0
		err = ed.RaiseEvent(context.Background(), testEvent, testEventArgs{})
		require.NoError(t, err)
		return counter1 == 0 && counter2 == 1
	}, time.Second, 10*time.Millisecond,
		"ext1 handler should be removed after context cancel while ext2 remains active")
}

// Test_Workflow_Step_Race_No_Duplicate_Execution simulates the race window during
// workflow commands (e.g., azd up) where step 1's context is cancelled but the cleanup
// goroutine hasn't run yet when step 2 registers the same hooks. Handlers from step 1
// must not fire during step 2, even before async cleanup removes them.
// Regression test for https://github.com/Azure/azure-dev/issues/6011.
func Test_Workflow_Step_Race_No_Duplicate_Execution(t *testing.T) {
	ed := NewEventDispatcher[testEventArgs](testEvent)

	callCount := 0
	createHandler := func() EventHandlerFn[testEventArgs] {
		return func(ctx context.Context, args testEventArgs) error {
			callCount++
			return nil
		}
	}

	// Step 1: register handler with cancellable context
	step1Ctx, cancelStep1 := context.WithCancel(context.Background())
	err := ed.AddHandler(step1Ctx, testEvent, createHandler())
	require.NoError(t, err)

	// Step 1 completes — cancel context (cleanup goroutine queued but may or may not have run)
	cancelStep1()

	// Step 2 starts immediately — registers the same hook again
	step2Ctx := t.Context()
	err = ed.AddHandler(step2Ctx, testEvent, createHandler())
	require.NoError(t, err)

	// Raise event — only step 2's handler should fire since step 1's context is cancelled.
	// This is deterministic regardless of whether the cleanup goroutine has run yet,
	// because RaiseEvent skips handlers with cancelled contexts.
	err = ed.RaiseEvent(context.Background(), testEvent, testEventArgs{})
	require.NoError(t, err)
	require.Equal(t, 1, callCount, "Only step 2's handler should fire; step 1's context is cancelled")
}

type testEventArgs struct{}

const testEvent Event = "test"
