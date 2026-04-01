// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Confirm tests ---

func TestNewConfirm_defaults(t *testing.T) {
	c := NewConfirm(&ConfirmOptions{
		Message: "Continue?",
	})
	require.NotNil(t, c)
	assert.Equal(t, "Continue?", c.options.Message)
	assert.Equal(t, "[y/n]", c.options.Hint)
	assert.Nil(t, c.value)
}

func TestNewConfirm_with_default_true(t *testing.T) {
	c := NewConfirm(&ConfirmOptions{
		Message:      "Continue?",
		DefaultValue: new(true),
	})
	require.NotNil(t, c)
	assert.Equal(t, "[Y/n]", c.options.Hint)
	assert.Equal(t, "Yes", c.displayValue)
	require.NotNil(t, c.value)
	assert.True(t, *c.value)
}

func TestNewConfirm_with_default_false(t *testing.T) {
	c := NewConfirm(&ConfirmOptions{
		Message:      "Continue?",
		DefaultValue: new(false),
	})
	assert.Equal(t, "[y/N]", c.options.Hint)
	assert.Equal(t, "No", c.displayValue)
	require.NotNil(t, c.value)
	assert.False(t, *c.value)
}

func TestNewConfirm_custom_hint(t *testing.T) {
	c := NewConfirm(&ConfirmOptions{
		Message: "Continue?",
		Hint:    "[custom]",
	})
	assert.Equal(t, "[custom]", c.options.Hint)
}

func TestConfirm_Render_initial(t *testing.T) {
	var buf bytes.Buffer
	printer := NewPrinter(&buf)

	c := NewConfirm(&ConfirmOptions{
		Message:     "Continue?",
		HelpMessage: "Some help",
	})

	err := c.Render(printer)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "Continue?")
	assert.Contains(t, output, "[y/n]")
}

func TestConfirm_Render_complete(t *testing.T) {
	var buf bytes.Buffer
	printer := NewPrinter(&buf)

	c := NewConfirm(&ConfirmOptions{Message: "OK?"})
	c.complete = true
	c.displayValue = "Yes"

	err := c.Render(printer)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "OK?")
	assert.Contains(t, output, "Yes")
}

func TestConfirm_Render_cancelled(t *testing.T) {
	var buf bytes.Buffer
	printer := NewPrinter(&buf)

	c := NewConfirm(&ConfirmOptions{Message: "OK?"})
	c.cancelled = true

	err := c.Render(printer)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "Cancelled")
}

func TestConfirm_Render_validation_error(t *testing.T) {
	var buf bytes.Buffer
	printer := NewPrinter(&buf)

	c := NewConfirm(&ConfirmOptions{Message: "OK?"})
	c.hasValidationError = true

	err := c.Render(printer)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "Enter a valid value")
}

func TestConfirm_Render_with_help(t *testing.T) {
	var buf bytes.Buffer
	printer := NewPrinter(&buf)

	c := NewConfirm(&ConfirmOptions{
		Message:     "OK?",
		HelpMessage: "Pick yes or no",
	})
	c.showHelp = true

	err := c.Render(printer)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "Hint:")
	assert.Contains(t, output, "Pick yes or no")
}

func TestConfirm_WithCanvas(t *testing.T) {
	c := NewConfirm(&ConfirmOptions{Message: "OK?"})
	var buf bytes.Buffer
	canvas := NewCanvas().WithWriter(&buf)
	defer canvas.Close()

	result := c.WithCanvas(canvas)
	assert.Equal(t, c, result)
}

// --- Prompt tests ---

func TestNewPrompt_defaults(t *testing.T) {
	p := NewPrompt(&PromptOptions{
		Message: "Enter name",
	})
	require.NotNil(t, p)
	assert.Equal(t, "Enter name", p.options.Message)
	assert.False(t, p.options.Required)
	assert.Equal(t, "", p.options.Hint)
}

func TestNewPrompt_auto_hint_with_help(t *testing.T) {
	p := NewPrompt(&PromptOptions{
		Message:     "Enter name",
		HelpMessage: "Your full name",
	})
	assert.Equal(t, "[Type ? for hint]", p.options.Hint)
}

func TestNewPrompt_custom_hint_preserved(t *testing.T) {
	p := NewPrompt(&PromptOptions{
		Message:     "Enter name",
		HelpMessage: "Your full name",
		Hint:        "[custom hint]",
	})
	assert.Equal(t, "[custom hint]", p.options.Hint)
}

func TestNewPrompt_with_default_value(t *testing.T) {
	p := NewPrompt(&PromptOptions{
		Message:      "Port",
		DefaultValue: "8080",
	})
	assert.Equal(t, "8080", p.value)
}

func TestPrompt_validate_required_empty(t *testing.T) {
	p := NewPrompt(&PromptOptions{
		Message:  "Name",
		Required: true,
	})
	p.value = ""
	p.validate()
	assert.True(t, p.hasValidationError)
	assert.Equal(t,
		"This field is required", p.validationMessage,
	)
}

func TestPrompt_validate_required_filled(t *testing.T) {
	p := NewPrompt(&PromptOptions{
		Message:  "Name",
		Required: true,
	})
	p.value = "Jon"
	p.validate()
	assert.False(t, p.hasValidationError)
}

func TestPrompt_validate_custom_fn_fail(t *testing.T) {
	p := NewPrompt(&PromptOptions{
		Message: "Port",
		ValidationFn: func(s string) (bool, string) {
			return false, "must be numeric"
		},
	})
	p.value = "abc"
	p.validate()
	assert.True(t, p.hasValidationError)
	assert.Equal(t, "must be numeric", p.validationMessage)
}

func TestPrompt_validate_custom_fn_pass(t *testing.T) {
	p := NewPrompt(&PromptOptions{
		Message: "Port",
		ValidationFn: func(s string) (bool, string) {
			return true, ""
		},
	})
	p.value = "8080"
	p.validate()
	assert.False(t, p.hasValidationError)
}

func TestPrompt_Render_initial(t *testing.T) {
	var buf bytes.Buffer
	printer := NewPrinter(&buf)

	p := NewPrompt(&PromptOptions{Message: "Name"})

	err := p.Render(printer)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "Name")
}

func TestPrompt_Render_with_placeholder(t *testing.T) {
	var buf bytes.Buffer
	printer := NewPrinter(&buf)

	p := NewPrompt(&PromptOptions{
		Message:     "Name",
		PlaceHolder: "Type here...",
	})
	p.value = ""

	err := p.Render(printer)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "Type here...")
}

func TestPrompt_Render_complete(t *testing.T) {
	var buf bytes.Buffer
	printer := NewPrinter(&buf)

	p := NewPrompt(&PromptOptions{Message: "Name"})
	p.complete = true
	p.value = "Jon"

	err := p.Render(printer)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "Jon")
}

func TestPrompt_Render_clear_on_completion(t *testing.T) {
	var buf bytes.Buffer
	printer := NewPrinter(&buf)

	p := NewPrompt(&PromptOptions{
		Message:           "Name",
		ClearOnCompletion: true,
	})
	p.complete = true

	err := p.Render(printer)
	require.NoError(t, err)

	assert.Empty(t, buf.String())
}

func TestPrompt_Render_cancelled(t *testing.T) {
	var buf bytes.Buffer
	printer := NewPrinter(&buf)

	p := NewPrompt(&PromptOptions{Message: "Name"})
	p.cancelled = true

	err := p.Render(printer)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "Cancelled")
}

func TestPrompt_Render_validation_shown(t *testing.T) {
	var buf bytes.Buffer
	printer := NewPrinter(&buf)

	p := NewPrompt(&PromptOptions{Message: "Port"})
	p.submitted = true
	p.hasValidationError = true
	p.validationMessage = "Invalid port"

	err := p.Render(printer)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "Invalid port")
}

func TestPrompt_Render_help_message(t *testing.T) {
	var buf bytes.Buffer
	printer := NewPrinter(&buf)

	p := NewPrompt(&PromptOptions{
		Message:     "Port",
		HelpMessage: "Enter a port number",
	})
	p.showHelp = true

	err := p.Render(printer)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "Hint:")
	assert.Contains(t, output, "Enter a port number")
}

func TestPrompt_Render_help_message_next_line(t *testing.T) {
	var buf bytes.Buffer
	printer := NewPrinter(&buf)

	p := NewPrompt(&PromptOptions{
		Message:               "Name",
		HelpMessageOnNextLine: "Below the input",
	})

	err := p.Render(printer)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "Below the input")
}

func TestPrompt_WithCanvas(t *testing.T) {
	p := NewPrompt(&PromptOptions{Message: "X"})
	var buf bytes.Buffer
	c := NewCanvas().WithWriter(&buf)
	defer c.Close()

	result := p.WithCanvas(c)
	assert.Equal(t, p, result)
}
