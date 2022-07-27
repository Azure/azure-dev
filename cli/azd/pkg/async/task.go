package async

type Task[R any] struct {
	isComplete    bool
	hasResult     bool
	result        R
	resultChannel chan R
	Error         error
}

func (t *Task[R]) Run(taskFn TaskRunFunc[R]) {
	go func() {
		context := TaskContext[R]{
			task: t,
		}

		taskFn(&context)
		t.complete(context.result, context.error)
	}()
}

func (t *Task[R]) IsCompleted() bool {
	return t.isComplete
}

func (t *Task[R]) Result() R {
	if t.hasResult {
		return t.result
	}

	t.result = <-t.resultChannel
	t.hasResult = true

	return t.result
}

func (t *Task[R]) complete(result R, err error) {
	t.Error = err
	if t.Error == nil {
		t.resultChannel <- result
	}

	t.isComplete = true
	close(t.resultChannel)
}

func NewTask[R any]() *Task[R] {
	return &Task[R]{
		resultChannel: make(chan R, 1),
	}
}

func RunTask[R any](taskFn TaskRunFunc[R]) *Task[R] {
	task := NewTask[R]()
	task.Run(taskFn)

	return task
}

type TaskWithProgress[R any, P any] struct {
	Task[R]
	progressChannel chan P
}

func (t *TaskWithProgress[R, P]) Progress() <-chan P {
	return t.progressChannel
}

func (t *TaskWithProgress[R, P]) Run(taskFn TaskWithProgressRunFunc[R, P]) {
	go func() {
		context := TaskContextWithProgress[R, P]{
			task: t,
		}

		taskFn(&context)
		t.complete(context.result, context.error)
		close(t.progressChannel)
	}()
}

func NewTaskWithProgress[R any, P any]() *TaskWithProgress[R, P] {
	return &TaskWithProgress[R, P]{
		Task: Task[R]{
			resultChannel: make(chan R, 1),
		},
		progressChannel: make(chan P),
	}
}

func RunTaskWithProgress[R any, P any](runFn TaskWithProgressRunFunc[R, P]) *TaskWithProgress[R, P] {
	task := NewTaskWithProgress[R, P]()
	task.Run(runFn)

	return task
}
