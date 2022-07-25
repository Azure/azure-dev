package async

type AsyncTaskRunFunc[R any] func(asyncContext *AsyncTaskContext[R])

type AsyncTaskContext[R any] struct {
	task   *AsyncTask[R]
	error  error
	result R
}

func (r *AsyncTaskContext[R]) SetError(err error) {
	r.error = err
}

func (r *AsyncTaskContext[R]) SetResult(result R) {
	r.result = result
}

type AsyncTaskWithProgressRunFunc[R any, P any] func(asyncContext *AsyncTaskContextWithProgress[R, P])

type AsyncTaskContextWithProgress[R any, P any] struct {
	task *AsyncTaskWithProgress[R, P]
	AsyncTaskContext[R]
}

func (r *AsyncTaskContextWithProgress[R, P]) SetProgress(progress P) {
	r.task.progressChannel <- progress
}
