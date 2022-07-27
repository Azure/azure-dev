package async

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTaskWithResult(t *testing.T) {
	expectedResult := "result"

	task := NewTask[string]()
	task.Run(func(ctx *TaskContext[string]) {
		time.Sleep(250 * time.Millisecond)
		ctx.SetResult(expectedResult)
	})

	actualResult := task.Result()

	require.Equal(t, expectedResult, actualResult)
	require.Nil(t, task.Error)
}

func TestTaskWithError(t *testing.T) {
	expectedError := errors.New("example error")

	task := NewTask[string]()
	task.Run(func(ctx *TaskContext[string]) {
		time.Sleep(250 * time.Millisecond)
		ctx.SetError(expectedError)
	})

	actualResult := task.Result()

	require.Equal(t, "", actualResult)
	require.Equal(t, expectedError, task.Error)
}

func TestTaskWithInvalidUsage(t *testing.T) {
	require.Panics(t, func() {
		task := NewTask[string]()
		task.Run(func(ctx *TaskContext[string]) {
			time.Sleep(250 * time.Millisecond)
			ctx.SetError(errors.New("error"))
			ctx.SetResult("value")
		})

		_ = task.Result()
	})
}
