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
type mutedConsole struct {
	parentConsole input.Console
}

// Sets the underlying writer for output the console or
// if writer is nil, sets it back to the default writer.
func (sc *mutedConsole) SetWriter(writer io.Writer) {
	log.Println("tried to set writer for silent console is a no-op action")
}

func (sc *mutedConsole) GetFormatter() output.Formatter {
	return nil
}

func (sc *mutedConsole) IsUnformatted() bool {
	return true
}

// Prints out a message to the underlying console write
func (sc *mutedConsole) Message(ctx context.Context, message string) {
	log.Println(message)
}

func (sc *mutedConsole) MessageUxItem(ctx context.Context, item ux.UxItem) {
	sc.Message(ctx, item.ToString(""))
}

func (sc *mutedConsole) ShowSpinner(ctx context.Context, title string, format input.SpinnerUxType) {
	log.Printf("request to show spinner on silent console with message: %s", title)
}

func (sc *mutedConsole) StopSpinner(ctx context.Context, lastMessage string, format input.SpinnerUxType) {
	log.Printf("request to stop spinner on silent console with message: %s", lastMessage)
}

func (sc *mutedConsole) IsSpinnerRunning(ctx context.Context) bool {
	return false
}

// Use parent console for input
func (sc *mutedConsole) Prompt(ctx context.Context, options input.ConsoleOptions) (string, error) {
	return sc.parentConsole.Prompt(ctx, options)
}

// Use parent console for input
func (sc *mutedConsole) Select(ctx context.Context, options input.ConsoleOptions) (int, error) {
	return sc.parentConsole.Select(ctx, options)
}

// Use parent console for input
func (sc *mutedConsole) Confirm(ctx context.Context, options input.ConsoleOptions) (bool, error) {
	return sc.parentConsole.Confirm(ctx, options)
}

func (sc *mutedConsole) GetWriter() io.Writer {
	return nil
}

func (sc *mutedConsole) Handles() input.ConsoleHandles {
	return sc.parentConsole.Handles()
}
