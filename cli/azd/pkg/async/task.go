package async

// Task represents a long running async operation
type Task[R comparable] struct {
	isComplete    bool
	hasResult     bool
	result        R
	resultChannel chan R
	Error         error
}

// Runs the specified taskFn as a go routine
func (t *Task[R]) Run(taskFn TaskRunFunc[R]) {
	go func() {
		context := NewTaskContext(t)

		taskFn(context)
		t.complete(context.result, context.error)
	}()
}

// Checks whether or not the Task has completed
func (t *Task[R]) IsCompleted() bool {
	return t.isComplete
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
	return result, t.Error
}

// Marks the current task as complete and sets internal error/result state and cleans up channels
func (t *Task[R]) complete(result R, err error) {
	t.Error = err
	if t.Error == nil {
		t.resultChannel <- result
	}

	t.isComplete = true
	close(t.resultChannel)
}

// Creates a new instance of a Task
func NewTask[R comparable]() *Task[R] {
	return &Task[R]{
		resultChannel: make(chan R, 1),
	}
}

// Creates and schedules the task function and returns a task instance that holds the future result
func RunTask[R comparable](taskFn TaskRunFunc[R]) *Task[R] {
	task := NewTask[R]()
	task.Run(taskFn)

	return task
}

// Represents an async Task operation that support progress reporting
type TaskWithProgress[R comparable, P comparable] struct {
	Task[R]
	progressChannel chan P
}

// Gets the go channel that represents the task progress
func (t *TaskWithProgress[R, P]) Progress() <-chan P {
	return t.progressChannel
}

// Runs the specified taskFn as a go routine
func (t *TaskWithProgress[R, P]) Run(taskFn TaskWithProgressRunFunc[R, P]) {
	go func() {
		context := NewTaskContextWithProgress(t)

		taskFn(context)
		t.complete(context.result, context.error)
		close(t.progressChannel)
	}()
}

// Creates a new Task instance with progress reporting
func NewTaskWithProgress[R comparable, P comparable]() *TaskWithProgress[R, P] {
	return &TaskWithProgress[R, P]{
		Task: Task[R]{
			resultChannel: make(chan R, 1),
		},
		progressChannel: make(chan P),
	}
}

// Creates and schedules the task function and returns a task instance that holds the future result
func RunTaskWithProgress[R comparable, P comparable](runFn TaskWithProgressRunFunc[R, P]) *TaskWithProgress[R, P] {
	task := NewTaskWithProgress[R, P]()
	task.Run(runFn)

	return task
}

// Represents an async task operation that supports progress reporting and interactive console status
type InteractiveTaskWithProgress[R comparable, P comparable] struct {
	TaskWithProgress[R, P]
	interactiveChannel chan bool
}

// Runs the specified taskFn as a go routine
func (t *InteractiveTaskWithProgress[R, P]) Run(taskFn InteractiveTaskWithProgressRunFunc[R, P]) {
	go func() {
		context := NewInteractiveTaskContextWithProgress(t)

		taskFn(context)
		t.complete(context.result, context.error)
		close(t.progressChannel)
		close(t.interactiveChannel)
	}()
}

// Gets the go channel that represents the task progress
func (t *InteractiveTaskWithProgress[R, P]) Interactive() <-chan bool {
	return t.interactiveChannel
}

// Creates a new Task instance with progress reporting and interactive console
func NewInteractiveTaskWithProgress[R comparable, P comparable]() *InteractiveTaskWithProgress[R, P] {
	return &InteractiveTaskWithProgress[R, P]{
		TaskWithProgress:   *NewTaskWithProgress[R, P](),
		interactiveChannel: make(chan bool),
	}
}

// Creates and schedules the task function and returns a task instance that holds the future result
func RunInteractiveTaskWithProgress[R comparable, P comparable](runFn InteractiveTaskWithProgressRunFunc[R, P]) *InteractiveTaskWithProgress[R, P] {
	task := NewInteractiveTaskWithProgress[R, P]()
	task.Run(runFn)

	return task
}
