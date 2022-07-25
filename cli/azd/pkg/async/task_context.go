package async

import (
	"fmt"
)

type TaskRunFunc[R any] func(asyncContext *TaskContext[R])

type TaskContext[R any] struct {
	task   *Task[R]
	error  error
	result R
}

func (c *TaskContext[R]) SetError(err error) {
	c.error = err
}

func (c *TaskContext[R]) SetResult(result R) {
	c.result = result
}

type TaskWithProgressRunFunc[R any, P any] func(asyncContext *TaskContextWithProgress[R, P])

type TaskContextWithProgress[R any, P any] struct {
	task *TaskWithProgress[R, P]
	TaskContext[R]
}

func (c *TaskContextWithProgress[R, P]) SetProgress(progress P) {
	c.task.progressChannel <- progress
}

type InteractiveTaskWithProgressRunFunc[R any, P any] func(asyncContext *InteractiveTaskContextWithProgress[R, P])

type InteractiveTaskContextWithProgress[R any, P any] struct {
	task *InteractiveTaskWithProgress[R, P]
	TaskContextWithProgress[R, P]
	interactive bool
}

func (c *InteractiveTaskContextWithProgress[R, P]) Interact(interactFn func() error) error {
	c.task.interactiveChannel <- true
	err := interactFn()
	if err != nil {
		c.SetError(fmt.Errorf("interaction error: %w", err))
	}
	c.task.interactiveChannel <- false

	return err
}
