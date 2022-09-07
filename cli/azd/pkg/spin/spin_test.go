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
		writer = io.Writer(&buf)
		spinner := NewSpinner(title)
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
		writer = io.Writer(&buf)
		spinner := NewSpinner(title)
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
	writer = io.Writer(&buf)

	title := "Spinning"
	spinner := NewSpinner(title)

	spinner.Start()

	message := "First update"
	spinner.Println(message)
	assert.Contains(t, buf.String(), message)

	message = "Second update"
	spinner.Println(message)
	assert.Contains(t, buf.String(), message)

	spinner.Stop()
}

func Test_WithGetSpinner(t *testing.T) {
	rootContext := context.Background()
	spinner := GetSpinner(rootContext)

	// Spinner hasn't been set yet
	require.Nil(t, spinner)

	spinner = NewSpinner("Test")
	newContext := WithSpinner(rootContext, spinner)

	existingOnNewContext := GetSpinner(newContext)
	existingOnRootContext := GetSpinner(rootContext)
	require.Same(t, spinner, existingOnNewContext)

	// Still nil on the root context
	require.Nil(t, existingOnRootContext)
}
