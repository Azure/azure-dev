package async

type AsyncTask[R any] struct {
	isComplete    bool
	hasResult     bool
	result        R
	resultChannel chan R
	Error         error
}

func (t *AsyncTask[R]) Run(taskFn AsyncTaskRunFunc[R]) {
	go func() {
		context := AsyncTaskContext[R]{
			task: t,
		}

		taskFn(&context)
		t.complete(context.result, context.error)
	}()
}

func (t *AsyncTask[R]) IsCompleted() bool {
	return t.isComplete
}

func (t *AsyncTask[R]) Result() R {
	if t.hasResult {
		return t.result
	}

	t.result = <-t.resultChannel
	t.hasResult = true

	return t.result
}

func (t *AsyncTask[R]) complete(result R, err error) {
	t.Error = err
	if t.Error == nil {
		t.resultChannel <- result
	}

	t.isComplete = true
	close(t.resultChannel)
}

func NewAsyncTask[R any]() *AsyncTask[R] {
	return &AsyncTask[R]{
		resultChannel: make(chan R, 1),
	}
}

func RunTask[R any](taskFn AsyncTaskRunFunc[R]) *AsyncTask[R] {
	task := NewAsyncTask[R]()
	task.Run(taskFn)

	return task
}

type AsyncTaskWithProgress[R any, P any] struct {
	AsyncTask[R]
	progressChannel chan P
}

func (t *AsyncTaskWithProgress[R, P]) Progress() <-chan P {
	return t.progressChannel
}

func (t *AsyncTaskWithProgress[R, P]) Run(taskFn AsyncTaskWithProgressRunFunc[R, P]) {
	go func() {
		context := AsyncTaskContextWithProgress[R, P]{
			task: t,
		}

		taskFn(&context)
		t.complete(context.result, context.error)
		close(t.progressChannel)
	}()
}

func NewAsyncTaskWithProgress[R any, P any]() *AsyncTaskWithProgress[R, P] {
	return &AsyncTaskWithProgress[R, P]{
		AsyncTask: AsyncTask[R]{
			resultChannel: make(chan R, 1),
		},
		progressChannel: make(chan P),
	}
}

func RunTaskWithProgress[R any, P any](runFn AsyncTaskWithProgressRunFunc[R, P]) *AsyncTaskWithProgress[R, P] {
	task := NewAsyncTaskWithProgress[R, P]()
	task.Run(runFn)

	return task
}
