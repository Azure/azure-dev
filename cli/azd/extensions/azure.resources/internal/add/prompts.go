// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package add

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"azure.resources/internal/resources"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

var labelPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

func (w *Wizard) text(ctx context.Context, message, defaultValue string) (string, error) {
	response, err := w.Client.Prompt().Prompt(ctx, &azdext.PromptRequest{Options: &azdext.PromptOptions{
		Message: message, DefaultValue: defaultValue, Required: true,
	}})
	if err != nil {
		return "", fmt.Errorf("prompting for %s: %w", message, err)
	}
	if response == nil {
		return "", fmt.Errorf("no response to %s", message)
	}
	return response.Value, nil
}

func (w *Wizard) choose(ctx context.Context, message string, choices []string) (string, error) {
	if len(choices) == 0 {
		return "", fmt.Errorf("no choices available for %s", message)
	}
	options := make([]*azdext.SelectChoice, len(choices))
	for i, choice := range choices {
		options[i] = &azdext.SelectChoice{Value: choice, Label: choice}
	}
	response, err := w.Client.Prompt().Select(ctx, &azdext.SelectRequest{Options: &azdext.SelectOptions{
		Message: message, Choices: options, SelectedIndex: new(int32(0)),
	}})
	if err != nil {
		return "", fmt.Errorf("selecting %s: %w", message, err)
	}
	if response == nil || response.Value == nil || *response.Value < 0 || int(*response.Value) >= len(choices) {
		return "", fmt.Errorf("invalid selection for %s", message)
	}
	return choices[*response.Value], nil
}

func (w *Wizard) confirm(ctx context.Context, message string, defaultValue bool) (bool, error) {
	response, err := w.Client.Prompt().Confirm(ctx, &azdext.ConfirmRequest{Options: &azdext.ConfirmOptions{
		Message: message, DefaultValue: new(defaultValue),
	}})
	if err != nil {
		return false, fmt.Errorf("confirming %s: %w", message, err)
	}
	if response == nil || response.Value == nil {
		return false, fmt.Errorf("no confirmation for %s", message)
	}
	return *response.Value, nil
}

func (w *Wizard) multi(ctx context.Context, message string, choices []string) ([]string, error) {
	options := make([]*azdext.MultiSelectChoice, len(choices))
	for i, choice := range choices {
		options[i] = &azdext.MultiSelectChoice{Value: choice, Label: choice}
	}
	response, err := w.Client.Prompt().MultiSelect(ctx,
		&azdext.MultiSelectRequest{Options: &azdext.MultiSelectOptions{
			Message: message, Choices: options, AllowEmptySelection: new(true),
		}})
	if err != nil {
		return nil, fmt.Errorf("selecting %s: %w", message, err)
	}
	if response == nil {
		return nil, fmt.Errorf("no response to %s", message)
	}
	selected := make([]string, 0, len(response.Values))
	for _, choice := range response.Values {
		if choice == nil || !slices.Contains(choices, choice.Value) {
			return nil, fmt.Errorf("invalid selection for %s", message)
		}
		if !slices.Contains(selected, choice.Value) {
			selected = append(selected, choice.Value)
		}
	}
	return selected, nil
}

func (w *Wizard) resourceName(ctx context.Context, defaultName string) (string, error) {
	for {
		name, err := w.text(ctx, "What should we call this resource?", defaultName)
		if err != nil {
			return "", err
		}
		if err := w.validateResourceName(name); err != nil {
			w.message(err.Error())
			continue
		}
		return name, nil
	}
}

func (w *Wizard) validateResourceName(name string) error {
	if name == "" {
		return fmt.Errorf("resource name cannot be empty")
	}
	if err := resources.ValidateEntryName(name); err != nil {
		return err
	}
	if !labelPattern.MatchString(name) {
		return fmt.Errorf("resource name must end with a lowercase letter or digit")
	}
	if _, exists := w.Resources[name]; exists {
		return fmt.Errorf("resource name %q is already in use", name)
	}
	if _, exists := w.Services[name]; exists {
		return fmt.Errorf("service name %q is already in use", name)
	}
	return nil
}

func (w *Wizard) message(message string) {
	writer := w.Writer
	if writer == nil {
		writer = io.Discard
	}
	fmt.Fprintln(writer, message)
}

func (w *Wizard) label(ctx context.Context, message, defaultValue string, blob bool) (string, error) {
	for {
		name, err := w.text(ctx, message, defaultValue)
		if err != nil {
			return "", err
		}
		if !labelPattern.MatchString(name) || (blob && (len(name) < 3 || strings.Contains(name, "--"))) {
			w.message("Use lowercase letters, digits and hyphens, starting and ending with a letter or digit.")
			continue
		}
		return name, nil
	}
}

func (w *Wizard) uniqueName(base string) string {
	base = strings.ToLower(base)
	base = regexp.MustCompile(`[^a-z0-9-]+`).ReplaceAllString(base, "-")
	base = strings.Trim(base, "-")
	if base == "" || base[0] < 'a' || base[0] > 'z' {
		base = "resource-" + base
	}
	if len(base) > 35 {
		base = base[:35]
	}
	name := base
	for i := 2; w.validateResourceName(name) != nil; i++ {
		name = fmt.Sprintf("%s-%d", base, i)
	}
	return name
}
