package project

import (
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"io"
	"log"

	"context"
)

// A console implementation which output goes only to logs
// This is used to prevent or stop actions using the terminal output, for
// example, when calling provision during deploying a service.
type silentConsole struct {
}

// Sets the underlying writer for output the console or
// if writer is nil, sets it back to the default writer.
func (sc *silentConsole) SetWriter(writer io.Writer) {
	log.Println("tried to set writer for silent console is a no-op action")
}

func (sc *silentConsole) GetFormatter() output.Formatter {
	return nil
}

func (sc *silentConsole) IsUnformatted() bool {
	return true
}

// Prints out a message to the underlying console write
func (sc *silentConsole) Message(ctx context.Context, message string) {
	log.Println(message)
}

func (sc *silentConsole) MessageUxItem(ctx context.Context, item ux.UxItem) {
	sc.Message(ctx, item.ToString(""))
}

func (sc *silentConsole) ShowSpinner(ctx context.Context, title string, format input.SpinnerUxType) {
	log.Printf("request to show spinner on silent console with message: %s", title)
}

func (sc *silentConsole) StopSpinner(ctx context.Context, lastMessage string, format input.SpinnerUxType) {
	log.Printf("request to stop spinner on silent console with message: %s", lastMessage)
}

// Prompts the user for a single value
func (sc *silentConsole) Prompt(ctx context.Context, options input.ConsoleOptions) (string, error) {
	log.Panic("Can't call Prompt from a silent console.")
	return "", nil
}

// Prompts the user to select from a set of values
func (sc *silentConsole) Select(ctx context.Context, options input.ConsoleOptions) (int, error) {
	log.Panic("Can't call Select from a silent console.")
	return 0, nil
}

// Prompts the user to confirm an operation
func (sc *silentConsole) Confirm(ctx context.Context, options input.ConsoleOptions) (bool, error) {
	log.Panic("Can't call Confirm from a silent console.")
	return false, nil
}

func (sc *silentConsole) GetWriter() io.Writer {
	return nil
}

func (sc *silentConsole) Handles() input.ConsoleHandles {
	return input.ConsoleHandles{}
}
