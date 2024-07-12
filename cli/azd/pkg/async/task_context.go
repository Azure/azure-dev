package async

import "fmt"

// Task function definition
type TaskRunFunc[R comparable] func(taskContext *TaskContext[R])

// The context available to the executing Task
type TaskContext[R comparable] struct {
	task   *Task[R]
	error  error
	result R
}

// Creates a new Task context
func NewTaskContext[R comparable](task *Task[R]) *TaskContext[R] {
	return &TaskContext[R]{
		task: task,
	}
}

// Sets the specified error for the task
func (c *TaskContext[R]) SetError(err error) {
	if c.result != *new(R) {
		panic(fmt.Sprintf("Task result has already been set! Task cannot have both a result and an error.\n"+
			"Result: %v, New Error: %v", c.result, err))
	}

	if c.error != nil {
		panic(fmt.Sprintf(
			"Task error has already been set! Ensure your task error is only ever set one time.\n"+
				"Old Error: %v\nNew Error: %v", c.error, err))
	}

	c.error = err
}

// Sets the result of the Task
func (c *TaskContext[R]) SetResult(result R) {
	if c.error != nil {
		panic(fmt.Sprintf("Task error has already been set! Task cannot have both a result and an error.\n"+
			"Error: %v, New Result: %v", c.error, result))
	}

	if c.result != *new(R) {
		panic(fmt.Sprintf("Task result has already been set! Ensure your task result is only ever set one time.\n"+
			"Old Result: %v\nNew Result: %v", c.result, result))
	}

	c.result = result
}

// Task with progress function definition
type TaskWithProgressRunFunc[R comparable, P comparable] func(ctx *TaskContextWithProgress[R, P])

// The context available to the executing Task
type TaskContextWithProgress[R comparable, P comparable] struct {
	task *TaskWithProgress[R, P]
	TaskContext[R]
}

// Creates a new Task context with progress reporting
func NewTaskContextWithProgress[R comparable, P comparable](task *TaskWithProgress[R, P]) *TaskContextWithProgress[R, P] {
	innerTask := NewTaskContext(&task.Task)

	return &TaskContextWithProgress[R, P]{
		task:        task,
		TaskContext: *innerTask,
	}
}

// Write a new progress value to the underlying progress channel
func (c *TaskContextWithProgress[R, P]) SetProgress(progress P) {
	c.task.progressChannel <- progress
}
