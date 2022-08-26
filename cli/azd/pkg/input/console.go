// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package input

import (
	"context"
	"fmt"
	"io"
	"log"

	"github.com/AlecAivazis/survey/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

type Console interface {
	// Prints out a message to the underlying console write
	Message(ctx context.Context, message string)
	// Prompts the user for a single value
	Prompt(ctx context.Context, options ConsoleOptions) (string, error)
	// Prompts the user to select from a set of values
	Select(ctx context.Context, options ConsoleOptions) (int, error)
	// Prompts the user to confirm an operation
	Confirm(ctx context.Context, options ConsoleOptions) (bool, error)
	// Sets the underlying writer for the console
	SetWriter(writer io.Writer)
}

type AskerConsole struct {
	interactive bool
	asker       Asker
	writer      io.Writer
	formatter   output.Formatter
}

type ConsoleOptions struct {
	Message      string
	Options      []string
	DefaultValue any
}

// Sets the underlying writer for the console
func (c *AskerConsole) SetWriter(writer io.Writer) {
	if writer == nil {
		writer = output.GetDefaultWriter()
	}

	c.writer = writer
}

// Prints out a message to the underlying console write
func (c *AskerConsole) Message(ctx context.Context, message string) {
	// Only write to the console during interactive & non-formatted responses.
	if c.interactive && c.formatter.Kind() == output.NoneFormat {
		fmt.Fprintln(c.writer, message)
	} else {
		log.Println(message)
	}
}

// Prompts the user for a single value
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

// Prompts the user to select from a set of values
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

// Prompts the user to confirm an operation
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

// Gets the underlying writer for the console
func (c *AskerConsole) Writer() io.Writer {
	return c.writer
}

// Creates a new console with the specified writer and formatter
func NewConsole(interactive bool, writer io.Writer, formatter output.Formatter) Console {
	asker := NewAsker(!interactive)

	return &AskerConsole{
		interactive: interactive,
		asker:       asker,
		writer:      writer,
		formatter:   formatter,
	}
}

type contextKey string

const (
	consoleContextKey contextKey = "console"
)

// Sets the console instance in the go context and returns the new context
func WithConsole(ctx context.Context, console Console) context.Context {
	return context.WithValue(ctx, consoleContextKey, console)
}

// Gets the console from the go context or nil if not found
func GetConsole(ctx context.Context) Console {
	console, ok := ctx.Value(consoleContextKey).(Console)
	if !ok {
		return nil
	}

	return console
}
