package input

import (
	"bufio"
	"context"
	"io"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
)

// A console implementation which pauses the terminal after each message until the user presses enter
type pausedConsole struct {
	Console
	parentConsole Console
}

func NewPausedConsole(basedConsole Console) Console {
	return &pausedConsole{
		parentConsole: basedConsole,
	}
}

// calls parent's implementation
func (sc *pausedConsole) SetWriter(writer io.Writer) {
	sc.parentConsole.SetWriter(writer)
}

// calls parent's implementation
func (sc *pausedConsole) GetFormatter() output.Formatter {
	return sc.parentConsole.GetFormatter()
}

// calls parent's implementation
func (sc *pausedConsole) IsUnformatted() bool {
	return sc.parentConsole.IsUnformatted()
}

// calls parent's implementation then block until next enter
func (sc *pausedConsole) Message(ctx context.Context, message string) {
	sc.parentConsole.Message(ctx, message)
	_ = bufio.NewScanner(sc.Handles().Stdin).Scan()
}

// calls parent's implementation then block until next enter
func (sc *pausedConsole) MessageUxItem(ctx context.Context, item ux.UxItem) {
	sc.parentConsole.MessageUxItem(ctx, item)
	_ = bufio.NewScanner(sc.Handles().Stdin).Scan()
}

// calls parent's implementation
func (sc *pausedConsole) ShowSpinner(ctx context.Context, title string, format SpinnerUxType) {
	sc.parentConsole.ShowSpinner(ctx, title, format)
}

// calls parent's implementation
func (sc *pausedConsole) StopSpinner(ctx context.Context, lastMessage string, format SpinnerUxType) {
	sc.parentConsole.StopSpinner(ctx, lastMessage, format)
}

// calls parent's implementation
func (sc *pausedConsole) IsSpinnerRunning(ctx context.Context) bool {
	return sc.parentConsole.IsSpinnerRunning(ctx)
}

// calls parent's implementation
func (sc *pausedConsole) Prompt(ctx context.Context, options ConsoleOptions) (string, error) {
	return sc.parentConsole.Prompt(ctx, options)
}

// calls parent's implementation
func (sc *pausedConsole) Select(ctx context.Context, options ConsoleOptions) (int, error) {
	return sc.parentConsole.Select(ctx, options)
}

// calls parent's implementation
func (sc *pausedConsole) Confirm(ctx context.Context, options ConsoleOptions) (bool, error) {
	return sc.parentConsole.Confirm(ctx, options)
}

// calls parent's implementation
func (sc *pausedConsole) GetWriter() io.Writer {
	return sc.parentConsole.GetWriter()
}

// calls parent's implementation
func (sc *pausedConsole) Handles() ConsoleHandles {
	return sc.parentConsole.Handles()
}
