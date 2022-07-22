package spin

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_Run(t *testing.T) {
	t.Run("Run executes runFn", func(t *testing.T) {
		var buf bytes.Buffer
		title := "Spinning"
		writer = io.Writer(&buf)
		spinner := New(title)
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
		spinner := New(title)
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
	spinner := New(title)

	_ = spinner.Start()

	message := "First update"
	spinner.Println(message)
	assert.Contains(t, buf.String(), message)

	message = "Second update"
	spinner.Println(message)
	assert.Contains(t, buf.String(), message)

	_ = spinner.Stop()
}
