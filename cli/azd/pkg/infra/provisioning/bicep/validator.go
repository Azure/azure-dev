// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

// validateValueRange ensures the string can be parsed as an integer with strconv.ParseInt and is within the provided min
// and max (nil meaning there is no min or max)
func validateValueRange(key string, minValue *int, maxValue *int) func(string) error {
	return func(s string) error {
		v, err := strconv.ParseInt(s, 10, 0)
		if err != nil {
			return fmt.Errorf("failed to convert %s to an integer: %w", s, err)
		}

		if minValue != nil && int(v) < *minValue {
			return fmt.Errorf("value for '%s' must be at least '%d'", key, *minValue)
		}

		if maxValue != nil && int(v) > *maxValue {
			return fmt.Errorf("value for '%s' must be at most '%d'", key, *maxValue)
		}

		return nil
	}
}

// validateLengthRange ensures the length of the string is within the provided min and max (nil meaning there is no bound)
func validateLengthRange(key string, minLength *int, maxLength *int) func(string) error {
	return func(s string) error {
		if minLength != nil && len(s) < *minLength {
			return fmt.Errorf("value for '%s' must be at least '%d' in length", key, *minLength)
		}

		if maxLength != nil && len(s) > *maxLength {
			return fmt.Errorf("value for '%s' must be at most '%d' in length", key, *maxLength)
		}

		return nil
	}
}

// validateJsonObject returns an error if json.Unmarshal fails to unmarshal s as an []any
func validateJsonArray(s string) error {
	var v []any
	err := json.Unmarshal([]byte(s), &v)
	if err != nil {
		return fmt.Errorf("failed to parse value as JSON array: %w", err)
	}

	return nil
}

// validateJsonObject returns an error if json.Unmarshal fails to unmarshal s as a map[string]any
func validateJsonObject(s string) error {
	var v map[string]any
	err := json.Unmarshal([]byte(s), &v)
	if err != nil {
		return fmt.Errorf("failed to parse value as JSON object: %w", err)
	}

	return nil
}

// promptWithValidation prompts for a value using the console and then validates that it satisfies all the validation
// functions. If any fail, the prompt is retried after printing the error (prefixed with "Error: ") to the console.
// If there are is an error prompting it is returned as is.
func promptWithValidation(
	ctx context.Context, console input.Console, options input.ConsoleOptions, validators ...func(string) error,
) (string, error) {
	for {
		userValue, err := console.Prompt(ctx, options)
		if err != nil {
			return "", err
		}

		isValid := true

		for _, validator := range validators {
			if err := validator(userValue); err != nil {
				console.Message(ctx, output.WithErrorFormat("Error: %s.", err))
				isValid = false
				break
			}
		}

		if isValid {
			return userValue, nil
		}
	}
}
