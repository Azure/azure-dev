// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"dario.cat/mergo"
	"github.com/azure/azure-dev/cli/azd/pkg/common"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

// TaskListOptions represents the options for the TaskList component.
type TaskListOptions struct {
	ContinueOnError bool
	// The writer to use for output (default: os.Stdout)
	Writer             io.Writer
	MaxConcurrentAsync int
	SuccessStyle       string
	ErrorStyle         string
	WarningStyle       string
	RunningStyle       string
	SkippedStyle       string
	PendingStyle       string
}

var DefaultTaskListOptions TaskListOptions = TaskListOptions{
	ContinueOnError:    false,
	Writer:             os.Stdout,
	MaxConcurrentAsync: 5,

	SuccessStyle: output.WithSuccessFormat("(✔) Done "),
	ErrorStyle:   output.WithErrorFormat("(x) Error "),
	WarningStyle: output.WithWarningFormat("(!) Warning "),
	RunningStyle: output.WithHighLightFormat("(-) Running "),
	SkippedStyle: output.WithGrayFormat("(-) Skipped "),
	PendingStyle: output.WithGrayFormat("(o) Pending "),
}

// TaskList is a component for managing a list of tasks.
type TaskList struct {
	canvas    Canvas
	waitGroup sync.WaitGroup
	options   *TaskListOptions
	allTasks  []*Task
	syncTasks []*Task // Queue for synchronous tasks

	completed      int32
	syncMutex      sync.Mutex // Mutex to handle sync task queue safely
	errorMutex     sync.Mutex // Mutex to handle errors slice safely
	asyncSemaphore chan struct{}
	errors         []error
}

// TaskOptions represents the options for the Task component.
type TaskOptions struct {
	Title  string
	Action func(SetProgressFunc) (TaskState, error)
	Async  bool
}

// SetProgressFunc is a function that sets the progress of a task.
type SetProgressFunc func(string)

// Task represents a task in the task list.
type Task struct {
	Title     string
	Action    func(SetProgressFunc) (TaskState, error)
	State     TaskState
	Error     error
	progress  string
	startTime *time.Time
	endTime   *time.Time
}

// TaskState represents the state of a task.
type TaskState int

const (
	// Pending indicates a task has not started yet.
	Pending TaskState = iota
	// Running indicates a task is currently executing.
	Running
	// Skipped indicates a task was not executed.
	Skipped
	// Warning indicates a task completed with a non-fatal issue. If the task
	// returns an error with this state, it is displayed but not returned by Run.
	Warning
	// Error indicates a task failed.
	Error
	// Success indicates a task completed successfully.
	Success
)

// NewTaskList creates a new TaskList instance.
func NewTaskList(options *TaskListOptions) *TaskList {
	mergedOptions := TaskListOptions{}

	if options == nil {
		options = &TaskListOptions{}
	}

	if err := mergo.Merge(&mergedOptions, options, mergo.WithoutDereference); err != nil {
		panic(err)
	}

	if err := mergo.Merge(&mergedOptions, DefaultTaskListOptions, mergo.WithoutDereference); err != nil {
		panic(err)
	}

	return &TaskList{
		options:        &mergedOptions,
		waitGroup:      sync.WaitGroup{},
		allTasks:       []*Task{},
		syncTasks:      []*Task{},
		syncMutex:      sync.Mutex{},
		errorMutex:     sync.Mutex{},
		completed:      0,
		asyncSemaphore: make(chan struct{}, mergedOptions.MaxConcurrentAsync),
		errors:         []error{},
	}
}

// WithCanvas sets the canvas for the TaskList component.
func (t *TaskList) WithCanvas(canvas Canvas) Visual {
	t.canvas = canvas
	return t
}

// Run executes all async tasks first and then runs queued sync tasks sequentially.
func (t *TaskList) Run() error {
	if t.canvas == nil {
		t.canvas = NewCanvas(t).WithWriter(t.options.Writer)
	}

	if err := t.canvas.Run(); err != nil {
		return err
	}

	go func() {
		for {
			if t.isCompleted() {
				break
			}

			if err := t.canvas.Update(); err != nil {
				log.Println("Failed to update task list canvas:", err)
				return
			}

			time.Sleep(1 * time.Second)
		}
	}()

	// Wait for all async tasks to complete
	t.waitGroup.Wait()
	// Run sync tasks after async tasks are completed
	t.runSyncTasks()

	if err := t.canvas.Update(); err != nil {
		return err
	}

	if len(t.errors) > 0 {
		return errors.Join(t.errors...)
	}

	return nil
}

// AddTask adds a task to the task list and manages async/sync execution.
func (t *TaskList) AddTask(options TaskOptions) *TaskList {
	task := &Task{
		Title:  options.Title,
		Action: options.Action,
		State:  Pending,
	}

	// Differentiate between async and sync tasks
	if options.Async {
		t.addAsyncTask(task)
	} else {
		t.addSyncTask(task)
	}

	t.allTasks = append(t.allTasks, task)

	return t
}

// Render renders the task list.
func (t *TaskList) Render(printer Printer) error {
	otherTasks := []*Task{}
	runningTasks := []*Task{}
	pendingTasks := []*Task{}

	// Sort tasks for proper rendering order
	for _, task := range t.allTasks {
		switch task.State {
		case Running:
			runningTasks = append(runningTasks, task)
		case Pending:
			pendingTasks = append(pendingTasks, task)
		default:
			otherTasks = append(otherTasks, task)
		}
	}

	renderTasks := []*Task{}
	renderTasks = append(renderTasks, otherTasks...)
	renderTasks = append(renderTasks, runningTasks...)
	renderTasks = append(renderTasks, pendingTasks...)

	// Use terminal width minus 1 to prevent cursor wrap on exact-width lines (Windows)
	maxWidth := max(printer.Width()-1, 1)

	printer.Fprintln()

	for _, task := range renderTasks {
		endTime := time.Now()
		if task.endTime != nil {
			endTime = *task.endTime
		}

		var elapsedText string
		if task.startTime != nil {
			elapsed := endTime.Sub(*task.startTime)
			elapsedText = output.WithGrayFormat("(%s)", durationAsText(elapsed))
		}

		var statusDescription string
		if task.Error != nil {
			if detailedErr, ok := errors.AsType[*common.DetailedError](task.Error); ok {
				statusDescription = detailedErr.Description()
			} else {
				statusDescription = task.Error.Error()
			}
		}

		var progressText string
		if task.progress != "" {
			progressText = fmt.Sprintf(" (%s)", task.progress)
		}

		var line string
		switch task.State {
		case Pending:
			line = fmt.Sprintf("%s %s", output.WithGrayFormat(t.options.PendingStyle), task.Title)
		case Running:
			line = fmt.Sprintf(
				"%s %s%s %s",
				output.WithHighLightFormat(t.options.RunningStyle),
				task.Title,
				progressText,
				elapsedText,
			)
		case Warning:
			line = fmt.Sprintf(
				"%s %s %s %s",
				output.WithWarningFormat(t.options.WarningStyle),
				task.Title,
				elapsedText,
				output.WithWarningFormat("(%s)", statusDescription),
			)
		case Error:
			line = fmt.Sprintf(
				"%s %s %s %s",
				output.WithErrorFormat(t.options.ErrorStyle),
				task.Title,
				elapsedText,
				output.WithErrorFormat("(%s)", statusDescription),
			)
		case Success:
			line = fmt.Sprintf("%s %s  %s", output.WithSuccessFormat(t.options.SuccessStyle), task.Title, elapsedText)
		case Skipped:
			if statusDescription == "" {
				line = fmt.Sprintf(
					"%s %s",
					output.WithGrayFormat(t.options.SkippedStyle),
					task.Title,
				)
			} else {
				line = fmt.Sprintf(
					"%s %s %s",
					output.WithGrayFormat(t.options.SkippedStyle),
					task.Title,
					output.WithErrorFormat("(%s)", statusDescription),
				)
			}
		}

		printer.Fprintf("%s\n", TruncateVisible(line, maxWidth))
	}

	printer.Fprintln()

	return nil
}

// isCompleted checks if all async tasks are complete.
func (t *TaskList) isCompleted() bool {
	return int(atomic.LoadInt32(&t.completed)) == len(t.allTasks)
}

// runSyncTasks executes all synchronous tasks in order after async tasks are completed.
func (t *TaskList) runSyncTasks() {
	t.syncMutex.Lock()
	defer t.syncMutex.Unlock()

	for _, task := range t.syncTasks {
		if len(t.errors) > 0 && !t.options.ContinueOnError {
			task.State = Skipped
			atomic.AddInt32(&t.completed, 1)
			continue
		}

		task.startTime = new(time.Now())
		task.State = Running

		setProgress := func(progress string) {
			task.progress = progress
		}

		state, err := task.Action(setProgress)
		if shouldCollectTaskError(err, state) {
			t.errorMutex.Lock()
			t.errors = append(t.errors, err)
			t.errorMutex.Unlock()
		}

		task.endTime = new(time.Now())
		task.Error = err
		task.State = state

		atomic.AddInt32(&t.completed, 1)
	}
}

// addAsyncTask adds an asynchronous task and starts its execution in a goroutine.
func (t *TaskList) addAsyncTask(task *Task) {
	t.waitGroup.Go(func() {

		// Acquire a slot in the semaphore
		t.asyncSemaphore <- struct{}{}
		defer func() { <-t.asyncSemaphore }()

		task.startTime = new(time.Now())
		task.State = Running

		setProgress := func(progress string) {
			task.progress = progress
		}

		state, err := task.Action(setProgress)
		if shouldCollectTaskError(err, state) {
			t.errorMutex.Lock()
			t.errors = append(t.errors, err)
			t.errorMutex.Unlock()
		}

		task.endTime = new(time.Now())
		task.Error = err
		task.State = state

		atomic.AddInt32(&t.completed, 1)
	})
}

// addSyncTask queues a synchronous task for execution after async completion.
func (t *TaskList) addSyncTask(task *Task) {
	t.syncMutex.Lock()
	defer t.syncMutex.Unlock()

	t.syncTasks = append(t.syncTasks, task)
}

// shouldCollectTaskError determines whether a task error should be added to the error collection.
// Warning-state tasks with errors are excluded to allow following tasks to continue.
func shouldCollectTaskError(err error, state TaskState) bool {
	return err != nil && state != Warning
}

// DurationAsText provides a slightly nicer string representation of a duration
// when compared to default formatting in go, by spelling out the words hour,
// minute and second and providing some spacing and eliding the fractional component
// of the seconds part.
func durationAsText(d time.Duration) string {
	if d.Seconds() < 1.0 {
		return "less than a second"
	}

	var builder strings.Builder

	if (d / time.Hour) > 0 {
		writePart(&builder, fmt.Sprintf("%d", d/time.Hour), "hour")
		d = d - ((d / time.Hour) * time.Hour)
	}

	if (d / time.Minute) > 0 {
		writePart(&builder, fmt.Sprintf("%d", d/time.Minute), "minute")
		d = d - ((d / time.Minute) * time.Minute)
	}

	if (d / time.Second) > 0 {
		writePart(&builder, fmt.Sprintf("%d", d/time.Second), "second")
	}

	return builder.String()
}

// writePart writes the string [part] followed by [unit] into [builder], unless
// part is empty or the string "0". If part is "1", the [unit] string is suffixed
// with s. If builder is non empty, the written string is preceded by a space.
func writePart(builder *strings.Builder, part string, unit string) {
	if part != "" && part != "0" {
		if builder.Len() > 0 {
			builder.WriteByte(' ')
		}

		builder.WriteString(part)
		builder.WriteByte(' ')
		builder.WriteString(unit)
		if part != "1" {
			builder.WriteByte('s')
		}
	}
}
