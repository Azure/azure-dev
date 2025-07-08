package ext

import (
	"context"
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/stretchr/testify/require"
)

// TestEventDispatcher_PreservesErrorWithSuggestion tests that ErrorWithSuggestion is preserved
// when there's a single handler error, which is critical for PowerShell hook suggestion text.
func TestEventDispatcher_PreservesErrorWithSuggestion(t *testing.T) {
	dispatcher := NewEventDispatcher[string](Event("test"))

	// Create an ErrorWithSuggestion like PowerShell hooks do
	originalErr := &internal.ErrorWithSuggestion{
		Suggestion: "PowerShell 7 is not installed or not in the path. To install PowerShell 7, " +
			"visit https://learn.microsoft.com/powershell/scripting/install/installing-powershell",
		Err: errors.New("executable file not found in $PATH"),
	}

	// Register a handler that returns the ErrorWithSuggestion
	err := dispatcher.AddHandler(Event("test"), func(ctx context.Context, eventArgs string) error {
		return originalErr
	})
	require.NoError(t, err)

	// Raise the event
	resultErr := dispatcher.RaiseEvent(context.Background(), Event("test"), "test-args")
	require.Error(t, resultErr)

	// Verify that ErrorWithSuggestion is preserved
	var suggestionErr *internal.ErrorWithSuggestion
	require.True(t, errors.As(resultErr, &suggestionErr), "ErrorWithSuggestion should be preserved for single handler error")
	require.Equal(t, originalErr.Suggestion, suggestionErr.Suggestion)
}

// TestEventDispatcher_MultipleErrors tests that multiple errors are still joined as before
func TestEventDispatcher_MultipleErrors(t *testing.T) {
	dispatcher := NewEventDispatcher[string](Event("test"))

	// Register multiple handlers that return errors
	err := dispatcher.AddHandler(Event("test"), func(ctx context.Context, eventArgs string) error {
		return errors.New("error 1")
	})
	require.NoError(t, err)

	err = dispatcher.AddHandler(Event("test"), func(ctx context.Context, eventArgs string) error {
		return errors.New("error 2")
	})
	require.NoError(t, err)

	// Raise the event
	resultErr := dispatcher.RaiseEvent(context.Background(), Event("test"), "test-args")
	require.Error(t, resultErr)

	// Verify that multiple errors are joined
	require.Contains(t, resultErr.Error(), "error 1")
	require.Contains(t, resultErr.Error(), "error 2")
}
