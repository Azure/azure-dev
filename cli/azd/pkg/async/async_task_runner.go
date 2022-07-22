package async

type AsyncTaskRunFunc[R any] func(runner *AsyncTaskRunner[R])

type AsyncTaskRunner[R any] struct {
	task   *AsyncTask[R]
	error  error
	result R
}

func (r *AsyncTaskRunner[R]) SetError(err error) {
	r.error = err
}

func (r *AsyncTaskRunner[R]) SetResult(result R) {
	r.result = result
}

type AsyncTaskWithProgressRunFunc[R any, P any] func(runner *AsyncTaskWithProgressRunner[R, P])

type AsyncTaskWithProgressRunner[R any, P any] struct {
	task *AsyncTaskWithProgress[R, P]
	AsyncTaskRunner[R]
}

func (r *AsyncTaskWithProgressRunner[R, P]) SetProgress(progress P) {
	r.task.progressChannel <- progress
}
