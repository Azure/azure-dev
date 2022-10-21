package async

import (
	"errors"
)

type Status string

const (
	Created         Status = "Created"
	Running         Status = "Running"
	Faulted         Status = "Faulted"
	RanToCompletion Status = "RanToCompletion"
)

// Task represents a long running async operation
type Task[R comparable] struct {
	hasResult     bool
	result        R
	resultChannel chan R
	error         error
	taskFn        TaskRunFunc[R]
	status        Status
}

// Creates a new instance of a Task
func NewTask[R comparable](taskFn TaskRunFunc[R]) *Task[R] {
	return &Task[R]{
		status:        Created,
		taskFn:        taskFn,
		resultChannel: make(chan R, 1),
	}
}

func (t *Task[R]) Status() Status {
	return t.status
}

func (t *Task[R]) initialize() error {
	switch t.status {
	case Running:
		return errors.New("Task is already running")
	case Faulted:
		return errors.New("Task is in a faulted state and has an error")
	case RanToCompletion:
		return errors.New("Task has already completed")
	}

	t.status = Running
	return nil
}

// Runs the specified taskFn as a go routine
func (t *Task[R]) Run() error {
	err := t.initialize()
	if err != nil {
		return err
	}

	go func() {
		context := NewTaskContext(t)

		t.status = Running
		t.taskFn(context)
		t.complete(context.result, context.error)
	}()

	return nil
}

// Waits for a result to become available and returns the task value
func (t *Task[R]) Result() R {
	if t.hasResult {
		return t.result
	}

	t.result = <-t.resultChannel
	t.hasResult = true

	return t.result
}

// Awaits the async execution and returns the result and error status
func (t *Task[R]) Await() (R, error) {
	result := t.Result()
	return result, t.error
}

// Marks the current task as complete and sets internal error/result state and cleans up channels
func (t *Task[R]) complete(result R, err error) {
	defer close(t.resultChannel)

	if err == nil {
		t.resultChannel <- result
		t.status = RanToCompletion
	} else {
		t.error = err
		t.status = Faulted
	}
}

// Creates and schedules the task function and returns a task instance that holds the future result
func RunTask[R comparable](taskFn TaskRunFunc[R]) *Task[R] {
	task := NewTask(taskFn)
	if err := task.Run(); err != nil {
		panic(err)
	}

	return task
}

// Represents an async Task operation that support progress reporting
type TaskWithProgress[R comparable, P comparable] struct {
	Task[R]
	progressChannel chan P
	taskFn          TaskWithProgressRunFunc[R, P]
}

// Creates a new Task instance with progress reporting
func NewTaskWithProgress[R comparable, P comparable](taskFn TaskWithProgressRunFunc[R, P]) *TaskWithProgress[R, P] {
	return &TaskWithProgress[R, P]{
		Task:            *NewTask[R](nil),
		taskFn:          taskFn,
		progressChannel: make(chan P),
	}
}

// Gets the go channel that represents the task progress
func (t *TaskWithProgress[R, P]) Progress() <-chan P {
	return t.progressChannel
}

// Runs the specified taskFn as a go routine
func (t *TaskWithProgress[R, P]) Run() error {
	err := t.initialize()
	if err != nil {
		return err
	}

	go func() {
		defer close(t.progressChannel)
		context := NewTaskContextWithProgress(t)

		t.taskFn(context)
		t.complete(context.result, context.error)
	}()

	return nil
}

// Creates and schedules the task function and returns a task instance that holds the future result
func RunTaskWithProgress[R comparable, P comparable](taskFn TaskWithProgressRunFunc[R, P]) *TaskWithProgress[R, P] {
	task := NewTaskWithProgress(taskFn)
	if err := task.Run(); err != nil {
		panic(err)
	}

	return task
}

// Represents an async task operation that supports progress reporting and interactive console status
type InteractiveTaskWithProgress[R comparable, P comparable] struct {
	TaskWithProgress[R, P]
	interactiveChannel chan bool
	taskFn             InteractiveTaskWithProgressRunFunc[R, P]
}

// Creates a new Task instance with progress reporting and interactive console
func NewInteractiveTaskWithProgress[R comparable, P comparable](
	taskFn InteractiveTaskWithProgressRunFunc[R, P],
) *InteractiveTaskWithProgress[R, P] {
	return &InteractiveTaskWithProgress[R, P]{
		TaskWithProgress:   *NewTaskWithProgress[R, P](nil),
		taskFn:             taskFn,
		interactiveChannel: make(chan bool),
	}
}

// Runs the specified taskFn as a go routine
func (t *InteractiveTaskWithProgress[R, P]) Run() error {
	err := t.initialize()
	if err != nil {
		return err
	}

	go func() {
		defer close(t.progressChannel)
		defer close(t.interactiveChannel)
		context := NewInteractiveTaskContextWithProgress(t)

		t.taskFn(context)
		t.complete(context.result, context.error)
	}()

	return nil
}

// Gets the go channel that represents the task progress
func (t *InteractiveTaskWithProgress[R, P]) Interactive() <-chan bool {
	return t.interactiveChannel
}

// Creates and schedules the task function and returns a task instance that holds the future result
func RunInteractiveTaskWithProgress[R comparable, P comparable](
	taskFn InteractiveTaskWithProgressRunFunc[R, P],
) *InteractiveTaskWithProgress[R, P] {
	task := NewInteractiveTaskWithProgress(taskFn)
	if err := task.Run(); err != nil {
		panic(err)
	}

	return task
}
