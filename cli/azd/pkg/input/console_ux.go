// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package input

import (
	"context"
	"errors"
	"fmt"
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

func (c *AskerConsole) promptUx(ctx context.Context, options ConsoleOptions) (string, error) {
	var defaultValue string
	if value, ok := options.DefaultValue.(string); ok {
		defaultValue = value
	}

	prompt := uxlib.NewPrompt(&uxlib.PromptOptions{
		Writer:       c.writer,
		Message:      options.Message,
		HelpMessage:  options.Help,
		DefaultValue: defaultValue,
		Secret:       options.IsPassword,
	})

	var response string
	err := c.doInteraction(func(c *AskerConsole) error {
		var askErr error
		response, askErr = prompt.Ask(ctx)
		return askErr
	})
	if err != nil {
		return "", mapUxCancel(err)
	}

	c.updateLastBytes(afterIoSentinel)
	return response, nil
}

func (c *AskerConsole) selectUx(ctx context.Context, options ConsoleOptions) (int, error) {
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

	component := uxlib.NewSelect(&uxlib.SelectOptions{
		Writer:        c.writer,
		Message:       options.Message,
		HelpMessage:   options.Help,
		Choices:       choices,
		SelectedIndex: &selectedIndex,
	})

	var result *int
	err := c.doInteraction(func(c *AskerConsole) error {
		var askErr error
		result, askErr = component.Ask(ctx)
		return askErr
	})
	if err != nil {
		return -1, mapUxCancel(err)
	}
	if result == nil {
		return -1, surveyterm.InterruptErr
	}

	c.updateLastBytes(afterIoSentinel)
	return *result, nil
}

func (c *AskerConsole) confirmUx(ctx context.Context, options ConsoleOptions) (bool, error) {
	defaultValue := false
	if value, ok := options.DefaultValue.(bool); ok {
		defaultValue = value
	}

	component := uxlib.NewConfirm(&uxlib.ConfirmOptions{
		Writer:       c.writer,
		Message:      options.Message,
		HelpMessage:  options.Help,
		DefaultValue: &defaultValue,
	})

	var result *bool
	err := c.doInteraction(func(c *AskerConsole) error {
		var askErr error
		result, askErr = component.Ask(ctx)
		return askErr
	})
	if err != nil {
		return false, mapUxCancel(err)
	}
	if result == nil {
		return false, surveyterm.InterruptErr
	}

	c.updateLastBytes(afterIoSentinel)
	return *result, nil
}

func (c *AskerConsole) multiSelectUx(ctx context.Context, options ConsoleOptions) ([]string, error) {
	defaultValues, _ := options.DefaultValue.([]string)

	choices := make([]*uxlib.MultiSelectChoice, len(options.Options))
	for i, option := range options.Options {
		choices[i] = &uxlib.MultiSelectChoice{
			Value:    option,
			Label:    optionLabel(options, i),
			Selected: slices.Contains(defaultValues, option),
		}
	}

	component := uxlib.NewMultiSelect(&uxlib.MultiSelectOptions{
		Writer:      c.writer,
		Message:     options.Message,
		HelpMessage: options.Help,
		Choices:     choices,
	})

	var selected []*uxlib.MultiSelectChoice
	err := c.doInteraction(func(c *AskerConsole) error {
		var askErr error
		selected, askErr = component.Ask(ctx)
		return askErr
	})
	if err != nil {
		return nil, mapUxCancel(err)
	}

	response := make([]string, len(selected))
	for i, choice := range selected {
		response[i] = choice.Value
	}

	c.updateLastBytes(afterIoSentinel)
	return response, nil
}
