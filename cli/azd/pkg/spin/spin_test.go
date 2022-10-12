package spin

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_Run(t *testing.T) {
	t.Run("Run executes runFn", func(t *testing.T) {
		var buf bytes.Buffer
		title := "Spinning"
		spinner := NewSpinner(&buf, title)
		hasRun := false

		err := spinner.Run(func() error {
			hasRun = true
			return nil
		})
		assert.True(t, hasRun)
		assert.Nil(t, err)
	})

	t.Run("Run returns err if runFn errs", func(t *testing.T) {
		var buf bytes.Buffer
		title := "Spinning"
		spinner := NewSpinner(&buf, title)
		hasRun := false

		err := spinner.Run(func() error {
			hasRun = true
			return errors.New("oh no")
		})
		assert.True(t, hasRun)
		assert.Error(t, err)
	})
}

func Test_Println(t *testing.T) {
	var buf bytes.Buffer

	title := "Spinning"
	spinner := NewSpinner(&buf, title)

	spinner.Start()

	message := "First update"
	spinner.Println(message)
	assert.Contains(t, buf.String(), message)

	message = "Second update"
	spinner.Println(message)
	assert.Contains(t, buf.String(), message)

	spinner.Stop()
}

func Test_GetAndSetSpinner(t *testing.T) {
	rootContext := context.Background()
	spinner := GetSpinner(rootContext)

	// Spinner hasn't been set yet
	require.Nil(t, spinner)

	spinner = NewSpinner(io.Discard, "Test")
	newContext := WithSpinner(rootContext, spinner)

	existingOnNewContext := GetSpinner(newContext)
	existingOnRootContext := GetSpinner(rootContext)
	require.Same(t, spinner, existingOnNewContext)

	// Still nil on the root context
	require.Nil(t, existingOnRootContext)
}

func Test_GetOrCreate(t *testing.T) {
	t.Run("New", func(t *testing.T) {
		rootContext := context.Background()
		spinner, newContext := GetOrCreateSpinner(rootContext, io.Discard, "New")

		require.NotNil(t, spinner)
		require.NotSame(t, rootContext, newContext)
	})

	t.Run("Existing", func(t *testing.T) {
		// Create new context and manually add Spinner to context.
		rootContext := context.Background()
		existingSpinner := NewSpinner(io.Discard, "Existing")
		existingContext := WithSpinner(rootContext, existingSpinner)

		// Get spinner or create spinner from context
		newSpinner, newContext := GetOrCreateSpinner(existingContext, io.Discard, "Test")

		require.Same(t, existingSpinner, newSpinner)
		require.Same(t, existingContext, newContext)
	})
}
