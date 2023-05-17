package project

import (
	"context"
	"io"
	"log"

	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
)

// A console implementation which output goes only to logs
// This is used to prevent or stop actions using the terminal output, for
// example, when calling provision during deploying a service.
type MutedConsole struct {
	ParentConsole input.Console
}

// Sets the underlying writer for output the console or
// if writer is nil, sets it back to the default writer.
func (sc *MutedConsole) SetWriter(writer io.Writer) {
	log.Println("tried to set writer for silent console is a no-op action")
}

func (sc *MutedConsole) GetFormatter() output.Formatter {
	return nil
}

func (sc *MutedConsole) IsUnformatted() bool {
	return true
}

// Prints out a message to the underlying console write
func (sc *MutedConsole) Message(ctx context.Context, message string) {
	log.Println(message)
}

func (sc *MutedConsole) MessageUxItem(ctx context.Context, item ux.UxItem) {
	sc.Message(ctx, item.ToString(""))
}

func (sc *MutedConsole) ShowSpinner(ctx context.Context, title string, format input.SpinnerUxType) {
	log.Printf("request to show spinner on silent console with message: %s", title)
}

func (sc *MutedConsole) StopSpinner(ctx context.Context, lastMessage string, format input.SpinnerUxType) {
	log.Printf("request to stop spinner on silent console with message: %s", lastMessage)
}

func (sc *MutedConsole) IsSpinnerRunning(ctx context.Context) bool {
	return false
}

// Use parent console for input
func (sc *MutedConsole) Prompt(ctx context.Context, options input.ConsoleOptions) (string, error) {
	return sc.ParentConsole.Prompt(ctx, options)
}

// Use parent console for input
func (sc *MutedConsole) Select(ctx context.Context, options input.ConsoleOptions) (int, error) {
	return sc.ParentConsole.Select(ctx, options)
}

// Use parent console for input
func (sc *MutedConsole) Confirm(ctx context.Context, options input.ConsoleOptions) (bool, error) {
	return sc.ParentConsole.Confirm(ctx, options)
}

func (sc *MutedConsole) GetWriter() io.Writer {
	return nil
}

func (sc *MutedConsole) Handles() input.ConsoleHandles {
	return sc.ParentConsole.Handles()
}
