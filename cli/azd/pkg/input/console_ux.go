// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package input

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"

	surveyterm "github.com/AlecAivazis/survey/v2/terminal"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	uxlib "github.com/azure/azure-dev/cli/azd/pkg/ux"
)

// The interactive prompts below are rendered with the in-repo ux package instead
// of AlecAivazis/survey. survey has a Windows-specific double-render bug (the
// prompt and its answer are redrawn above the next prompt) that was never fixed
// upstream and the library is archived. See Azure/azure-dev#435.
//
// Only the terminal path is routed through ux. The non-terminal / no-prompt paths
// continue to use the lightweight asker implementation so machine-friendly input
// behavior (used by tests, CI, and piped stdin) is unchanged.

// mapUxCancel converts a ux cancellation error into the survey interrupt error
// that the rest of azd already recognizes, preserving existing error handling.
func mapUxCancel(err error) error {
	if err != nil && errors.Is(err, uxlib.ErrCancelled) {
		return surveyterm.InterruptErr
	}

	return err
}

// optionLabel returns the display label for an option, appending the gray detail
// text (when present) the same way the previous survey-based rendering did.
func optionLabel(options ConsoleOptions, index int) string {
	option := options.Options[index]
	if index < len(options.OptionDetails) && options.OptionDetails[index] != "" {
		return fmt.Sprintf("%s %s", option, output.WithGrayFormat("(%s)", options.OptionDetails[index]))
	}

	return option
}

// selectChoices builds the ux select choices and the initially selected index
// from the console options. The selected index defaults to 0 and is set to the
// position of the string default value when present.
func selectChoices(options ConsoleOptions) ([]*uxlib.SelectChoice, int) {
	choices := make([]*uxlib.SelectChoice, len(options.Options))
	for i, option := range options.Options {
		choices[i] = &uxlib.SelectChoice{
			Value: option,
			Label: optionLabel(options, i),
		}
	}

	selectedIndex := 0
	if value, ok := options.DefaultValue.(string); ok {
		if idx := slices.Index(options.Options, value); idx >= 0 {
			selectedIndex = idx
		}
	}

	return choices, selectedIndex
}

// multiSelectChoices builds the ux multi-select choices from the console options,
// marking choices that appear in the []string default value as pre-selected.
func multiSelectChoices(options ConsoleOptions) []*uxlib.MultiSelectChoice {
	defaultValues, _ := options.DefaultValue.([]string)

	choices := make([]*uxlib.MultiSelectChoice, len(options.Options))
	for i, option := range options.Options {
		choices[i] = &uxlib.MultiSelectChoice{
			Value:    option,
			Label:    optionLabel(options, i),
			Selected: slices.Contains(defaultValues, option),
		}
	}

	return choices
}

// multiSelectValues maps the ux multi-select result back to the option values.
func multiSelectValues(selected []*uxlib.MultiSelectChoice) []string {
	response := make([]string, len(selected))
	for i, choice := range selected {
		response[i] = choice.Value
	}

	return response
}

// newPromptOptions builds the ux prompt options from the console options.
func newPromptOptions(writer io.Writer, options ConsoleOptions) *uxlib.PromptOptions {
	var defaultValue string
	if value, ok := options.DefaultValue.(string); ok {
		defaultValue = value
	}

	return &uxlib.PromptOptions{
		Writer:       writer,
		Message:      options.Message,
		HelpMessage:  options.Help,
		DefaultValue: defaultValue,
		Secret:       options.IsPassword,
	}
}

// newSelectOptions builds the ux select options from the console options.
func newSelectOptions(writer io.Writer, options ConsoleOptions) *uxlib.SelectOptions {
	choices, selectedIndex := selectChoices(options)

	return &uxlib.SelectOptions{
		Writer:        writer,
		Message:       options.Message,
		HelpMessage:   options.Help,
		Choices:       choices,
		SelectedIndex: new(selectedIndex),
	}
}

// newConfirmOptions builds the ux confirm options from the console options.
func newConfirmOptions(writer io.Writer, options ConsoleOptions) *uxlib.ConfirmOptions {
	defaultValue := false
	if value, ok := options.DefaultValue.(bool); ok {
		defaultValue = value
	}

	return &uxlib.ConfirmOptions{
		Writer:       writer,
		Message:      options.Message,
		HelpMessage:  options.Help,
		DefaultValue: new(defaultValue),
	}
}

// newMultiSelectOptions builds the ux multi-select options from the console options.
// Empty selection is allowed to preserve the behavior of the survey-based
// implementation that callers depend on.
func newMultiSelectOptions(writer io.Writer, options ConsoleOptions) *uxlib.MultiSelectOptions {
	return &uxlib.MultiSelectOptions{
		Writer:              writer,
		Message:             options.Message,
		HelpMessage:         options.Help,
		Choices:             multiSelectChoices(options),
		AllowEmptySelection: new(true),
	}
}

// selectResult converts the ux select result into the (index, error) pair the
// console contract expects. A nil result indicates an interrupted prompt.
func selectResult(result *int, err error) (int, error) {
	if err != nil {
		return -1, err
	}
	if result == nil {
		return -1, surveyterm.InterruptErr
	}

	return *result, nil
}

// confirmResult converts the ux confirm result into the (bool, error) pair the
// console contract expects. A nil result indicates an interrupted prompt.
func confirmResult(result *bool, err error) (bool, error) {
	if err != nil {
		return false, err
	}
	if result == nil {
		return false, surveyterm.InterruptErr
	}

	return *result, nil
}

// multiSelectResult converts the ux multi-select result into the ([]string, error)
// pair the console contract expects.
func multiSelectResult(selected []*uxlib.MultiSelectChoice, err error) ([]string, error) {
	if err != nil {
		return nil, err
	}

	return multiSelectValues(selected), nil
}

// uxComponent is the subset of the ux prompt components used by runComponent.
type uxComponent[T any] interface {
	Ask(ctx context.Context) (T, error)
}

// runComponent executes an interactive ux component, pausing any active spinner,
// mapping cancellation to the survey interrupt error, and recording the
// post-interaction console state on success.
func runComponent[T any](ctx context.Context, c *AskerConsole, component uxComponent[T]) (T, error) {
	var result T
	err := c.doInteraction(func(c *AskerConsole) error {
		var askErr error
		result, askErr = component.Ask(ctx)
		return askErr
	})
	if err != nil {
		var zero T
		return zero, mapUxCancel(err)
	}

	c.updateLastBytes(afterIoSentinel)
	return result, nil
}

func (c *AskerConsole) promptUx(ctx context.Context, options ConsoleOptions) (string, error) {
	return runComponent(ctx, c, uxlib.NewPrompt(newPromptOptions(c.writer, options)))
}

func (c *AskerConsole) selectUx(ctx context.Context, options ConsoleOptions) (int, error) {
	return selectResult(runComponent(ctx, c, uxlib.NewSelect(newSelectOptions(c.writer, options))))
}

func (c *AskerConsole) confirmUx(ctx context.Context, options ConsoleOptions) (bool, error) {
	return confirmResult(runComponent(ctx, c, uxlib.NewConfirm(newConfirmOptions(c.writer, options))))
}

func (c *AskerConsole) multiSelectUx(ctx context.Context, options ConsoleOptions) ([]string, error) {
	return multiSelectResult(runComponent(ctx, c, uxlib.NewMultiSelect(newMultiSelectOptions(c.writer, options))))
}
