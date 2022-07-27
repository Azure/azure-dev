package async

type TaskRunFunc[R any] func(ctx *TaskContext[R])

type TaskContext[R any] struct {
	task   *Task[R]
	error  error
	result R
}

func (r *TaskContext[R]) SetError(err error) {
	r.error = err
}

func (r *TaskContext[R]) SetResult(result R) {
	r.result = result
}

type TaskWithProgressRunFunc[R any, P any] func(ctx *TaskContextWithProgress[R, P])

type TaskContextWithProgress[R any, P any] struct {
	task *TaskWithProgress[R, P]
	TaskContext[R]
}

func (r *TaskContextWithProgress[R, P]) SetProgress(progress P) {
	r.task.progressChannel <- progress
}
