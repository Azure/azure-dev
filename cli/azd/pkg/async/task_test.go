package async

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTaskWithResult(t *testing.T) {
	expectedResult := "result"

	task := NewTask(func(ctx *TaskContext[string]) {
		ctx.SetResult(expectedResult)
	})
	err := task.Run()
	require.NoError(t, err)

	actualResult, err := task.Await()

	require.Equal(t, expectedResult, actualResult)
	require.Nil(t, err)
}

func TestTaskWithAwait(t *testing.T) {
	expectedResult := "result"

	task := NewTask(func(ctx *TaskContext[string]) {
		ctx.SetResult(expectedResult)
	})
	err := task.Run()
	require.NoError(t, err)

	actualResult, err := task.Await()

	require.Equal(t, expectedResult, actualResult)
	require.Nil(t, err)
}

func TestTaskWithError(t *testing.T) {
	expectedError := errors.New("example error")

	task := NewTask(func(ctx *TaskContext[string]) {
		ctx.SetError(expectedError)
	})
	err := task.Run()
	require.NoError(t, err)

	actualResult, err := task.Await()

	require.Equal(t, "", actualResult)
	require.Equal(t, expectedError, err)
}

func TestTaskWithProgressWithResult(t *testing.T) {
	expectedResult := "result"
	progress := []string{}
	progressDone := make(chan bool)

	task := NewTaskWithProgress(func(ctx *TaskContextWithProgress[string, string]) {
		ctx.SetProgress("thing 1")
		ctx.SetProgress("thing 2")
		ctx.SetProgress("thing 3")
		ctx.SetResult(expectedResult)
	})

	go func() {
		for status := range task.Progress() {
			progress = append(progress, status)
		}
		progressDone <- true
	}()

	err := task.Run()
	require.NoError(t, err)

	actualResult, err := task.Await()
	<-progressDone

	require.Equal(t, expectedResult, actualResult)
	require.Nil(t, err)
	require.Equal(t, 3, len(progress))
	require.Equal(t, "thing 1", progress[0])
	require.Equal(t, "thing 2", progress[1])
	require.Equal(t, "thing 3", progress[2])
}

func TestTaskWithProgressWithError(t *testing.T) {
	expectedError := errors.New("example error")
	progress := []string{}
	progressDone := make(chan bool, 1)

	task := NewTaskWithProgress(func(ctx *TaskContextWithProgress[string, string]) {
		ctx.SetProgress("thing 1")
		ctx.SetProgress("thing 2")

		// Something bad happens but previous project goes through
		ctx.SetError(expectedError)
	})

	go func() {
		for status := range task.Progress() {
			progress = append(progress, status)
		}
		progressDone <- true
	}()

	err := task.Run()
	require.NoError(t, err)

	actualResult, err := task.Await()
	<-progressDone

	require.Equal(t, "", actualResult)
	require.Equal(t, expectedError, err)
	require.Equal(t, 2, len(progress))
	require.Equal(t, "thing 1", progress[0])
	require.Equal(t, "thing 2", progress[1])
}

func TestTaskCannotRunAgain(t *testing.T) {
	task := NewTask(func(ctx *TaskContext[string]) {
		ctx.SetResult("result")
	})

	err := task.Run()
	require.NoError(t, err)

	_, _ = task.Await()

	// Second run call should fail
	err = task.Run()
	require.Error(t, err)
}

func TestTaskStatusWithSuccess(t *testing.T) {
	task := NewTask(func(ctx *TaskContext[string]) {
		ctx.SetResult("result")
	})

	require.Equal(t, Created, task.Status())

	err := task.Run()
	require.NoError(t, err)
	require.Equal(t, Running, task.Status())

	_, _ = task.Await()
	require.Equal(t, RanToCompletion, task.Status())
}

func TestTaskStatusWithError(t *testing.T) {
	task := NewTask(func(ctx *TaskContext[string]) {
		ctx.SetError(errors.New("error"))
	})

	require.Equal(t, Created, task.Status())

	err := task.Run()
	require.NoError(t, err)
	require.Equal(t, Running, task.Status())

	_, _ = task.Await()
	require.Equal(t, Faulted, task.Status())
}
