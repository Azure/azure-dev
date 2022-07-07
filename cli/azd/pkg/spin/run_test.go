package spin

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/theckman/yacspin"
)

func Test_Run(t *testing.T) {
	t.Run("FinalFuncs Are Called", func(t *testing.T) {
		runCount := 0
		increment := func(s *yacspin.Spinner) {
			assert.NotNil(t, s, "spinner should be passed to final functions")
			runCount++
		}

		// FinalFuncs are called when the worker function succeeds.
		_ = Run("prefix", func() error { return nil }, increment, increment)
		assert.Equal(t, 2, runCount, "final functions should be called on success")

		// And FinalFuncs are also called when the worker function fails.
		runCount = 0
		_ = Run("prefix", func() error { return errors.New("oh no") }, increment, increment)
		assert.Equal(t, 2, runCount, "final functions should be called on error")
	})
}
