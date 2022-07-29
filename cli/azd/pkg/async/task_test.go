package async

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
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

func TestTaskWithProgressWithResult(t *testing.T) {
	expectedResult := "result"
	progress := []string{}

	task := NewTaskWithProgress[string, string]()

	go func() {
		for status := range task.Progress() {
			progress = append(progress, status)
		}
	}()

	task.Run(func(ctx *TaskContextWithProgress[string, string]) {
		ctx.SetProgress("thing 1")
		time.Sleep(250 * time.Millisecond)
		ctx.SetProgress("thing 2")
		ctx.SetResult(expectedResult)
	})

	actualResult := task.Result()
	require.Equal(t, expectedResult, actualResult)
	require.Nil(t, task.Error)
	require.Equal(t, 2, len(progress))
	require.Equal(t, "thing 1", progress[0])
	require.Equal(t, "thing 2", progress[1])
}

func TestTaskWithProgressWithError(t *testing.T) {
	expectedError := errors.New("example error")
	progress := []string{}

	task := NewTaskWithProgress[string, string]()

	go func() {
		for status := range task.Progress() {
			progress = append(progress, status)
		}
	}()

	task.Run(func(ctx *TaskContextWithProgress[string, string]) {
		ctx.SetProgress("thing 1")
		time.Sleep(250 * time.Millisecond)
		ctx.SetProgress("thing 2")

		// Something bad happens but previous project goes through
		ctx.SetError(expectedError)
	})

	actualResult := task.Result()
	require.Equal(t, "", actualResult)
	require.Equal(t, expectedError, task.Error)
	require.Equal(t, 2, len(progress))
	require.Equal(t, "thing 1", progress[0])
	require.Equal(t, "thing 2", progress[1])
}

func TestInteractiveTaskWithResult(t *testing.T) {
	ctx := context.Background()
	console := mocks.NewMockConsole()
	progress := []string{}
	interactiveStatus := []bool{}
	expectedResult := "westus2"

	console.WhenPrompt(func(options input.ConsoleOptions) bool {
		return options.Message == "What location?"
	}).Respond(expectedResult)

	console.WhenConfirm(func(options input.ConsoleOptions) bool {
		return options.Message == "Are you sure?"
	}).Respond(true)

	task := NewInteractiveTaskWithProgress[string, string]()
	task.Run(func(taskContext *InteractiveTaskContextWithProgress[string, string]) {
		var selectedLocation string

		taskContext.SetProgress("thing 1")
		time.Sleep(250 * time.Millisecond)
		taskContext.SetProgress("thing 2")

		err := taskContext.Interact(func() error {
			location, err := console.Prompt(ctx, input.ConsoleOptions{
				Message:      "What location?",
				DefaultValue: "eastus2",
			})

			if err != nil {
				return err
			}

			confirm, err := console.Confirm(ctx, input.ConsoleOptions{
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

	go func() {
		for status := range task.Progress() {
			progress = append(progress, status)
		}
	}()

	go func() {
		for isInteractive := range task.interactiveChannel {
			interactiveStatus = append(interactiveStatus, isInteractive)
		}
	}()

	actualResult := task.Result()
	// Result still expected
	require.Equal(t, expectedResult, actualResult)
	require.Nil(t, task.Error)
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
	ctx := context.Background()
	console := mocks.NewMockConsole()
	progress := []string{}
	interactiveStatus := []bool{}
	expectedError := errors.New("User did not confirm")

	console.WhenPrompt(func(options input.ConsoleOptions) bool {
		return options.Message == "What location?"
	}).Respond("westus2")

	// This time the user will not confirm
	console.WhenConfirm(func(options input.ConsoleOptions) bool {
		return options.Message == "Are you sure?"
	}).Respond(false)

	task := NewInteractiveTaskWithProgress[string, string]()
	task.Run(func(taskContext *InteractiveTaskContextWithProgress[string, string]) {
		var selectedLocation string

		taskContext.SetProgress("thing 1")
		time.Sleep(250 * time.Millisecond)
		taskContext.SetProgress("thing 2")

		err := taskContext.Interact(func() error {
			_, err := console.Prompt(ctx, input.ConsoleOptions{
				Message:      "What location?",
				DefaultValue: "eastus2",
			})

			if err != nil {
				return err
			}

			confirm, err := console.Confirm(ctx, input.ConsoleOptions{
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
			return
		}

		taskContext.SetResult(selectedLocation)
	})

	go func() {
		for status := range task.Progress() {
			progress = append(progress, status)
		}
	}()

	go func() {
		for isInteractive := range task.interactiveChannel {
			interactiveStatus = append(interactiveStatus, isInteractive)
		}
	}()

	actualResult := task.Result()
	// Err expected
	require.Equal(t, "", actualResult)
	require.Contains(t, task.Error.Error(), expectedError.Error())
	// Progress still reported
	require.Equal(t, 2, len(progress))
	require.Equal(t, "thing 1", progress[0])
	require.Equal(t, "thing 2", progress[1])
	// interactive status reported
	require.Equal(t, 2, len(interactiveStatus))
	require.Equal(t, true, interactiveStatus[0])
	require.Equal(t, false, interactiveStatus[1])
}
