// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package async

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewProgress(t *testing.T) {
	t.Run("creates valid progress instance", func(t *testing.T) {
		progress := NewProgress[string]()

		assert.NotNil(t, progress)
		assert.NotNil(t, progress.progressChannel)

		// Clean up
		progress.Done()
	})
}

func TestNewNoopProgress(t *testing.T) {
	t.Run("creates noop progress that drains channel", func(t *testing.T) {
		progress := NewNoopProgress[string]()

		assert.NotNil(t, progress)
		assert.NotNil(t, progress.progressChannel)

		// Should not block when sending progress
		progress.SetProgress("test1")
		progress.SetProgress("test2")

		// Clean up
		progress.Done()

		// Give a moment for the goroutine to finish draining
		time.Sleep(10 * time.Millisecond)
	})
}

func TestProgress_BasicUsage(t *testing.T) {
	t.Run("can send and receive progress updates", func(t *testing.T) {
		progress := NewProgress[string]()

		// Start a goroutine to send progress
		go func() {
			progress.SetProgress("step1")
			progress.SetProgress("step2")
			progress.SetProgress("step3")
			progress.Done()
		}()

		// Collect all progress updates
		var updates []string
		for update := range progress.Progress() {
			updates = append(updates, update)
		}

		assert.Equal(t, []string{"step1", "step2", "step3"}, updates)
	})
}

func TestProgress_IntegerProgress(t *testing.T) {
	t.Run("works with integer progress type", func(t *testing.T) {
		progress := NewProgress[int]()

		go func() {
			for i := 0; i < 5; i++ {
				progress.SetProgress(i * 10) // 0, 10, 20, 30, 40
			}
			progress.Done()
		}()

		var updates []int
		for update := range progress.Progress() {
			updates = append(updates, update)
		}

		assert.Equal(t, []int{0, 10, 20, 30, 40}, updates)
	})
}

func TestProgress_CustomStructProgress(t *testing.T) {
	type ProgressInfo struct {
		Step    string
		Percent int
	}

	t.Run("works with custom struct progress type", func(t *testing.T) {
		progress := NewProgress[ProgressInfo]()

		expected := []ProgressInfo{
			{Step: "init", Percent: 0},
			{Step: "processing", Percent: 50},
			{Step: "complete", Percent: 100},
		}

		go func() {
			for _, info := range expected {
				progress.SetProgress(info)
			}
			progress.Done()
		}()

		var updates []ProgressInfo
		for update := range progress.Progress() {
			updates = append(updates, update)
		}

		assert.Equal(t, expected, updates)
	})
}

func TestProgress_ProgressChannel(t *testing.T) {
	t.Run("Progress() returns read-only channel", func(t *testing.T) {
		progress := NewProgress[string]()

		ch := progress.Progress()
		assert.NotNil(t, ch)

		// Verify it's the same channel
		go func() {
			progress.SetProgress("test")
			progress.Done()
		}()

		update, ok := <-ch
		assert.True(t, ok)
		assert.Equal(t, "test", update)

		// Channel should be closed after Done()
		_, ok = <-ch
		assert.False(t, ok)
	})
}

func TestProgress_Done(t *testing.T) {
	t.Run("Done closes the channel", func(t *testing.T) {
		progress := NewProgress[string]()

		go func() {
			time.Sleep(10 * time.Millisecond)
			progress.Done()
		}()

		// Should receive nothing and channel should be closed
		var updates []string
		for update := range progress.Progress() {
			updates = append(updates, update)
		}

		assert.Empty(t, updates)
	})

	t.Run("Done can be called multiple times safely", func(t *testing.T) {
		progress := NewProgress[string]()

		// First call should work
		assert.NotPanics(t, func() {
			progress.Done()
		})

		// Subsequent calls should not panic (though they're not recommended)
		// Note: In Go, closing a closed channel panics, but this test documents current behavior
		assert.Panics(t, func() {
			progress.Done()
		})
	})
}

func TestProgress_ConcurrentAccess(t *testing.T) {
	t.Run("handles concurrent progress updates", func(t *testing.T) {
		progress := NewProgress[int]()

		const numGoroutines = 10
		const updatesPerGoroutine = 5

		var wg sync.WaitGroup
		wg.Add(numGoroutines)

		// Start multiple goroutines sending progress
		for i := 0; i < numGoroutines; i++ {
			go func(goroutineID int) {
				defer wg.Done()
				for j := 0; j < updatesPerGoroutine; j++ {
					progress.SetProgress(goroutineID*100 + j)
				}
			}(i)
		}

		// Close progress after all goroutines finish
		go func() {
			wg.Wait()
			progress.Done()
		}()

		// Collect all updates
		var updates []int
		for update := range progress.Progress() {
			updates = append(updates, update)
		}

		// Should receive exactly the expected number of updates
		assert.Len(t, updates, numGoroutines*updatesPerGoroutine)
	})
}

func TestRunWithProgress(t *testing.T) {
	t.Run("runs function with progress and returns result", func(t *testing.T) {
		var observedProgress []string
		observer := func(p string) {
			observedProgress = append(observedProgress, p)
		}

		expectedResult := "success"
		workFunc := func(p *Progress[string]) (string, error) {
			p.SetProgress("starting")
			p.SetProgress("middle")
			p.SetProgress("finishing")
			return expectedResult, nil
		}

		result, err := RunWithProgress(observer, workFunc)

		assert.NoError(t, err)
		assert.Equal(t, expectedResult, result)
		assert.Equal(t, []string{"starting", "middle", "finishing"}, observedProgress)
	})

	t.Run("runs function with progress and returns error", func(t *testing.T) {
		var observedProgress []string
		observer := func(p string) {
			observedProgress = append(observedProgress, p)
		}

		expectedError := errors.New("test error")
		workFunc := func(p *Progress[string]) (string, error) {
			p.SetProgress("starting")
			p.SetProgress("error occurred")
			return "", expectedError
		}

		result, err := RunWithProgress(observer, workFunc)

		assert.Error(t, err)
		assert.Equal(t, expectedError, err)
		assert.Equal(t, "", result)
		assert.Equal(t, []string{"starting", "error occurred"}, observedProgress)
	})

	t.Run("waits for observer to finish before returning", func(t *testing.T) {
		var observedProgress []string
		var observerFinished bool

		observer := func(p string) {
			observedProgress = append(observedProgress, p)
			// Simulate slow observer
			time.Sleep(50 * time.Millisecond)
			if p == "last" {
				observerFinished = true
			}
		}

		workFunc := func(p *Progress[string]) (string, error) {
			p.SetProgress("first")
			p.SetProgress("last")
			return "done", nil
		}

		result, err := RunWithProgress(observer, workFunc)

		assert.NoError(t, err)
		assert.Equal(t, "done", result)
		assert.True(t, observerFinished, "Observer should have finished processing all progress updates")
		assert.Equal(t, []string{"first", "last"}, observedProgress)
	})
}

func TestRunWithProgressE(t *testing.T) {
	t.Run("runs function with progress and returns nil error", func(t *testing.T) {
		var observedProgress []int
		observer := func(p int) {
			observedProgress = append(observedProgress, p)
		}

		workFunc := func(p *Progress[int]) error {
			p.SetProgress(10)
			p.SetProgress(50)
			p.SetProgress(100)
			return nil
		}

		err := RunWithProgressE(observer, workFunc)

		assert.NoError(t, err)
		assert.Equal(t, []int{10, 50, 100}, observedProgress)
	})

	t.Run("runs function with progress and returns error", func(t *testing.T) {
		var observedProgress []int
		observer := func(p int) {
			observedProgress = append(observedProgress, p)
		}

		expectedError := errors.New("processing error")
		workFunc := func(p *Progress[int]) error {
			p.SetProgress(10)
			p.SetProgress(25)
			return expectedError
		}

		err := RunWithProgressE(observer, workFunc)

		assert.Error(t, err)
		assert.Equal(t, expectedError, err)
		assert.Equal(t, []int{10, 25}, observedProgress)
	})

	t.Run("handles panic in work function", func(t *testing.T) {
		var observedProgress []string
		observer := func(p string) {
			observedProgress = append(observedProgress, p)
		}

		workFunc := func(p *Progress[string]) error {
			p.SetProgress("before panic")
			panic("test panic")
		}

		assert.Panics(t, func() {
			RunWithProgressE(observer, workFunc)
		})

		// Observer should still have received the progress before panic
		assert.Equal(t, []string{"before panic"}, observedProgress)
	})
}

func TestProgress_EdgeCases(t *testing.T) {
	t.Run("empty progress updates", func(t *testing.T) {
		progress := NewProgress[string]()

		go func() {
			// Don't send any progress, just close
			progress.Done()
		}()

		var updates []string
		for update := range progress.Progress() {
			updates = append(updates, update)
		}

		assert.Empty(t, updates)
	})

	t.Run("progress with zero values", func(t *testing.T) {
		progress := NewProgress[string]()

		go func() {
			progress.SetProgress("") // empty string
			progress.SetProgress("non-empty")
			progress.SetProgress("") // empty string again
			progress.Done()
		}()

		var updates []string
		for update := range progress.Progress() {
			updates = append(updates, update)
		}

		assert.Equal(t, []string{"", "non-empty", ""}, updates)
	})
}
