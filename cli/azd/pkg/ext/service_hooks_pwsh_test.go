package ext

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	azdexec "github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/powershell"
	"github.com/stretchr/testify/require"
)

// TestServiceHooks_PowerShellSuggestionPreserved tests the specific issue where
// PowerShell suggestion text was lost for service-level hooks due to event dispatcher error flattening.
func TestServiceHooks_PowerShellSuggestionPreserved(t *testing.T) {
	// Mock command runner (not used in this test since we mock the check function)
	mockRunner := &mockCommandRunner{}

	// Mock check function that simulates PowerShell not being installed
	mockCheckPath := func(options tools.ExecOptions) error {
		return exec.ErrNotFound
	}

	// Create PowerShell script with mock check
	script := powershell.NewPowershellScriptWithMockCheckPath(mockRunner, "/tmp", []string{}, mockCheckPath)

	// Execute script to get the original ErrorWithSuggestion
	_, originalErr := script.Execute(context.Background(), "/tmp/test.ps1", tools.ExecOptions{
		UserPwsh: "pwsh",
	})
	require.Error(t, originalErr)

	// Verify it's an ErrorWithSuggestion
	var suggestionErr *internal.ErrorWithSuggestion
	require.True(t, errors.As(originalErr, &suggestionErr), "Should be ErrorWithSuggestion")
	require.Contains(t, suggestionErr.Suggestion, "PowerShell 7 is not installed")

	// Simulate the error going through the event dispatcher (like service hooks do)
	dispatcher := NewEventDispatcher[string](Event("prepackage"))

	// Register a handler that returns the PowerShell error (simulating hook execution)
	err := dispatcher.AddHandler(Event("prepackage"), func(ctx context.Context, eventArgs string) error {
		// Wrap like HooksRunner does
		return fmt.Errorf("'prepackage' hook failed with exit code: '1', Path: '/tmp/test.ps1'. : %w", originalErr)
	})
	require.NoError(t, err)

	// Raise the event (simulating service lifecycle event triggering)
	eventErr := dispatcher.RaiseEvent(context.Background(), Event("prepackage"), "test")
	require.Error(t, eventErr)

	// This should now preserve the ErrorWithSuggestion (the fix)
	var finalSuggestionErr *internal.ErrorWithSuggestion
	require.True(t, errors.As(eventErr, &finalSuggestionErr),
		"ErrorWithSuggestion should be preserved through event dispatcher")
	require.Contains(t, finalSuggestionErr.Suggestion, "PowerShell 7 is not installed",
		"Suggestion text should be preserved")
}

type mockCommandRunner struct{}

func (m *mockCommandRunner) Run(ctx context.Context, args azdexec.RunArgs) (azdexec.RunResult, error) {
	return azdexec.RunResult{ExitCode: 1}, fmt.Errorf("executable file not found in $PATH")
}

func (m *mockCommandRunner) RunList(
	ctx context.Context, commands []string, args azdexec.RunArgs,
) (azdexec.RunResult, error) {
	return azdexec.RunResult{ExitCode: 1}, fmt.Errorf("executable file not found in $PATH")
}
