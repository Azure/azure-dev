package input

import (
	"context"
	"fmt"
	"io"
	"log"

	"github.com/AlecAivazis/survey/v2"
	"github.com/mattn/go-colorable"
)

type Console interface {
	Message(ctx context.Context, message string)
	Prompt(ctx context.Context, options ConsoleOptions) (string, error)
	Select(ctx context.Context, options ConsoleOptions) (int, error)
	Confirm(ctx context.Context, options ConsoleOptions) (bool, error)
	SetWriter(writer io.Writer)
}

type AskerConsole struct {
	interactive bool
	asker       Asker
	writer      io.Writer
}

type ConsoleOptions struct {
	Message      string
	Options      []string
	DefaultValue any
}

func (c *AskerConsole) SetWriter(writer io.Writer) {
	if writer == nil {
		writer = colorable.NewColorableStdout()
	}

	c.writer = writer
}

func (c *AskerConsole) Message(ctx context.Context, message string) {
	if c.interactive {
		fmt.Fprintln(c.writer, message)
	} else {
		log.Println(message)
	}
}

func (c *AskerConsole) Prompt(ctx context.Context, options ConsoleOptions) (string, error) {
	var defaultValue string
	if value, ok := options.DefaultValue.(string); ok {
		defaultValue = value
	}

	survey := &survey.Input{
		Message: options.Message,
		Default: defaultValue,
	}

	var response string

	if err := c.asker(survey, &response); err != nil {
		return "", err
	}

	return response, nil
}

func (c *AskerConsole) Select(ctx context.Context, options ConsoleOptions) (int, error) {
	survey := &survey.Select{
		Message: options.Message,
		Options: options.Options,
		Default: options.DefaultValue,
	}

	var response int

	if err := c.asker(survey, &response); err != nil {
		return -1, err
	}

	return response, nil
}

func (c *AskerConsole) Confirm(ctx context.Context, options ConsoleOptions) (bool, error) {
	var defaultValue bool
	if value, ok := options.DefaultValue.(bool); ok {
		defaultValue = value
	}

	survey := &survey.Confirm{
		Message: options.Message,
		Default: defaultValue,
	}

	var response bool

	if err := c.asker(survey, &response); err != nil {
		return false, err
	}

	return response, nil
}

func (c *AskerConsole) Writer() io.Writer {
	return c.writer
}

func NewConsole(interactive bool, writer io.Writer) Console {
	asker := NewAsker(!interactive)

	return &AskerConsole{
		interactive: interactive,
		asker:       asker,
		writer:      writer,
	}
}

type contextKey string

const (
	consoleContextKey contextKey = "console"
)

func WithConsole(ctx context.Context, console Console) context.Context {
	return context.WithValue(ctx, consoleContextKey, console)
}

func GetConsole(ctx context.Context) Console {
	console, ok := ctx.Value(consoleContextKey).(Console)
	if !ok {
		return nil
	}

	return console
}
