// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package mcp

import (
	"context"
	"fmt"
	"math/big"
	"slices"
	"strconv"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal/agent/consent"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// McpElicitationHandler handles elicitation requests from MCP clients by prompting the user for input
type McpElicitationHandler struct {
	consentManager consent.ConsentManager
	console        input.Console
}

// promptInfo contains the title and description for a property prompt
type promptInfo struct {
	Title       string
	Description string
}

// NewMcpElicitationHandler creates a new MCP elicitation handler with the specified consent manager and console
func NewMcpElicitationHandler(consentManager consent.ConsentManager, console input.Console) client.ElicitationHandler {
	return &McpElicitationHandler{
		consentManager: consentManager,
		console:        console,
	}
}

// Elicit implements client.ElicitationHandler.
func (h *McpElicitationHandler) Elicit(ctx context.Context, request mcp.ElicitationRequest) (*mcp.ElicitationResult, error) {
	// Get current executing tool for context (package-level tracking)
	currentTool := consent.GetCurrentExecutingTool()
	if currentTool == nil {
		return nil, fmt.Errorf("no current tool executing")
	}

	// Check consent for sampling if consent manager is available
	if err := h.checkConsent(ctx, currentTool); err != nil {
		return &mcp.ElicitationResult{
			ElicitationResponse: mcp.ElicitationResponse{
				Action: mcp.ElicitationResponseActionDecline,
			},
		}, nil
	}

	const root = "mem://schema.json"
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(root, request.Params.RequestedSchema); err != nil {
		return nil, err
	}

	schema, err := compiler.Compile(root)
	if err != nil {
		return nil, err
	}

	results := map[string]any{}

	h.console.Message(ctx, "")
	h.console.Message(ctx, request.Params.Message)
	h.console.Message(ctx, "")

	// Sort properties to show required fields first, then optional fields
	orderedKeys := h.getOrderedPropertyKeys(schema)

	for _, key := range orderedKeys {
		property := schema.Properties[key]
		value, err := h.promptForValue(ctx, key, property, schema)
		if err != nil {
			return nil, err
		}
		results[key] = value
	}

	h.console.Message(ctx, "")

	return &mcp.ElicitationResult{
		ElicitationResponse: mcp.ElicitationResponse{
			Action:  mcp.ElicitationResponseActionCancel,
			Content: results,
		},
	}, nil
}

func (h *McpElicitationHandler) promptForValue(
	ctx context.Context,
	key string,
	property *jsonschema.Schema,
	root *jsonschema.Schema,
) (any, error) {
	required := slices.Contains(root.Required, key)

	// Check for enum first (regardless of type)
	if property.Enum != nil && len(property.Enum.Values) > 0 {
		return h.promptForEnum(ctx, key, property, required)
	}

	// Handle by type - need to check Types field
	if property.Types != nil {
		typeStrings := property.Types.ToStrings()
		if len(typeStrings) > 0 {
			primaryType := typeStrings[0] // Use first type for now
			switch primaryType {
			case "boolean":
				return h.promptForBoolean(ctx, key, property, required)
			case "integer":
				return h.promptForInteger(ctx, key, property, required)
			case "number":
				return h.promptForNumber(ctx, key, property, required)
			case "string":
				return h.promptForString(ctx, key, property, required)
			default:
				// Fall back to string prompt for unsupported types
				return h.promptForString(ctx, key, property, required)
			}
		}
	}

	// Fall back to string prompt if no type information
	return h.promptForString(ctx, key, property, required)
}

// promptForString handles string input with validation for length and pattern
func (h *McpElicitationHandler) promptForString(
	ctx context.Context,
	key string,
	property *jsonschema.Schema,
	required bool,
) (any, error) {
	promptInfo := h.getPromptInfo(key, property, "string")

	validationFn := func(input string) (bool, string) {
		// Check minimum length
		if property.MinLength != nil && len(input) < *property.MinLength {
			return false, fmt.Sprintf("Must be at least %d characters", *property.MinLength)
		}

		// Check maximum length
		if property.MaxLength != nil && len(input) > *property.MaxLength {
			return false, fmt.Sprintf("Must be no more than %d characters", *property.MaxLength)
		}

		// Check pattern
		if property.Pattern != nil {
			matched := property.Pattern.MatchString(input)
			if !matched {
				return false, "Does not match required pattern"
			}
		}

		return true, ""
	}

	prompt := ux.NewPrompt(&ux.PromptOptions{
		Message:      promptInfo.Title,
		Required:     required,
		HelpMessage:  promptInfo.Description,
		ValidationFn: validationFn,
	})

	return prompt.Ask(ctx)
}

// promptForInteger handles integer input with validation for ranges
func (h *McpElicitationHandler) promptForInteger(
	ctx context.Context,
	key string,
	property *jsonschema.Schema,
	required bool,
) (any, error) {
	promptInfo := h.getPromptInfo(key, property, "integer")

	validationFn := func(input string) (bool, string) {
		if input == "" {
			return !required, ""
		}

		value, err := strconv.ParseInt(input, 10, 64)
		if err != nil {
			return false, "Must be a valid whole number"
		}

		valueRat := big.NewRat(value, 1)

		// Check minimum value
		if property.Minimum != nil && valueRat.Cmp(property.Minimum) < 0 {
			minFloat, _ := property.Minimum.Float64()
			return false, fmt.Sprintf("Must be at least %.0f", minFloat)
		}

		// Check maximum value
		if property.Maximum != nil && valueRat.Cmp(property.Maximum) > 0 {
			maxFloat, _ := property.Maximum.Float64()
			return false, fmt.Sprintf("Must be no more than %.0f", maxFloat)
		}

		// Check exclusive minimum
		if property.ExclusiveMinimum != nil && valueRat.Cmp(property.ExclusiveMinimum) <= 0 {
			minFloat, _ := property.ExclusiveMinimum.Float64()
			return false, fmt.Sprintf("Must be greater than %.0f", minFloat)
		}

		// Check exclusive maximum
		if property.ExclusiveMaximum != nil && valueRat.Cmp(property.ExclusiveMaximum) >= 0 {
			maxFloat, _ := property.ExclusiveMaximum.Float64()
			return false, fmt.Sprintf("Must be less than %.0f", maxFloat)
		}

		return true, ""
	}

	prompt := ux.NewPrompt(&ux.PromptOptions{
		Message:      promptInfo.Title,
		Required:     required,
		HelpMessage:  promptInfo.Description,
		ValidationFn: validationFn,
	})

	result, err := prompt.Ask(ctx)
	if err != nil {
		return nil, err
	}

	if result == "" {
		return nil, nil
	}

	// Convert to int64 for JSON serialization
	value, _ := strconv.ParseInt(result, 10, 64)
	return value, nil
}

// promptForNumber handles number input with validation for ranges
func (h *McpElicitationHandler) promptForNumber(
	ctx context.Context,
	key string,
	property *jsonschema.Schema,
	required bool,
) (any, error) {
	promptInfo := h.getPromptInfo(key, property, "number")

	validationFn := func(input string) (bool, string) {
		if input == "" {
			return !required, ""
		}

		value, err := strconv.ParseFloat(input, 64)
		if err != nil {
			return false, "Must be a valid number"
		}

		valueRat := big.NewRat(0, 1)
		valueRat.SetFloat64(value)

		// Check minimum value
		if property.Minimum != nil && valueRat.Cmp(property.Minimum) < 0 {
			minFloat, _ := property.Minimum.Float64()
			return false, fmt.Sprintf("Must be at least %g", minFloat)
		}

		// Check maximum value
		if property.Maximum != nil && valueRat.Cmp(property.Maximum) > 0 {
			maxFloat, _ := property.Maximum.Float64()
			return false, fmt.Sprintf("Must be no more than %g", maxFloat)
		}

		// Check exclusive minimum
		if property.ExclusiveMinimum != nil && valueRat.Cmp(property.ExclusiveMinimum) <= 0 {
			minFloat, _ := property.ExclusiveMinimum.Float64()
			return false, fmt.Sprintf("Must be greater than %g", minFloat)
		}

		// Check exclusive maximum
		if property.ExclusiveMaximum != nil && valueRat.Cmp(property.ExclusiveMaximum) >= 0 {
			maxFloat, _ := property.ExclusiveMaximum.Float64()
			return false, fmt.Sprintf("Must be less than %g", maxFloat)
		}

		return true, ""
	}

	prompt := ux.NewPrompt(&ux.PromptOptions{
		Message:      promptInfo.Title,
		Required:     required,
		HelpMessage:  promptInfo.Description,
		ValidationFn: validationFn,
	})

	result, err := prompt.Ask(ctx)
	if err != nil {
		return nil, err
	}

	if result == "" {
		return nil, nil
	}

	// Convert to float64 for JSON serialization
	value, _ := strconv.ParseFloat(result, 64)
	return value, nil
}

// promptForBoolean handles boolean input using confirmation prompt
func (h *McpElicitationHandler) promptForBoolean(
	ctx context.Context,
	key string,
	property *jsonschema.Schema,
	required bool,
) (any, error) {
	promptInfo := h.getPromptInfo(key, property, "boolean")

	var defaultValue *bool
	if !required {
		// For optional booleans, use nil as default
		defaultValue = nil
	}

	// Make the message more question-like for booleans
	message := promptInfo.Title
	if !strings.HasSuffix(strings.ToLower(message), "?") &&
		!strings.Contains(strings.ToLower(message), " enable ") &&
		!strings.Contains(strings.ToLower(message), " allow ") &&
		!strings.Contains(strings.ToLower(message), " use ") {
		message = message + "?"
	}

	confirm := ux.NewConfirm(&ux.ConfirmOptions{
		Message:      message,
		DefaultValue: defaultValue,
		HelpMessage:  promptInfo.Description,
	})

	result, err := confirm.Ask(ctx)
	if err != nil {
		return nil, err
	}

	// Return the boolean value or nil for optional fields
	if result == nil {
		return nil, nil
	}
	return *result, nil
}

// promptForEnum handles enum selection using select prompt
func (h *McpElicitationHandler) promptForEnum(
	ctx context.Context,
	key string,
	property *jsonschema.Schema,
	required bool,
) (any, error) {
	promptInfo := h.getPromptInfo(key, property, "enum")

	choices := make([]*ux.SelectChoice, 0, len(property.Enum.Values))

	// Add "None" option for optional enums
	if !required {
		choices = append(choices, &ux.SelectChoice{
			Value: "",
			Label: "None",
		})
	}

	// Add enum values as choices
	for _, enumValue := range property.Enum.Values {
		// Convert enum value to string for display
		valueStr := fmt.Sprintf("%v", enumValue)
		choices = append(choices, &ux.SelectChoice{
			Value: valueStr,
			Label: valueStr,
		})
	}

	selectPrompt := ux.NewSelect(&ux.SelectOptions{
		Message:     promptInfo.Title,
		Choices:     choices,
		HelpMessage: promptInfo.Description,
	})

	selectedIndex, err := selectPrompt.Ask(ctx)
	if err != nil {
		return nil, err
	}

	if selectedIndex == nil {
		return nil, nil
	}

	selectedChoice := choices[*selectedIndex]
	if selectedChoice.Value == "" {
		return nil, nil
	}

	// Adjust index if "None" option was added
	enumIndex := *selectedIndex
	if !required {
		enumIndex--
	}

	if enumIndex >= 0 && enumIndex < len(property.Enum.Values) {
		return property.Enum.Values[enumIndex], nil
	}

	return selectedChoice.Value, nil
}

// checkConsent checks consent for sampling requests using the current executing tool
func (h *McpElicitationHandler) checkConsent(
	ctx context.Context,
	currentTool *consent.ExecutingTool,
) error {
	// Create a consent checker for this specific server
	consentChecker := consent.NewConsentChecker(h.consentManager, currentTool.Server)

	// Check elicitation consent using the consent checker
	decision, err := consentChecker.CheckElicitationConsent(ctx, currentTool.Name)
	if err != nil {
		return fmt.Errorf("consent check failed: %w", err)
	}

	if !decision.Allowed {
		if decision.RequiresPrompt {
			// Use console.DoInteraction to show consent prompt
			if err := h.console.DoInteraction(func() error {
				return consentChecker.PromptAndGrantElicitationConsent(
					ctx,
					currentTool.Name,
					"Allows requesting additional information from the user",
				)
			}); err != nil {
				return err
			}
		} else {
			return fmt.Errorf("sampling denied: %s", decision.Reason)
		}
	}

	return nil
}

// getPromptInfo extracts user-friendly title and description from a property schema
func (h *McpElicitationHandler) getPromptInfo(key string, property *jsonschema.Schema, promptType string) promptInfo {
	info := promptInfo{}

	// Use title if available, otherwise use the key
	if property.Title != "" {
		info.Title = property.Title
	} else {
		info.Title = key
	}

	// Use existing description if available, otherwise generate one
	if property.Description != "" {
		info.Description = property.Description
	} else {
		info.Description = h.generateDescription(property, promptType)
	}

	return info
}

// generateDescription creates a user-friendly description based on the property schema
func (h *McpElicitationHandler) generateDescription(property *jsonschema.Schema, promptType string) string {
	var parts []string

	switch promptType {
	case "string":
		parts = append(parts, "Enter text")
		if property.MinLength != nil && property.MaxLength != nil {
			parts = append(parts, fmt.Sprintf("(%d-%d characters)", *property.MinLength, *property.MaxLength))
		} else if property.MinLength != nil {
			parts = append(parts, fmt.Sprintf("(at least %d characters)", *property.MinLength))
		} else if property.MaxLength != nil {
			parts = append(parts, fmt.Sprintf("(up to %d characters)", *property.MaxLength))
		}
	case "integer":
		parts = append(parts, "Enter a whole number")
		if property.Minimum != nil && property.Maximum != nil {
			minFloat, _ := property.Minimum.Float64()
			maxFloat, _ := property.Maximum.Float64()
			parts = append(parts, fmt.Sprintf("(between %.0f and %.0f)", minFloat, maxFloat))
		} else if property.Minimum != nil {
			minFloat, _ := property.Minimum.Float64()
			parts = append(parts, fmt.Sprintf("(%.0f or higher)", minFloat))
		} else if property.Maximum != nil {
			maxFloat, _ := property.Maximum.Float64()
			parts = append(parts, fmt.Sprintf("(%.0f or lower)", maxFloat))
		}
	case "number":
		parts = append(parts, "Enter a number")
		if property.Minimum != nil && property.Maximum != nil {
			minFloat, _ := property.Minimum.Float64()
			maxFloat, _ := property.Maximum.Float64()
			parts = append(parts, fmt.Sprintf("(between %g and %g)", minFloat, maxFloat))
		} else if property.Minimum != nil {
			minFloat, _ := property.Minimum.Float64()
			parts = append(parts, fmt.Sprintf("(%g or higher)", minFloat))
		} else if property.Maximum != nil {
			maxFloat, _ := property.Maximum.Float64()
			parts = append(parts, fmt.Sprintf("(%g or lower)", maxFloat))
		}
	case "boolean":
		parts = append(parts, "Choose yes or no")
	case "enum":
		if property.Enum != nil && len(property.Enum.Values) > 0 {
			parts = append(parts, "Select one option from the list")
		} else {
			parts = append(parts, "Select an option")
		}
	default:
		parts = append(parts, "Enter a value")
	}

	return strings.Join(parts, " ")
}

// getOrderedPropertyKeys returns property keys ordered with required properties first, then optional
func (h *McpElicitationHandler) getOrderedPropertyKeys(schema *jsonschema.Schema) []string {
	var requiredKeys []string
	var optionalKeys []string

	for key := range schema.Properties {
		if slices.Contains(schema.Required, key) {
			requiredKeys = append(requiredKeys, key)
		} else {
			optionalKeys = append(optionalKeys, key)
		}
	}

	// Concatenate required keys first, then optional keys
	result := make([]string, 0, len(requiredKeys)+len(optionalKeys))
	result = append(result, requiredKeys...)
	result = append(result, optionalKeys...)

	return result
}
