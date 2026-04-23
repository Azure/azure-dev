// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- TaskList tests ---

func TestNewTaskList_nil_options(t *testing.T) {
	tl := NewTaskList(nil)
	require.NotNil(t, tl)
	assert.NotNil(t, tl.options)
	assert.Equal(t, 5, tl.options.MaxConcurrentAsync)
	assert.False(t, tl.options.ContinueOnError)
}

func TestNewTaskList_custom_options(t *testing.T) {
	tl := NewTaskList(&TaskListOptions{
		ContinueOnError:    true,
		MaxConcurrentAsync: 10,
	})
	require.NotNil(t, tl)
	assert.True(t, tl.options.ContinueOnError)
	assert.Equal(t, 10, tl.options.MaxConcurrentAsync)
}

func TestTaskList_AddTask(t *testing.T) {
	tl := NewTaskList(nil)

	result := tl.AddTask(TaskOptions{
		Title: "Task 1",
		Action: func(sp SetProgressFunc) (TaskState, error) {
			return Success, nil
		},
	})

	// AddTask should return the TaskList for chaining
	assert.Equal(t, tl, result)
	assert.Len(t, tl.allTasks, 1)
	assert.Equal(t, "Task 1", tl.allTasks[0].Title)
	assert.Equal(t, Pending, tl.allTasks[0].State)
}

func TestTaskList_AddTask_chaining(t *testing.T) {
	tl := NewTaskList(nil)
	action := func(sp SetProgressFunc) (TaskState, error) {
		return Success, nil
	}

	tl.AddTask(TaskOptions{Title: "A", Action: action}).
		AddTask(TaskOptions{Title: "B", Action: action}).
		AddTask(TaskOptions{Title: "C", Action: action})

	assert.Len(t, tl.allTasks, 3)
}

func TestTaskList_Render_pending(t *testing.T) {
	var buf bytes.Buffer
	printer := NewPrinter(&buf)

	tl := NewTaskList(nil)
	tl.allTasks = append(tl.allTasks, &Task{
		Title: "My pending task",
		State: Pending,
	})

	err := tl.Render(printer)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "My pending task")
}

func TestTaskList_Render_success_with_elapsed(t *testing.T) {
	var buf bytes.Buffer
	printer := NewPrinter(&buf)

	start := time.Now().Add(-5 * time.Second)
	end := time.Now()

	tl := NewTaskList(nil)
	tl.allTasks = append(tl.allTasks, &Task{
		Title:     "Completed task",
		State:     Success,
		startTime: &start,
		endTime:   &end,
	})

	err := tl.Render(printer)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "Completed task")
}

func TestTaskList_Render_error_with_description(t *testing.T) {
	var buf bytes.Buffer
	printer := NewPrinter(&buf)

	start := time.Now().Add(-2 * time.Second)
	end := time.Now()

	tl := NewTaskList(nil)
	tl.allTasks = append(tl.allTasks, &Task{
		Title:     "Failed task",
		State:     Error,
		Error:     errors.New("something broke"),
		startTime: &start,
		endTime:   &end,
	})

	err := tl.Render(printer)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "Failed task")
	assert.Contains(t, output, "something broke")
}

func TestTaskList_Render_running_with_progress(t *testing.T) {
	var buf bytes.Buffer
	printer := NewPrinter(&buf)

	start := time.Now().Add(-1 * time.Second)
	tl := NewTaskList(nil)
	tl.allTasks = append(tl.allTasks, &Task{
		Title:     "Running task",
		State:     Running,
		progress:  "50%",
		startTime: &start,
	})

	err := tl.Render(printer)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "Running task")
	assert.Contains(t, output, "50%")
}

func TestTaskList_Render_skipped_with_and_without_error(t *testing.T) {
	var buf bytes.Buffer
	printer := NewPrinter(&buf)

	tl := NewTaskList(nil)
	tl.allTasks = append(tl.allTasks,
		&Task{Title: "Skip no error", State: Skipped},
		&Task{
			Title: "Skip with error",
			State: Skipped,
			Error: errors.New("skipped reason"),
		},
	)

	err := tl.Render(printer)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "Skip no error")
	assert.Contains(t, output, "Skip with error")
	assert.Contains(t, output, "skipped reason")
}

func TestTaskList_Render_warning(t *testing.T) {
	var buf bytes.Buffer
	printer := NewPrinter(&buf)

	start := time.Now().Add(-3 * time.Second)
	end := time.Now()

	tl := NewTaskList(nil)
	tl.allTasks = append(tl.allTasks, &Task{
		Title:     "Warn task",
		State:     Warning,
		Error:     errors.New("partial failure"),
		startTime: &start,
		endTime:   &end,
	})

	err := tl.Render(printer)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "Warn task")
	assert.Contains(t, output, "partial failure")
}

func TestTaskList_Render_ordering(t *testing.T) {
	var buf bytes.Buffer
	printer := NewPrinter(&buf)

	tl := NewTaskList(nil)
	tl.allTasks = []*Task{
		{Title: "running-task", State: Running},
		{Title: "done-task", State: Success},
		{Title: "pending-task", State: Pending},
	}

	err := tl.Render(printer)
	require.NoError(t, err)

	output := buf.String()
	// Completed tasks render before running, running before pending
	doneIdx := bytes.Index([]byte(output), []byte("done-task"))
	runIdx := bytes.Index([]byte(output), []byte("running-task"))
	pendIdx := bytes.Index([]byte(output), []byte("pending-task"))

	assert.Less(t, doneIdx, runIdx,
		"done should appear before running")
	assert.Less(t, runIdx, pendIdx,
		"running should appear before pending")
}

func TestTaskList_WithCanvas(t *testing.T) {
	tl := NewTaskList(nil)
	var buf bytes.Buffer
	c := NewCanvas().WithWriter(&buf)
	defer c.Close()

	result := tl.WithCanvas(c)
	assert.Equal(t, tl, result)
}

// --- Spinner tests ---

func TestNewSpinner_defaults(t *testing.T) {
	s := NewSpinner(&SpinnerOptions{})
	require.NotNil(t, s)
	assert.Equal(t, "Loading...", s.text)
	assert.Len(t, s.options.Animation, 4)
	assert.Equal(
		t, 250*time.Millisecond, s.options.Interval,
	)
}

func TestNewSpinner_custom_text(t *testing.T) {
	s := NewSpinner(&SpinnerOptions{Text: "Please wait"})
	require.NotNil(t, s)
	assert.Equal(t, "Please wait", s.text)
}

func TestSpinner_UpdateText(t *testing.T) {
	s := NewSpinner(&SpinnerOptions{})
	s.UpdateText("new text")
	assert.Equal(t, "new text", s.text)
}

func TestSpinner_Render(t *testing.T) {
	var buf bytes.Buffer
	printer := NewPrinter(&buf)

	s := NewSpinner(&SpinnerOptions{
		Text:      "Working...",
		Animation: []string{"|", "/", "-", "\\"},
	})

	err := s.Render(printer)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "Working...")
}

func TestSpinner_Render_cycles_animation(t *testing.T) {
	s := NewSpinner(&SpinnerOptions{
		Animation: []string{"a", "b", "c"},
	})

	// Render multiple times to cycle through animation
	for range 3 {
		var buf bytes.Buffer
		printer := NewPrinter(&buf)
		err := s.Render(printer)
		require.NoError(t, err)
	}

	// After 3 renders, index should wrap back to 0
	assert.Equal(t, 0, s.animationIndex)
}

func TestSpinner_Render_clear_returns_nil(t *testing.T) {
	var buf bytes.Buffer
	printer := NewPrinter(&buf)

	s := NewSpinner(&SpinnerOptions{})
	s.clear = true

	err := s.Render(printer)
	require.NoError(t, err)
	assert.Empty(t, buf.String())
}

func TestSpinner_WithCanvas(t *testing.T) {
	s := NewSpinner(&SpinnerOptions{})
	var buf bytes.Buffer
	c := NewCanvas().WithWriter(&buf)
	defer c.Close()

	result := s.WithCanvas(c)
	assert.Equal(t, s, result)
}

func TestSpinner_WithCanvas_nil(t *testing.T) {
	s := NewSpinner(&SpinnerOptions{})
	result := s.WithCanvas(nil)
	assert.Equal(t, s, result)
	assert.Nil(t, s.canvas)
}
