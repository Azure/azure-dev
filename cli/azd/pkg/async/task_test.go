package async

import (
	"context"
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
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

func TestInteractiveTaskWithResult(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	progress := []string{}
	interactiveStatus := []bool{}
	expectedResult := "westus2"

	progressDone := make(chan bool)
	interactiveDone := make(chan bool)

	mockContext.Console.WhenPrompt(func(options input.ConsoleOptions) bool {
		return options.Message == "What location?"
	}).Respond(expectedResult)

	mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
		return options.Message == "Are you sure?"
	}).Respond(true)

	task := NewInteractiveTaskWithProgress(func(taskContext *InteractiveTaskContextWithProgress[string, string]) {
		var selectedLocation string

		taskContext.SetProgress("thing 1")
		taskContext.SetProgress("thing 2")

		err := taskContext.Interact(func() error {
			location, err := mockContext.Console.Prompt(*mockContext.Context, input.ConsoleOptions{
				Message:      "What location?",
				DefaultValue: "eastus2",
			})

			if err != nil {
				return err
			}

			confirm, err := mockContext.Console.Confirm(*mockContext.Context, input.ConsoleOptions{
				Message:      "Are you sure?",
				DefaultValue: true,
			})

			if err != nil {
				return err
			}

			if !confirm {
				return errors.New("User did not confirm")
			}

			selectedLocation = location

			return nil
		})

		if err != nil {
			taskContext.SetError(err)
			return
		}

		taskContext.SetResult(selectedLocation)
	})

	err := task.Run()
	require.NoError(t, err)

	go func() {
		for status := range task.Progress() {
			progress = append(progress, status)
		}
		progressDone <- true
	}()

	go func() {
		for isInteractive := range task.interactiveChannel {
			interactiveStatus = append(interactiveStatus, isInteractive)
		}
		interactiveDone <- true
	}()

	actualResult, err := task.Await()
	<-progressDone
	<-interactiveDone

	// Result still expected
	require.Equal(t, expectedResult, actualResult)
	require.Nil(t, err)
	// Progress still reported
	require.Equal(t, 2, len(progress))
	require.Equal(t, "thing 1", progress[0])
	require.Equal(t, "thing 2", progress[1])
	// interactive status reported
	require.Equal(t, 2, len(interactiveStatus))
	require.Equal(t, true, interactiveStatus[0])
	require.Equal(t, false, interactiveStatus[1])
}

func TestInteractiveTaskWithError(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	progress := []string{}
	interactiveStatus := []bool{}

	progressDone := make(chan bool)
	interactiveDone := make(chan bool)

	expectedError := errors.New("User did not confirm")

	mockContext.Console.WhenPrompt(func(options input.ConsoleOptions) bool {
		return options.Message == "What location?"
	}).Respond("westus2")

	// This time the user will not confirm
	mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
		return options.Message == "Are you sure?"
	}).Respond(false)

	task := NewInteractiveTaskWithProgress(func(taskContext *InteractiveTaskContextWithProgress[string, string]) {
		var selectedLocation string

		taskContext.SetProgress("thing 1")
		taskContext.SetProgress("thing 2")

		err := taskContext.Interact(func() error {
			_, err := mockContext.Console.Prompt(*mockContext.Context, input.ConsoleOptions{
				Message:      "What location?",
				DefaultValue: "eastus2",
			})

			if err != nil {
				return err
			}

			confirm, err := mockContext.Console.Confirm(*mockContext.Context, input.ConsoleOptions{
				Message:      "Are you sure?",
				DefaultValue: true,
			})

			if err != nil {
				return err
			}

			if !confirm {
				return expectedError
			}

			return nil
		})

		if err != nil {
			taskContext.SetError(err)
			return
		}

		taskContext.SetResult(selectedLocation)
	})

	err := task.Run()
	require.NoError(t, err)

	go func() {
		for status := range task.Progress() {
			progress = append(progress, status)
		}
		progressDone <- true
	}()

	go func() {
		for isInteractive := range task.interactiveChannel {
			interactiveStatus = append(interactiveStatus, isInteractive)
		}
		interactiveDone <- true
	}()

	actualResult, err := task.Await()
	<-progressDone
	<-interactiveDone

	// Err expected
	require.Equal(t, "", actualResult)
	require.Contains(t, err.Error(), expectedError.Error())
	// Progress still reported
	require.Equal(t, 2, len(progress))
	require.Equal(t, "thing 1", progress[0])
	require.Equal(t, "thing 2", progress[1])
	// interactive status reported
	require.Equal(t, 2, len(interactiveStatus))
	require.Equal(t, true, interactiveStatus[0])
	require.Equal(t, false, interactiveStatus[1])
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
